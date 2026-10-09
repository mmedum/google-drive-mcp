package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

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
	Properties                   map[string]string
	CopyRequiresWriterPermission *bool
	WritersCanShare              *bool
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

	meta, changes, err := metaPatch(f, in, color, s.now())
	if err != nil {
		return nil, err
	}
	// The two sharing switches loosen a file the way a shared drive's
	// restrictions do, and GDRIVE_SHARING=off refuses them the same way.
	loosens := meta.CopyRequiresWriterPermission != nil && !*meta.CopyRequiresWriterPermission ||
		meta.WritersCanShare != nil && *meta.WritersCanShare
	if loosens && s.opts.Sharing == config.SharingOff {
		return nil, Errorf(ClassForbidden, "this server was started with GDRIVE_SHARING=off, and letting viewers copy "+
			"%s or editors reshare it would widen who can reach or pass on what is in it. Nothing was changed. "+
			"Tightening either is still allowed.", f.Name)
	}

	if len(changes) == 0 {
		return s.report(ctx, res, outcome{Action: render.ActionUnchanged,
			Note: "nothing to do: every field passed already had that value."}), nil
	}

	updated, err := s.api.UpdateFile(ctx, f.ID, meta, gapi.UpdateOptions{})
	if err != nil {
		return nil, wrap(err, "updating "+f.Name)
	}
	// A rename makes every path that led here wrong.
	return s.write(ctx, updated, outcome{Action: render.ActionUpdated, Changes: changes, Moved: meta.Name != ""})
}

// metaPatch works out the smallest patch that turns f into what the
// caller asked for, and the before-and-after list that goes with it. A
// field whose value is already what was asked for is left out of both:
// Drive would accept it, and the result would claim a change that did
// not happen.
func metaPatch(f *gdrive.File, in UpdateFileInput, color string, now time.Time) (*gdrive.FileMeta, []render.Change, error) {
	meta := &gdrive.FileMeta{}
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
	}
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
		return nil, nil, err
	}
	return meta, append(changes, propertyChanges...), nil
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
	parentID := f.Parent()
	parentName := ""
	if strings.TrimSpace(in.To) != "" {
		parent, err := s.parentFolder(ctx, in.To)
		if err != nil {
			return nil, err
		}
		parentID, parentName = parent.ID, parent.Name
		if err := s.refuseDuplicate(ctx, parent, name, in.AllowDuplicate); err != nil {
			return nil, err
		}
	}

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
	if in.DryRun {
		where := parentName
		if where == "" {
			where = "the folder it is in now"
		}
		note := fmt.Sprintf("Would copy it as %s into %s", name, where)
		if convert != "" {
			note += ", imported as " + model.KindName(convert)
		}
		return s.report(ctx, res, outcome{Action: render.ActionCopied, DryRun: true,
			Note: note + ". Nothing was copied."}), nil
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
	notes := []string{}
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
	return s.write(ctx, copied, outcome{Action: render.ActionCopied, Note: strings.Join(notes, " ")})
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
