package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmedum/google-drive-mcp/v2/internal/gapi"
	"github.com/mmedum/google-drive-mcp/v2/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v2/internal/model"
	"github.com/mmedum/google-drive-mcp/v2/internal/render"
)

// MaxMoveFiles is how many items one move_file call takes. Every one is
// read before anything moves, and a question naming the ones that
// widen access has to stay readable.
const MaxMoveFiles = 50

// MoveFileInput moves one item, or several, to another folder or shared
// drive.
type MoveFileInput struct {
	File string
	// Files moves several items to the one destination: at most
	// MaxMoveFiles, and not together with File.
	Files []string
	To    string
	// DryRun reports what would happen, who could reach each item before
	// and after included, and changes nothing.
	DryRun bool
}

// MoveFile changes which folder an item is in. Drive allows one parent,
// so a move is a swap, and a My Drive folder cannot move into a shared
// drive at all: the result says so and what to do instead.
//
// A move also changes who can reach the item. The sharing guide says a
// move "re-evaluates and applies the new parent's permissions to the item
// and its children", and the Workspace help that what it inherited from
// the old folder goes while grants made on it directly stay. So the
// result shows who could reach it before and who can after, and a move
// that would reach more people is put to the person first (§4a).
func (s *Service) MoveFile(ctx context.Context, in MoveFileInput) (*Result, error) {
	if err := s.writable("move_file"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.To) == "" {
		return nil, Errorf(ClassInvalid, "to is required: name the folder, root, or a shared drive to move into")
	}
	switch {
	case len(in.Files) > 0 && strings.TrimSpace(in.File) != "":
		return nil, Errorf(ClassInvalid, "pass file for one item or files for several, not both")
	case len(in.Files) > 0:
		return s.moveMany(ctx, in)
	case strings.TrimSpace(in.File) == "":
		return nil, Errorf(ClassInvalid, "file is required: name the item to move, or pass files for several")
	}
	// A shortcut is the thing sitting in the folder, so a move acts on
	// it rather than on what it points at.
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: false, Fresh: true})
	if err != nil {
		return nil, err
	}
	target, err := s.parentFolder(ctx, in.To)
	if err != nil {
		return nil, err
	}
	p, err := s.planMove(ctx, res, target)
	if err != nil {
		return nil, err
	}
	if p.here {
		return s.report(ctx, res, outcome{Action: render.ActionUnchanged,
			Note: "it is already in " + target.Name + "."}), nil
	}
	s.predictMove(ctx, p, s.reachOf(ctx, target))
	if in.DryRun {
		return s.moveResult(ctx, res, p, p.after, true), nil
	}
	if p.widens() {
		if err := ask(ctx, render.AskMove(s.moveTarget(ctx, target), []string{p.res.File.ID},
			[]render.MoveItem{p.question()})); err != nil {
			return nil, err
		}
	}

	f := res.File
	moved, err := s.api.UpdateFile(ctx, f.ID, &gdrive.FileMeta{}, gapi.UpdateOptions{
		WriteOptions:  gapi.WriteOptions{ResourceIDs: []string{target.ID}},
		AddParents:    target.ID,
		RemoveParents: p.from,
	})
	if err != nil {
		return nil, wrap(err, fmt.Sprintf("moving %s to %s", f.Name, target.Name))
	}
	s.forget(moved, true)
	return s.moveResult(ctx, &Resolved{File: moved}, p, s.sharingNow(ctx, moved), false), nil
}

// moveMany moves several items to one destination. Each is read and
// checked first, and gets its own outcome: refused, with the reason,
// when a check stops it, and failed, with Drive's answer, when the move
// does. A failure does not undo the moves before it. The person is asked
// once, before anything moves, about every item that would reach more
// people. §18 has why a move may be bulk when removal and sharing are
// not.
func (s *Service) moveMany(ctx context.Context, in MoveFileInput) (*Result, error) {
	if len(in.Files) > MaxMoveFiles {
		return nil, Errorf(ClassInvalid, "files holds %d items, and one call moves at most %d. Split them, or "+
			"move the folder they are in.", len(in.Files), MaxMoveFiles)
	}
	for i, ref := range in.Files {
		if strings.TrimSpace(ref) == "" {
			return nil, Errorf(ClassInvalid, "files[%d] is empty: every entry names an item to move", i)
		}
	}
	target, err := s.parentFolder(ctx, in.To)
	if err != nil {
		return nil, err
	}
	items := make([]*movedItem, len(in.Files))
	seen := map[string]bool{}
	var dest *reach
	for i, ref := range in.Files {
		it := &movedItem{json: render.MovedJSON{File: ref}}
		items[i] = it
		res, err := s.Resolve(ctx, ref, ResolveOptions{FollowShortcut: false, Fresh: true})
		if err != nil {
			it.set(render.MovedRefused, err.Error())
			continue
		}
		it.json.ID, it.json.Name = res.File.ID, res.File.Name
		if seen[res.File.ID] {
			it.set(render.MovedRefused, "it is listed more than once, and moves once, as the first")
			continue
		}
		seen[res.File.ID] = true
		p, err := s.planMove(ctx, res, target)
		if err != nil {
			it.set(render.MovedRefused, err.Error())
			continue
		}
		if p.here {
			it.set(render.MovedUnchanged, "it is already in "+target.Name+".")
			continue
		}
		if dest == nil {
			r := s.reachOf(ctx, target)
			dest = &r
		}
		s.predictMove(ctx, p, *dest)
		it.plan = p
		it.json.From, it.json.To, it.json.Widens = p.oldPath, p.newPath, p.widens()
		it.json.SharingBefore, it.json.SharingAfter = p.before.Summary(), p.after.Summary()
	}

	if in.DryRun {
		for _, it := range items {
			if it.plan != nil {
				it.set(render.MovedWouldMove, s.moveNote(ctx, it.plan, it.plan.after, true))
			}
		}
		return s.manyResult(ctx, target, items, true), nil
	}
	var moving []string
	var widening []render.MoveItem
	for _, it := range items {
		if it.plan == nil {
			continue
		}
		moving = append(moving, it.plan.res.File.ID)
		if it.plan.widens() {
			widening = append(widening, it.plan.question())
		}
	}
	if len(widening) > 0 {
		if err := ask(ctx, render.AskMove(s.moveTarget(ctx, target), moving, widening)); err != nil {
			return nil, err
		}
	}
	for _, it := range items {
		if it.plan != nil {
			s.moveOne(ctx, it)
		}
	}
	return s.manyResult(ctx, target, items, false), nil
}

// movedItem is one item of a move of several: its plan, when it got
// that far, and what came of it.
type movedItem struct {
	plan *movePlan
	json render.MovedJSON
}

func (it *movedItem) set(outcome, reason string) {
	it.json.Outcome, it.json.Reason = outcome, reason
}

// moveOne makes one move of several. A failure is read back, because
// an error does not always mean nothing happened: an item found in the
// destination is reported as moved, and one that is not says where it
// is.
func (s *Service) moveOne(ctx context.Context, it *movedItem) {
	p := it.plan
	f, target := p.res.File, p.target
	moved, err := s.api.UpdateFile(ctx, f.ID, &gdrive.FileMeta{}, gapi.UpdateOptions{
		WriteOptions:  gapi.WriteOptions{ResourceIDs: []string{target.ID}},
		AddParents:    target.ID,
		RemoveParents: p.from,
	})
	late := ""
	if err != nil {
		failed := wrap(err, fmt.Sprintf("moving %s to %s", f.Name, target.Name)).Error()
		// Until the read-back says otherwise, it is not where it was going.
		it.json.To, it.json.SharingAfter = "", ""
		now, rerr := s.Resolve(ctx, f.ID, ResolveOptions{FollowShortcut: false, Fresh: true})
		switch {
		case rerr != nil:
			it.set(render.MovedFailed, failed+" Where it is now could not be read back.")
			return
		case now.File.Parent() != target.ID:
			it.set(render.MovedFailed, failed+" It is in "+s.Location(ctx, now.File).String()+".")
			return
		}
		moved, it.json.To = now.File, p.newPath
		late = "Drive answered with an error, and it is in the destination all the same. The error: " + failed
	}
	s.forget(moved, true)
	after := s.sharingNow(ctx, moved)
	it.json.SharingAfter = after.Summary()
	it.set(render.MovedMoved, strings.TrimSpace(late+" "+s.moveNote(ctx, p, after, false)))
}

// manyResult reports a move of several items.
func (s *Service) manyResult(ctx context.Context, target *gdrive.File, items []*movedItem, dryRun bool) *Result {
	out := make([]render.MovedJSON, 0, len(items))
	moved, failed := 0, 0
	for _, it := range items {
		out = append(out, it.json)
		switch it.json.Outcome {
		case render.MovedMoved, render.MovedWouldMove:
			moved++
		case render.MovedFailed:
			failed++
		}
	}
	note := ""
	if failed > 0 && moved > 0 {
		note = "each item moved or failed on its own: the ones that moved stay moved."
	}
	action := render.ActionMoved
	if moved == 0 {
		action = render.ActionUnchanged
	}
	text := render.MoveMany(s.locationOf(ctx, target), out, dryRun, note)
	return &Result{Text: text, JSON: &render.WriteJSON{Summary: text, Action: string(action), Note: note,
		DryRun: dryRun, Items: out}}
}

// movePlan is one move as it will be made: where the item goes, and who
// could reach it before and would after.
type movePlan struct {
	res    *Resolved
	target *gdrive.File
	// from is the folder it leaves. here says it is already in target,
	// and nothing else is filled in.
	from string
	here bool

	oldPath, newPath string
	// before is who can reach it now, read from Drive; after is who would
	// reach it once moved, worked out from the destination before the
	// move; gained is what after reaches that before does not.
	before, after model.Sharing
	gained        []model.Grant
	// unread names what could not be read, "it" or "the destination",
	// which leaves after unknown.
	unread string
	// self is the signed-in account's address, read when first needed.
	self    string
	selfSet bool
}

// widens reports whether the move would let more people reach the item,
// or cannot tell: a question too many rather than one too few.
func (p *movePlan) widens() bool { return p.unread != "" || len(p.gained) > 0 }

// question is the item as a question names it.
func (p *movePlan) question() render.MoveItem {
	f := p.res.File
	kind := "file"
	switch {
	case f.IsFolder():
		kind = "folder"
	case f.IsShortcut():
		kind = "shortcut"
	}
	return render.MoveItem{ID: f.ID, Name: f.Name, Kind: kind, Gained: p.gained, Unread: p.unread}
}

// planMove refuses a move Drive would refuse, before anything is read
// about who can reach the item.
func (s *Service) planMove(ctx context.Context, res *Resolved, target *gdrive.File) (*movePlan, error) {
	f := res.File
	if f.ID == target.ID {
		return nil, Errorf(ClassInvalid, "%s cannot be moved into itself", f.Name)
	}
	p := &movePlan{res: res, target: target, from: f.Parent()}
	if p.from == target.ID {
		p.here = true
		return p, nil
	}
	if f.IsFolder() && f.DriveID == "" && target.DriveID != "" {
		return nil, Errorf(ClassUnsupported, "Drive does not move a My Drive folder into a shared drive, "+
			"because the shared drive would have to take ownership of everything inside it. "+
			"Create the folder in the shared drive with create_folder, then move the files into it.")
	}
	if err := s.movable(ctx, f, target); err != nil {
		return nil, err
	}
	p.oldPath = s.Location(ctx, f).String()
	p.newPath = s.locationOf(ctx, target)
	return p, nil
}

// reach is a permission list and whether it could be read.
type reach struct {
	perms []*gdrive.Permission
	known bool
}

// reachOf reads who can reach a folder, which is what an item moved into
// it inherits.
func (s *Service) reachOf(ctx context.Context, folder *gdrive.File) reach {
	perms, err := s.api.ListPermissions(ctx, folder.ID)
	if err != nil {
		s.log.DebugContext(ctx, "permissions unavailable", "file", gapi.ShortID(folder.ID), "class", gapi.Class(err))
		return reach{}
	}
	return reach{perms: perms, known: true}
}

// predictMove fills in who can reach the item now and who would once it
// is in the destination. A list that cannot be read leaves after
// unknown, and the move then counts as one that may widen.
func (s *Service) predictMove(ctx context.Context, p *movePlan, dest reach) {
	f := p.res.File
	item := s.reachOf(ctx, f)
	p.before = model.NewSharing(f.Shared, nil, false)
	if item.known {
		p.before = s.sharingFrom(ctx, f, item.perms, p.res.DriveName)
	}
	switch {
	case !item.known:
		p.unread = "it"
	case !dest.known:
		p.unread = "the destination"
	}
	if p.unread != "" {
		p.after = model.NewSharing(false, nil, false)
		return
	}
	grants := movedGrants(f, p.target, item.perms, dest.perms)
	shared := false
	for _, g := range grants {
		shared = shared || g.Role != model.RoleOwner
	}
	p.after = model.SharingOf(shared, grants)
	if p.target.DriveID != "" {
		p.after.SharedDrive = s.Location(ctx, p.target).Drive
	}
	p.gained = s.gained(ctx, p, p.before, p.after)
}

// gained is what after reaches that before does not, leaving out the
// signed-in account, whose address is read only when a person is among
// it.
func (s *Service) gained(ctx context.Context, p *movePlan, before, after model.Sharing) []model.Grant {
	out := model.Gained(before, after, p.self)
	if p.selfSet {
		return out
	}
	for _, g := range out {
		if g.Type == principalUser {
			return model.Gained(before, after, s.selfOf(ctx, p))
		}
	}
	return out
}

// differs reports whether Drive's answer after the move reaches other
// people, or reaches them differently, than the plan worked out,
// leaving out the signed-in account.
func (s *Service) differs(ctx context.Context, p *movePlan, after model.Sharing) bool {
	if model.SameReach(p.after, after, p.self) {
		return false
	}
	return p.selfSet || !model.SameReach(p.after, after, s.selfOf(ctx, p))
}

// selfOf reads the plan's account address once.
func (s *Service) selfOf(ctx context.Context, p *movePlan) string {
	if !p.selfSet {
		p.self, p.selfSet = s.selfAddress(ctx, p.res.File, p.target), true
	}
	return p.self
}

// selfAddress is the signed-in account's address: read off a file it
// owns when one is at hand, and asked of Drive otherwise. Empty when
// neither answers, which leaves the account counted like anyone else.
func (s *Service) selfAddress(ctx context.Context, files ...*gdrive.File) string {
	for _, f := range files {
		for _, o := range f.Owners {
			if o != nil && o.Me && o.EmailAddress != "" {
				return o.EmailAddress
			}
		}
	}
	about, err := s.api.About(ctx)
	if err != nil || about.User == nil {
		return ""
	}
	return about.User.EmailAddress
}

// movedGrants works out who can reach an item once it sits in target,
// from what Google documents about a move: grants made on the item
// directly stay, what it inherited where it was goes, and it inherits
// everything the destination has. A My Drive item moved into a shared
// drive loses its owner, because the drive owns it. An owner of the
// destination who does not own the item is counted as an editor: what
// Drive gives them is not documented (§18), and counting them at all
// errs toward showing more exposure, not less.
func movedGrants(f, target *gdrive.File, item, dest []*gdrive.Permission) []model.Grant {
	intoDrive := f.DriveID == "" && target.DriveID != ""
	var out []model.Grant
	at := map[string]int{}
	put := func(g model.Grant) {
		i, ok := at[g.Key()]
		if !ok {
			at[g.Key()] = len(out)
			out = append(out, g)
			return
		}
		e := &out[i]
		if model.RoleWidens(e.Role, g.Role) {
			e.Role = g.Role
		}
		e.Discoverable = e.Discoverable || g.Discoverable
		if g.Inherited() {
			e.InheritedFrom = g.InheritedFrom
		}
	}
	for _, p := range item {
		role, direct := model.DirectRole(p)
		if !direct || intoDrive && role == model.RoleOwner {
			continue
		}
		g := model.GrantOf(p)
		g.Role, g.InheritedFrom = role, ""
		put(g)
	}
	for _, p := range dest {
		g := model.GrantOf(p)
		g.PermissionID, g.InheritedFrom, g.PendingOwner = "", target.Name, false
		if g.Role == model.RoleOwner {
			if i, ok := at[g.Key()]; ok && out[i].Role == model.RoleOwner {
				continue
			}
			g.Role = model.RoleWriter
		}
		put(g)
	}
	return out
}

// moveTarget is the destination as a question names it.
func (s *Service) moveTarget(ctx context.Context, target *gdrive.File) render.MoveTarget {
	t := render.MoveTarget{ID: target.ID, Name: target.Name, Kind: "folder"}
	switch {
	case target.DriveID != "" && target.ID == target.DriveID:
		t.Kind = "shared drive"
	case s.isDriveRoot(ctx, target):
		t.Kind = "My Drive"
	}
	return t
}

// moveResult reports a move, or what one would do: the location before
// and after, and who could reach the item before and can after. after is
// what Drive answered once the move was made, or the prediction on a dry
// run.
func (s *Service) moveResult(ctx context.Context, res *Resolved, p *movePlan, after model.Sharing, dryRun bool) *Result {
	changes := make([]render.Change, 0, 2)
	changes = append(changes, render.Change{Field: "location", From: p.oldPath, To: p.newPath})
	changes = append(changes, render.Exposure(p.before, after)...)
	out := outcome{Action: render.ActionMoved, DryRun: dryRun, Changes: changes, Note: s.moveNote(ctx, p, after, dryRun)}
	result := s.report(ctx, res, out)
	result.JSON.SharingBefore = p.before.Summary()
	result.JSON.SharingAfter = after.Summary()
	return result
}

// moveNote says who the move lets reach the item that could not before,
// and, after a real move, whether Drive's answer is what was worked out
// beforehand.
func (s *Service) moveNote(ctx context.Context, p *movePlan, after model.Sharing, dryRun bool) string {
	var parts []string
	switch {
	case dryRun && p.unread != "":
		parts = append(parts, "who can reach "+p.unread+" could not be read, so whether the move would let more "+
			"people reach it is unknown. A real move puts it to the person first when the client can ask")
	case dryRun && len(p.gained) > 0:
		parts = append(parts, "the move would let more people reach it, or give them more access: "+
			render.Reach(p.gained)+". A real move puts that to the person first when the client can ask")
	case dryRun, p.before.Unknown, after.Unknown:
	default:
		if gained := s.gained(ctx, p, p.before, after); len(gained) > 0 {
			parts = append(parts, "more people can reach it now, or have more access: "+render.Reach(gained))
		}
		if !p.after.Unknown && s.differs(ctx, p, after) {
			parts = append(parts, "that is not who this server worked out would reach it, which was: "+
				p.after.Summary()+". Drive can take a moment to apply a move's sharing; list_permissions "+
				"shows where it stands")
		}
	}
	return joinSentences(parts)
}

// movable refuses a move the destination will not accept, before it is
// attempted, so the reason names the folder rather than repeating
// Google's generic refusal.
func (s *Service) movable(ctx context.Context, f, target *gdrive.File) error {
	if target.Capabilities != nil && !target.Capabilities.CanAddChildren {
		return Errorf(ClassForbidden, "this account cannot put anything into %s", target.Name)
	}
	if f.Capabilities == nil {
		return nil
	}
	leavingDrive := f.DriveID != target.DriveID
	switch {
	case leavingDrive && !f.Capabilities.CanMoveItemOutOfDrive:
		return Errorf(ClassForbidden, "this account cannot move %s out of %s", f.Name, s.Location(ctx, f).Drive)
	case !leavingDrive && !f.Capabilities.CanMoveItemWithinDrive:
		return Errorf(ClassForbidden, "this account cannot move %s", f.Name)
	}
	return nil
}

// locationOf renders where a folder's contents live, as a destination
// path.
func (s *Service) locationOf(ctx context.Context, folder *gdrive.File) string {
	return s.folderLocation(ctx, folder, s.Location(ctx, folder)).String()
}
