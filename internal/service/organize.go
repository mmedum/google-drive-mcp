package service

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mmedum/google-drive-mcp/v2/internal/config"
	"github.com/mmedum/google-drive-mcp/v2/internal/gapi"
	"github.com/mmedum/google-drive-mcp/v2/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v2/internal/model"
	"github.com/mmedum/google-drive-mcp/v2/internal/render"
)

// CreateFolderInput describes a folder to create.
type CreateFolderInput struct {
	Name           string
	Parent         string
	Description    string
	Color          string
	AllowDuplicate bool
}

// CreateFolder makes a folder, refusing a second one of the same name
// beside the first unless it is asked for explicitly.
func (s *Service) CreateFolder(ctx context.Context, in CreateFolderInput) (*Result, error) {
	if err := s.writable("create_folder"); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, Errorf(ClassInvalid, "name is required")
	}
	color, err := folderColor(in.Color)
	if err != nil {
		return nil, err
	}
	parent, err := s.parentFolder(ctx, in.Parent)
	if err != nil {
		return nil, err
	}
	if err := s.refuseDuplicate(ctx, parent, name, in.AllowDuplicate); err != nil {
		return nil, err
	}
	meta := &gdrive.FileMeta{Name: name, MimeType: gdrive.MimeFolder, Parents: []string{parent.ID}}
	if in.Description != "" {
		meta.Description = gdrive.String(in.Description)
	}
	if color != "" {
		meta.FolderColorRgb = gdrive.String(color)
	}
	if err := s.assignID(ctx, meta); err != nil {
		return nil, err
	}
	f, err := s.api.CreateFile(ctx, meta, gapi.WriteOptions{ResourceIDs: []string{parent.ID}})
	if err != nil {
		return nil, wrap(err, fmt.Sprintf("creating the folder %q in %s", name, parent.Name))
	}
	return s.write(ctx, f, outcome{Action: render.ActionCreated})
}

// UpdateFileInput is a metadata patch: only the fields passed change.
type UpdateFileInput struct {
	File        string
	Name        string
	Description *string
	Starred     *bool
	Color       string
	// Properties are custom key-value pairs; an empty value deletes a key.
	Properties map[string]string
	// CopyRequiresWriterPermission is Drive's legacy download switch.
	// false also lifts a restriction on editors.
	CopyRequiresWriterPermission *bool
	WritersCanShare              *bool
	// RestrictDownload is who cannot download, print or copy the file:
	// none, viewers (and commenters), or editors as well. Not together
	// with CopyRequiresWriterPermission.
	RestrictDownload string
	// LimitedAccess turns a folder's limited access on or off. Off lets
	// everyone who reaches the folder above open it, so it asks.
	LimitedAccess *bool
	// Viewed marks the file as seen by the signed-in person, which is
	// what puts it in Drive's Recent view. Only true does anything:
	// viewedByMeTime is a timestamp, and Drive offers no way to say a
	// file was never opened.
	Viewed bool
}

// UpdateFile renames, describes, stars, colors or tags a file. It
// reports every field before and after, so a patch that did nothing is
// visible as one.
func (s *Service) UpdateFile(ctx context.Context, in UpdateFileInput) (*Result, error) {
	if err := s.writable("update_file"); err != nil {
		return nil, err
	}
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: false, Fresh: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	color, err := folderColor(in.Color)
	if err != nil {
		return nil, err
	}
	if color != "" && !f.IsFolder() && !f.IsShortcut() {
		return nil, Errorf(ClassInvalid, "color is a folder's, and %s is %s", f.Name, model.KindWithArticle(f))
	}

	p, err := metaPatch(f, in, color, s.now())
	if err != nil {
		return nil, err
	}
	meta, changes := p.meta, p.changes
	// The sharing switches loosen a file the way a shared drive's
	// restrictions do, and GDRIVE_SHARING=off refuses them the same way.
	if loosened := p.loosening(); len(loosened) > 0 && s.opts.Sharing == config.SharingOff {
		return nil, Errorf(ClassForbidden, "this server was started with GDRIVE_SHARING=off, and %s on %s would "+
			"widen who can reach or pass on what is in it. Nothing was changed. Tightening is still allowed.",
			strings.Join(loosened, " and "), f.Name)
	}

	if len(changes) == 0 {
		return s.report(ctx, res, outcome{Action: render.ActionUnchanged,
			Note: "nothing to do: every field passed already had that value."}), nil
	}
	if meta.InheritedPermissionsDisabled != nil && !*meta.InheritedPermissionsDisabled {
		if err := s.askOpenFolder(ctx, res); err != nil {
			return nil, err
		}
	}

	updated, err := s.api.UpdateFile(ctx, f.ID, meta, gapi.UpdateOptions{})
	if err != nil {
		return nil, wrap(err, "updating "+f.Name)
	}
	// A rename makes every path that led here wrong.
	return s.write(ctx, updated, outcome{Action: render.ActionUpdated, Changes: changes, Moved: meta.Name != ""})
}

// patch is what update_file sends, the before and after that go with
// it, and whether it lifts the file's download restriction, which only
// the level it asks for and the level the file has can say.
type patch struct {
	meta           *gdrive.FileMeta
	changes        []render.Change
	liftsDownloads bool
}

// loosening names what a patch does that lets more people reach or pass
// on the file: letting viewers copy it, editors reshare it, lifting a
// download restriction, or turning a folder's limited access off.
func (p *patch) loosening() []string {
	var out []string
	if v := p.meta.CopyRequiresWriterPermission; v != nil && !*v {
		out = append(out, "letting viewers copy it")
	}
	if v := p.meta.WritersCanShare; v != nil && *v {
		out = append(out, "letting editors reshare it")
	}
	if p.liftsDownloads {
		out = append(out, "lifting its download restriction")
	}
	if v := p.meta.InheritedPermissionsDisabled; v != nil && !*v {
		out = append(out, "turning its limited access off")
	}
	return out
}

// downloadRank orders the levels from least to most restricted.
func downloadRank(level string) int { return slices.Index(model.DownloadLevels(), level) }

// askOpenFolder puts it to the person before a folder's limited access
// goes off, naming who could then open it: everyone who reaches the
// folder above and does not reach this one now. That is what a move
// into the folder above would give it, so it is worked out by the same
// prediction. A list that cannot be read asks too.
func (s *Service) askOpenFolder(ctx context.Context, res *Resolved) error {
	f := res.File
	p := &movePlan{res: res, target: &gdrive.File{ID: f.Parent(), MimeType: gdrive.MimeFolder}}
	above := reach{}
	if parent, err := s.Resolve(ctx, f.Parent(), ResolveOptions{FollowShortcut: false}); f.Parent() != "" && err == nil {
		p.target, above = parent.File, s.reachOf(ctx, parent.File)
	}
	s.predictMove(ctx, p, above)
	if !p.widens() {
		return nil
	}
	unread := p.unread
	if unread == "the destination" {
		unread = "the folder above it"
	}
	return ask(ctx, render.AskOpenFolder(f.ID, f.Name, p.gained, unread))
}

// metaPatch works out the smallest patch that turns f into what the
// caller asked for, and the before-and-after list that goes with it. A
// field whose value is already what was asked for is left out of both:
// Drive would accept it, and the result would claim a change that did
// not happen.
func metaPatch(f *gdrive.File, in UpdateFileInput, color string, now time.Time) (*patch, error) {
	meta := &gdrive.FileMeta{}
	p := &patch{meta: meta}
	var changes []render.Change
	if name := strings.TrimSpace(in.Name); name != "" && name != f.Name {
		meta.Name = name
		changes = append(changes, render.Change{Field: "name", From: f.Name, To: name})
	}
	if in.Description != nil && *in.Description != f.Description {
		meta.Description = in.Description
		changes = append(changes, render.Change{Field: "description", From: f.Description, To: *in.Description})
	}
	if in.Starred != nil && *in.Starred != f.Starred {
		meta.Starred = in.Starred
		changes = append(changes, render.Change{Field: "starred", From: yesNo(f.Starred), To: yesNo(*in.Starred)})
	}
	if color != "" && color != f.FolderColorRgb {
		meta.FolderColorRgb = gdrive.String(color)
		changes = append(changes, render.Change{Field: "color", From: f.FolderColorRgb, To: color})
	}
	if in.CopyRequiresWriterPermission != nil && *in.CopyRequiresWriterPermission != f.CopyRequiresWriterPermission {
		meta.CopyRequiresWriterPermission = in.CopyRequiresWriterPermission
		changes = append(changes, render.Change{Field: "viewers and commenters may copy, print and download",
			From: yesNo(!f.CopyRequiresWriterPermission), To: yesNo(!*in.CopyRequiresWriterPermission)})
		// The files guide: false sets both restrictedForReaders and
		// restrictedForWriters to false, so editors are let go too.
		if !*in.CopyRequiresWriterPermission && model.ItemDownloads(f) == model.DownloadsEditors {
			changes = append(changes, render.Change{Field: "editors may copy, print and download",
				From: yesNo(false), To: yesNo(true)})
		}
	}
	restrictionChanges, err := restrictionPatch(f, in, p)
	if err != nil {
		return nil, err
	}
	changes = append(changes, restrictionChanges...)
	if in.WritersCanShare != nil && *in.WritersCanShare != f.WritersCanShare {
		meta.WritersCanShare = in.WritersCanShare
		changes = append(changes, render.Change{Field: "editors may change sharing",
			From: yesNo(f.WritersCanShare), To: yesNo(*in.WritersCanShare)})
	}
	if in.Viewed {
		// No "already that value" check: the field is a timestamp, so
		// marking a file seen again moves it up the Recent view, which is
		// the point of asking.
		meta.ViewedByMeTime = now.UTC().Format(time.RFC3339)
		changes = append(changes, render.Change{Field: "last opened by you",
			From: orNever(f.ViewedByMeTime), To: meta.ViewedByMeTime})
	}
	propertyChanges, err := propertyPatch(f, in.Properties, meta)
	if err != nil {
		return nil, err
	}
	changes = append(changes, propertyChanges...)
	p.changes = changes
	return p, nil
}

// restrictionPatch adds a download restriction and a folder's limited
// access to the patch, refusing what Drive says this account may not
// change before Drive is asked.
func restrictionPatch(f *gdrive.File, in UpdateFileInput, p *patch) ([]render.Change, error) {
	meta := p.meta
	var changes []render.Change
	if want := strings.ToLower(strings.TrimSpace(in.RestrictDownload)); want != "" {
		switch {
		case !slices.Contains(model.DownloadLevels(), want):
			return nil, Errorf(ClassInvalid, "restrict_download %q is not one of %s", in.RestrictDownload,
				strings.Join(model.DownloadLevels(), ", "))
		case in.CopyRequiresWriterPermission != nil:
			return nil, Errorf(ClassInvalid, "pass restrict_download or the legacy copy_requires_writer_permission, "+
				"not both: Google warns the two can conflict")
		}
		if was := model.ItemDownloads(f); want != was {
			if f.Capabilities != nil && !f.Capabilities.CanChangeItemDownloadRestriction {
				return nil, Errorf(ClassForbidden, "this account cannot change who may download %s: Drive lets "+
					"its owner, or an organizer of its shared drive, do that", f.Name)
			}
			meta.DownloadRestrictions = &gdrive.DownloadRestrictionsPatch{ItemDownloadRestriction: model.DownloadRestriction(want)}
			p.liftsDownloads = downloadRank(want) < downloadRank(was)
			changes = append(changes, render.Change{Field: "who cannot download, print or copy it, as set on the file",
				From: model.DownloadStops(was), To: model.DownloadStops(want)})
		}
	}
	if in.LimitedAccess == nil {
		return changes, nil
	}
	if !f.IsFolder() {
		return nil, Errorf(ClassInvalid, "limited access is a folder's: Drive does not offer it on files, and %s is %s",
			f.Name, model.KindWithArticle(f))
	}
	on := *in.LimitedAccess
	if on == f.InheritedPermissionsDisabled {
		return changes, nil
	}
	if c := f.Capabilities; c != nil && (on && !c.CanDisableInheritedPermissions || !on && !c.CanEnableInheritedPermissions) {
		return nil, Errorf(ClassForbidden, "this account cannot turn limited access %s on %s: Drive lets its owner, "+
			"an organizer of its shared drive, or in My Drive an editor allowed to share, do that", onOff(on), f.Name)
	}
	meta.InheritedPermissionsDisabled = in.LimitedAccess
	return append(changes, render.Change{Field: "limited access", From: yesNo(!on), To: yesNo(on)}), nil
}

// onOff is a switch's state in words.
func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// propertyPatch adds the custom-property changes to meta. Drive clears a
// key when its value is null, which is what an empty value asks for.
func propertyPatch(f *gdrive.File, want map[string]string, meta *gdrive.FileMeta) ([]render.Change, error) {
	var changes []render.Change
	for key, value := range want {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, Errorf(ClassInvalid, "a property key cannot be empty")
		}
		was, had := f.Properties[key]
		if value == "" && !had {
			continue
		}
		if had && was == value {
			continue
		}
		if meta.Properties == nil {
			meta.Properties = map[string]*string{}
		}
		if value == "" {
			meta.Properties[key] = nil
			changes = append(changes, render.Change{Field: "property " + key, From: was, To: "(deleted)"})
			continue
		}
		meta.Properties[key] = gdrive.String(value)
		changes = append(changes, render.Change{Field: "property " + key, From: was, To: value})
	}
	return changes, nil
}

// CopyFileInput describes a copy.
type CopyFileInput struct {
	File string
	Name string
	To   string
	// ConvertTo asks Google to import the copy as one of its own kinds,
	// which is how a PDF or a scan becomes a document with searchable
	// text.
	ConvertTo   string
	OCRLanguage string
	// KeepRevisionForever pins the copy's first revision.
	KeepRevisionForever bool
	// CopyComments brings the file's comment threads along. Off by
	// default: a copy usually starts a fresh conversation, and comments
	// carry other people's words to wherever the copy lands.
	CopyComments   bool
	AllowDuplicate bool
	// Recursive copies a folder and everything inside it. Drive has no
	// call for that, so it is a walk and a write per item, which is why
	// it has to be asked for.
	Recursive bool
	// MaxItems bounds what a recursive copy will attempt. Over it, the
	// copy is refused before it starts rather than stopped halfway.
	MaxItems int
	DryRun   bool
}

// CopyFile copies one file, optionally converting it as Google imports
// it, or — with recursive — a folder and everything inside it.
//
// A copy takes on who can reach the folder it lands in, and none of the
// grants made on the original, so it can reach people the original does
// not. The rule is the move's (§4a): the result shows who reaches the
// original and who reaches the copy, a copy that reaches more people is
// put to the person first, and with GDRIVE_SHARING=off it is refused.
func (s *Service) CopyFile(ctx context.Context, in CopyFileInput) (*Result, error) {
	if err := s.writable("copy_file"); err != nil {
		return nil, err
	}
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	if f.IsFolder() {
		if !in.Recursive {
			return nil, Errorf(ClassInvalid, "%s is a folder, and Drive has no call that copies one: it is a "+
				"walk and a write for every item inside. Pass recursive: true to do that, and dry_run: true "+
				"first to see how much it is.", f.Name)
		}
		if strings.TrimSpace(in.ConvertTo) != "" {
			return nil, Errorf(ClassInvalid, "convert_to asks Google to import a file as one of its own "+
				"kinds, and a folder is not a file with content to import. Copy the folder, then convert "+
				"what is inside it.")
		}
		return s.copyTree(ctx, res, in)
	}
	if in.Recursive {
		return nil, Errorf(ClassInvalid, "%s is %s, not a folder; recursive is for copying a folder and "+
			"everything inside it. Leave it out to copy this.", f.Name, model.KindWithArticle(f))
	}
	convert, err := convertTarget(in.ConvertTo)
	if err != nil {
		return nil, err
	}
	if f.Capabilities != nil && !f.Capabilities.CanCopy {
		return nil, Errorf(ClassForbidden, "this account cannot copy %s. Its owner may have turned copying off "+
			"for viewers and commenters.", f.Name)
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		// Drive's own default, so a copy made here looks like a copy made
		// in the web interface.
		name = "Copy of " + f.Name
	}
	place, err := s.copyPlace(ctx, f, in.To, name, in.AllowDuplicate)
	if err != nil {
		return nil, err
	}
	parentID, parentName, target := place.id, place.name, place.target

	meta := &gdrive.FileMeta{Name: name, MimeType: convert}
	if parentID != "" {
		meta.Parents = []string{parentID}
	}
	// A copy of a Google Doc is a Google Doc unless it is being
	// converted, and what the copy becomes is what decides whether an id
	// may be sent with it.
	if err := s.checkConversion(ctx, f.MimeType, convert); err != nil {
		return nil, err
	}
	becomes := convert
	if becomes == "" {
		becomes = f.MimeType
	}
	parts := s.predictCopy(ctx, res, nil, target)
	if err := s.copyRefused(parts); err != nil {
		return nil, err
	}
	if in.DryRun {
		where := parentName
		if where == "" {
			where = "the folder it is in now"
		}
		note := fmt.Sprintf("Would copy it as %s into %s", name, where)
		if convert != "" {
			note += ", imported as " + model.KindName(convert)
		}
		note = strings.TrimSpace(note + ". " + sentence(s.moveNote(ctx, parts[0], parts[0].after, true)))
		return s.copyResult(ctx, res, parts[0].before, parts[0].after, outcome{Action: render.ActionCopied,
			DryRun: true, Note: note + " Nothing was copied."}), nil
	}
	if err := s.askCopy(ctx, target, parts); err != nil {
		return nil, err
	}
	if err := s.assignIDFor(ctx, meta, becomes); err != nil {
		return nil, err
	}
	copied, err := s.api.CopyFile(ctx, f.ID, meta, gapi.WriteOptions{
		OCRLanguage: in.OCRLanguage, KeepRevisionForever: in.KeepRevisionForever,
		CopyComments: in.CopyComments, ResourceIDs: []string{parentID},
	})
	if err != nil {
		return nil, wrap(err, fmt.Sprintf("copying %s%s", f.Name, listOrNothing([]string{parentName}, " into ", "")))
	}
	s.forget(copied, false)
	after := s.sharingNow(ctx, copied)
	notes := []string{}
	if note := s.moveNote(ctx, parts[0], after, false); note != "" {
		notes = append(notes, sentence(note))
	}
	if convert != "" {
		notes = append(notes, fmt.Sprintf("Google imported the copy as %s. The original %s is untouched.",
			model.KindName(convert), f.Name))
	}
	if in.CopyComments {
		// What was ASKED for, not what happened. files.copy answers with a
		// File and says nothing about comments, so this cannot know: the
		// sentence here used to be "the comment threads were copied with
		// it", which is the lock_file mistake with somebody else's words
		// in place of a restriction. Reading it back was the other option
		// and was rejected: comments.list lags a copy, so an empty answer
		// would report threads as dropped when they were merely late,
		// which is the empty_trash mistake in the opposite direction. The
		// caller is pointed at the one call that settles it instead.
		notes = append(notes, "Drive was asked to bring the comment threads along, and its answer "+
			"says nothing about whether it did. It does not always: one live run found a Google "+
			"Doc's threads came across and an uploaded CSV's did not, same run, same argument. So "+
			"check rather than assume — list_comments on the copy shows what came across, though "+
			"it can lag a copy by a moment. Anything that did is what other people wrote, now "+
			"readable by everybody who can see the copy.")
	}
	return s.copyResult(ctx, &Resolved{File: copied}, parts[0].before, after, outcome{Action: render.ActionCopied,
		Note: strings.Join(notes, " ")}), nil
}

// copyPlace is where a copy goes. id and name are what the write is
// given, both empty when the copy goes beside an original that is in no
// folder this account can see; target is the folder the copy's reach is
// worked out from.
type copyPlace struct {
	id, name string
	target   *gdrive.File
}

// copyPlace resolves where a copy goes: the folder named, once a copy
// of that name already there is refused, or the folder the original is
// in. Beside an original in no folder this account can see, the copy
// goes to My Drive's root, which is where Drive puts a copy given no
// folder. A folder that cannot be read stands in by its id, which leaves
// who the copy would reach unknown.
func (s *Service) copyPlace(ctx context.Context, f *gdrive.File, to, name string, allowDuplicate bool) (copyPlace, error) {
	if strings.TrimSpace(to) != "" {
		parent, err := s.parentFolder(ctx, to)
		if err != nil {
			return copyPlace{}, err
		}
		if err := s.refuseDuplicate(ctx, parent, name, allowDuplicate); err != nil {
			return copyPlace{}, err
		}
		return copyPlace{id: parent.ID, name: parent.Name, target: parent}, nil
	}
	ref := f.Parent()
	if ref == "" {
		ref = RootAlias
	}
	target := &gdrive.File{ID: ref, Name: "the folder it is in", MimeType: gdrive.MimeFolder}
	if res, err := s.Resolve(ctx, ref, ResolveOptions{FollowShortcut: false}); err == nil {
		target = res.File
	}
	return copyPlace{id: f.Parent(), target: target}, nil
}

// predictCopy works out who a copy would reach that cannot reach the
// original, for each part of the original a copy can reach further
// than: the original itself, and among inside, the items of a folder
// being copied, each folder with limited access, which does not take on
// the reach of the folder around it and so may reach fewer people. Every
// other item reaches at least who the folder around it does, so a copy
// that widens nothing for those parts widens nothing anywhere.
func (s *Service) predictCopy(ctx context.Context, res *Resolved, inside []*gdrive.File, target *gdrive.File) []*movePlan {
	parts := []*movePlan{{res: res, target: target, copy: true}}
	for _, f := range inside {
		if f.IsFolder() && f.InheritedPermissionsDisabled {
			parts = append(parts, &movePlan{res: &Resolved{File: f}, target: target, copy: true})
		}
	}
	dest := s.reachOf(ctx, target)
	for _, p := range parts {
		s.predictMove(ctx, p, dest)
	}
	return parts
}

// copyRefused is GDRIVE_SHARING=off's refusal of a copy any part of
// which would reach more people than the original does, or nil.
func (s *Service) copyRefused(parts []*movePlan) error {
	var why []string
	for i, p := range parts {
		w := s.sharingOffRefuses(p)
		switch {
		case w == "":
		case i == 0:
			why = append(why, w)
		default:
			why = append(why, fmt.Sprintf("Inside it, %s has limited access, and the copy of it would reach "+
				"more people than it does: %s.", p.res.File.Name, copyReach(p)))
		}
	}
	if len(why) == 0 {
		return nil
	}
	if !strings.HasPrefix(why[0], "this server") {
		why[0] = "this server was started with GDRIVE_SHARING=off. " + why[0]
	}
	return Errorf(ClassForbidden, "%s Nothing was copied. A copy into a folder that nobody new can reach is "+
		"still allowed.", strings.Join(why, " "))
}

// sentence starts s with a capital letter, for a note that follows
// another sentence.
func sentence(s string) string {
	if s == "" {
		return ""
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// copyReach is who one part of a copy would newly reach.
func copyReach(p *movePlan) string {
	if p.unread != "" {
		return "who can reach " + p.unread + " could not be read"
	}
	return render.Reach(p.gained)
}

// askCopy puts a copy that would reach more people than the original
// to the person, once, naming each part that would.
func (s *Service) askCopy(ctx context.Context, target *gdrive.File, parts []*movePlan) error {
	var widening []render.MoveItem
	for _, p := range parts {
		if p.widens() {
			widening = append(widening, p.question())
		}
	}
	if len(widening) == 0 {
		return nil
	}
	return ask(ctx, render.AskCopy(s.moveTarget(ctx, target), parts[0].question(), widening))
}

// copyResult reports a copy, or what one would do, with who can reach
// the original and who can reach the copy: as Drive reports it once the
// copy is made, or as worked out beforehand on a dry run.
func (s *Service) copyResult(ctx context.Context, res *Resolved, before, after model.Sharing, out outcome) *Result {
	reach := fmt.Sprintf("Who can reach the original: %s. Who can reach the copy: %s.", before.Summary(), after.Summary())
	if out.DryRun {
		reach = fmt.Sprintf("Who can reach the original: %s. Who would reach the copy: %s.", before.Summary(), after.Summary())
	}
	out.Note = strings.TrimSpace(out.Note + " " + reach)
	result := s.report(ctx, res, out)
	result.JSON.SharingBefore = before.Summary()
	result.JSON.SharingAfter = after.Summary()
	return result
}

// CreateShortcutInput describes a shortcut to create.
type CreateShortcutInput struct {
	Target         string
	Parent         string
	Name           string
	AllowDuplicate bool
}

// CreateShortcut puts a pointer to one item in another folder. It is
// what Drive offers instead of a second parent.
func (s *Service) CreateShortcut(ctx context.Context, in CreateShortcutInput) (*Result, error) {
	if err := s.writable("create_shortcut"); err != nil {
		return nil, err
	}
	res, err := s.Resolve(ctx, in.Target, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return nil, err
	}
	target := res.File
	parent, err := s.parentFolder(ctx, in.Parent)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = target.Name
	}
	if err := s.refuseDuplicate(ctx, parent, name, in.AllowDuplicate); err != nil {
		return nil, err
	}
	meta := &gdrive.FileMeta{
		Name: name, MimeType: gdrive.MimeShortcut, Parents: []string{parent.ID},
		ShortcutDetails: &gdrive.ShortcutDetails{TargetID: target.ID},
	}
	if err := s.assignID(ctx, meta); err != nil {
		return nil, err
	}
	f, err := s.api.CreateFile(ctx, meta, gapi.WriteOptions{ResourceIDs: []string{target.ID, parent.ID}})
	if err != nil {
		return nil, wrap(err, fmt.Sprintf("creating a shortcut to %s in %s", target.Name, parent.Name))
	}
	return s.write(ctx, f, outcome{Action: render.ActionCreated,
		Note: fmt.Sprintf("it points at %s (%s). Organizing, sharing and trashing "+
			"act on the shortcut, not on what it points at.", target.Name, target.ID)})
}

// TrashInput selects an item to trash or restore.
type TrashInput struct {
	File   string
	DryRun bool
}

// TrashFile moves an item to the trash, where it can be brought back.
// Permanent deletion is a separate, gated tool.
func (s *Service) TrashFile(ctx context.Context, in TrashInput) (*Result, error) {
	return s.setTrashed(ctx, in, true)
}

// RestoreFile takes an item out of the trash and says where it landed,
// because a restore puts it back where it was, which may not be where
// the person is looking.
func (s *Service) RestoreFile(ctx context.Context, in TrashInput) (*Result, error) {
	return s.setTrashed(ctx, in, false)
}

func (s *Service) setTrashed(ctx context.Context, in TrashInput, trashed bool) (*Result, error) {
	tool := "restore_file"
	if trashed {
		tool = "trash_file"
	}
	if err := s.writable(tool); err != nil {
		return nil, err
	}
	// A shortcut is trashed as itself; following it would destroy the
	// wrong thing.
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: false, Fresh: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	if f.Trashed == trashed {
		state := "already in the trash"
		if !trashed {
			state = "not in the trash"
		}
		return s.report(ctx, res, outcome{Action: render.ActionUnchanged,
			Note: fmt.Sprintf("%s is %s.", f.Name, state)}), nil
	}
	if err := s.trashable(f, trashed); err != nil {
		return nil, err
	}

	what := describeTrashSubject(f)
	action := render.ActionTrashed
	if !trashed {
		action = render.ActionRestored
	}
	if in.DryRun {
		verb := "would go to the trash"
		if !trashed {
			verb = "would come out of the trash"
		}
		return s.report(ctx, res, outcome{Action: action, DryRun: true,
			Note: fmt.Sprintf("%s %s.", what, verb)}), nil
	}

	updated, err := s.api.UpdateFile(ctx, f.ID, &gdrive.FileMeta{Trashed: gdrive.Bool(trashed)}, gapi.UpdateOptions{})
	if err != nil {
		return nil, s.trashError(err, f, trashed)
	}
	note := what + " is in the trash. restore_file brings it back, and Drive empties the " +
		"trash 30 days after an item goes in."
	if !trashed {
		note = what + " is back in " + s.Location(ctx, updated).String() + "."
	}
	// Trashing takes an item out of every listing that led to it.
	return s.write(ctx, updated, outcome{Action: action, Note: note, Moved: true})
}

// describeTrashSubject names what is about to move, saying when a folder
// takes its contents with it.
func describeTrashSubject(f *gdrive.File) string {
	if f.IsFolder() {
		return f.Name + " (a folder, with everything inside it)"
	}
	return f.Name
}

// trashable checks Drive's own answer before asking, so the refusal
// explains itself.
func (s *Service) trashable(f *gdrive.File, trashed bool) error {
	if f.Capabilities == nil {
		return nil
	}
	if trashed && !f.Capabilities.CanTrash {
		return Errorf(ClassForbidden, "this account cannot trash %s. In My Drive only the owner can; "+
			"in a shared drive it depends on your role. get_file shows what you can do with it.", f.Name)
	}
	if !trashed && !f.Capabilities.CanUntrash {
		return Errorf(ClassForbidden, "this account cannot restore %s", f.Name)
	}
	return nil
}

// trashError explains Drive's refusals of a trash in the terms that
// caused them.
func (s *Service) trashError(err error, f *gdrive.File, trashed bool) error {
	if gapi.IsNotOwner(err) && trashed {
		return &Error{Class: ClassForbidden, Message: fmt.Sprintf(
			"only the owner can trash %s, and this account is not the owner. "+
				"A file shared with you goes away by removing your own access, not by trashing it.", f.Name), Err: err}
	}
	doing := "trashing"
	if !trashed {
		doing = "restoring"
	}
	return wrap(err, doing+" "+f.Name)
}

// hexColor is the form Drive takes a folder color in.
var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// folderColor validates a color. Drive publishes a palette and snaps
// anything else to the nearest entry in it, which is worth saying rather
// than pretending the exact value was kept.
func folderColor(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if !strings.HasPrefix(v, "#") {
		v = "#" + v
	}
	if !hexColor.MatchString(v) {
		return "", Errorf(ClassInvalid, "color %q is not an RGB hex value like #4986e7", v)
	}
	return strings.ToLower(v), nil
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// orNever names a timestamp that was never set, so a before-and-after
// line does not read as though a field went from nothing to something
// without saying what nothing meant.
func orNever(ts string) string {
	if strings.TrimSpace(ts) == "" {
		return "never"
	}
	return ts
}
