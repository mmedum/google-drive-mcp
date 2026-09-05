package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/render"
)

// ListDrivesInput tunes a shared-drive listing.
type ListDrivesInput struct {
	// Name narrows the list to drives whose name contains it, using
	// Drive's own drives.list query.
	Name string
	// IncludeHidden keeps the drives that are out of the sidebar's
	// default view. They are left out by default because a listing
	// should show what a person sees.
	IncludeHidden bool
}

// ListDrives shows the shared drives this account can see, with what it
// may do in each and what restrictions are in force.
func (s *Service) ListDrives(ctx context.Context, in ListDrivesInput) (string, error) {
	all, err := s.Drives(ctx)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(in.Name)
	drives := make([]*model.Drive, 0, len(all))
	hidden := 0
	for _, d := range all {
		if name != "" && !strings.Contains(strings.ToLower(d.Name), strings.ToLower(name)) {
			continue
		}
		if d.Hidden {
			hidden++
			if !in.IncludeHidden {
				continue
			}
		}
		drives = append(drives, model.NewDrive(d))
	}
	sort.Slice(drives, func(i, j int) bool { return strings.ToLower(drives[i].Name) < strings.ToLower(drives[j].Name) })

	o := render.DrivesOptions{
		Title:       fmt.Sprintf("shared drives this account can see: %s", model.Plural(len(drives), "drive", "drives")),
		HiddenShown: in.IncludeHidden && hidden > 0,
		Empty: "no shared drives. They are a Google Workspace feature; get_account says whether this " +
			"account has them at all.",
	}
	if name != "" {
		o.Title = fmt.Sprintf("shared drives whose name contains %q: %s", name, model.Plural(len(drives), "drive", "drives"))
	}
	if hidden > 0 && !in.IncludeHidden {
		o.Note = fmt.Sprintf("%s hidden from the sidebar and left out; pass include_hidden: true to see %s",
			model.Plural(hidden, "drive is", "drives are"), oneOrThem(hidden))
	}
	return render.Drives(drives, o), nil
}

func oneOrThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// Drive actions manage_drive accepts.
const (
	DriveCreate   = "create"
	DriveRename   = "rename"
	DriveHide     = "hide"
	DriveUnhide   = "unhide"
	DriveRestrict = "restrict"
)

// DriveActions lists what manage_drive accepts, for tool descriptions
// and error messages.
func DriveActions() []string {
	return []string{DriveCreate, DriveHide, DriveRename, DriveRestrict, DriveUnhide}
}

// ManageDriveInput is one change to a shared drive.
type ManageDriveInput struct {
	Action string
	// Drive is the drive to act on, by name or id. Not used by create.
	Drive string
	// Name is the new drive's name, or the new name for a rename.
	Name string
	// Restrictions are the switches to set, by the names in
	// model.DriveRestrictionNames.
	Restrictions map[string]bool
	DryRun       bool
}

// ManageDrive creates a shared drive, renames it, hides it or changes
// its restrictions. Membership is not here: a shared drive's members are
// permissions on the drive, so share_file and unshare_file do that with
// the drive as the target, and one place decides who may see what.
func (s *Service) ManageDrive(ctx context.Context, in ManageDriveInput) (*Result, error) {
	if err := s.writable("manage_drive"); err != nil {
		return nil, err
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	switch action {
	case DriveCreate:
		return s.createDrive(ctx, in)
	case DriveRename, DriveHide, DriveUnhide, DriveRestrict:
		return s.changeDrive(ctx, action, in)
	case "":
		return nil, Errorf(ClassInvalid, "action is required: one of %s", strings.Join(DriveActions(), ", "))
	}
	return nil, Errorf(ClassInvalid, "action %q is not one of %s", in.Action, strings.Join(DriveActions(), ", "))
}

// createDrive makes a shared drive. The requestId is what makes it
// idempotent: Drive collapses a repeat carrying the same one into the
// first, so an ambiguous failure cannot leave two drives named the same.
func (s *Service) createDrive(ctx context.Context, in ManageDriveInput) (*Result, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, Errorf(ClassInvalid, "name is required to create a shared drive")
	}
	if strings.TrimSpace(in.Drive) != "" {
		return nil, Errorf(ClassInvalid, "create makes a new shared drive, so it takes name, not drive. "+
			"To change the one named, use rename, hide, unhide or restrict.")
	}
	about, err := s.api.About(ctx)
	if err == nil && !about.CanCreateDrives {
		return nil, Errorf(ClassForbidden, "this account cannot create shared drives. They are a Google "+
			"Workspace feature, and a consumer account has none; an administrator can also turn creation off.")
	}
	if existing, err := s.drivesList(ctx); err == nil {
		for _, d := range existing {
			if strings.EqualFold(d.Name, name) {
				return nil, Errorf(ClassExists, "a shared drive named %q is already here (%s). Drive allows a "+
					"second one of that name, and nothing afterwards could tell you which you meant.", d.Name, d.ID)
			}
		}
	}

	meta := &gdrive.DriveMeta{Name: name}
	restrictions, changes, err := driveRestrictionPatch(nil, in.Restrictions)
	if err != nil {
		return nil, err
	}
	meta.Restrictions = restrictions
	if in.DryRun {
		return driveResult(&model.Drive{Name: name}, outcome{Action: render.ActionCreated,
			DryRun: true, Changes: changes,
			Note: "a shared drive is owned by the organisation, not by you, and everything put in it belongs " +
				"to the organisation from then on"}), nil
	}

	requestID, err := requestID()
	if err != nil {
		return nil, err
	}
	created, err := s.api.CreateDrive(ctx, requestID, meta)
	if err != nil {
		return nil, wrap(err, fmt.Sprintf("creating the shared drive %q", name))
	}
	s.forgetDrives()
	return driveResult(model.NewDrive(created), outcome{Action: render.ActionCreated, Changes: changes,
		Note: "everything in a shared drive belongs to the organisation rather than to a person, and you are " +
			"its organizer. share_file with this drive as the target adds members."}), nil
}

// changeDrive applies a rename, a hide, an unhide or a restriction
// change to a drive that already exists.
func (s *Service) changeDrive(ctx context.Context, action string, in ManageDriveInput) (*Result, error) {
	if strings.TrimSpace(in.Drive) == "" {
		return nil, Errorf(ClassInvalid, "drive is required for %s: name the shared drive to change, by name or id", action)
	}
	d, err := s.findDrive(ctx, in.Drive)
	if err != nil {
		return nil, err
	}
	if err := driveChangeAllowed(d, action); err != nil {
		return nil, err
	}

	var changes []render.Change
	switch action {
	case DriveRename:
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return nil, Errorf(ClassInvalid, "name is required to rename a shared drive")
		}
		if name == d.Name {
			return driveResult(model.NewDrive(d), outcome{Action: render.ActionUnchanged,
				Note: "it is already called that."}), nil
		}
		changes = []render.Change{{Field: "name", From: d.Name, To: name}}
		return s.applyDriveChange(ctx, d, action, &gdrive.DriveMeta{Name: name}, changes, in.DryRun)
	case DriveHide, DriveUnhide:
		want := action == DriveHide
		if d.Hidden == want {
			return driveResult(model.NewDrive(d), outcome{Action: render.ActionUnchanged,
				Note: fmt.Sprintf("it is already %s.", hiddenWords(want))}), nil
		}
		changes = []render.Change{{Field: "hidden from the sidebar", From: yesNo(d.Hidden), To: yesNo(want)}}
		return s.applyDriveChange(ctx, d, action, nil, changes, in.DryRun)
	}

	restrictions, changes, err := driveRestrictionPatch(d.Restrictions, in.Restrictions)
	if err != nil {
		return nil, err
	}
	if len(changes) == 0 {
		return driveResult(model.NewDrive(d), outcome{Action: render.ActionUnchanged,
			Note: "every restriction passed already had that value."}), nil
	}
	return s.applyDriveChange(ctx, d, action, &gdrive.DriveMeta{Restrictions: restrictions}, changes, in.DryRun)
}

// applyDriveChange makes the call the action needs and renders what it
// did. Hiding has its own endpoint; the rest are patches.
func (s *Service) applyDriveChange(ctx context.Context, d *gdrive.Drive, action string,
	meta *gdrive.DriveMeta, changes []render.Change, dryRun bool,
) (*Result, error) {
	if dryRun {
		return driveResult(model.NewDrive(d), outcome{Action: driveAction(action), DryRun: true, Changes: changes}), nil
	}
	var updated *gdrive.Drive
	var err error
	switch action {
	case DriveHide:
		updated, err = s.api.HideDrive(ctx, d.ID)
	case DriveUnhide:
		updated, err = s.api.UnhideDrive(ctx, d.ID)
	default:
		updated, err = s.api.UpdateDrive(ctx, d.ID, meta)
	}
	if err != nil {
		return nil, wrap(err, fmt.Sprintf("%s on the shared drive %s", action, d.Name))
	}
	s.forgetDrives()
	return driveResult(model.NewDrive(updated), outcome{Action: driveAction(action), Changes: changes}), nil
}

// driveAction maps a manage_drive action onto the word a result reports,
// which is a closed set the schema also names.
func driveAction(action string) render.Action {
	if action == DriveCreate {
		return render.ActionCreated
	}
	return render.ActionUpdated
}

// driveChangeAllowed refuses before the call what Drive's own
// capabilities say will fail, so the reason names the drive rather than
// repeating a generic 403.
func driveChangeAllowed(d *gdrive.Drive, action string) error {
	c := d.Capabilities
	if c == nil {
		return nil
	}
	switch action {
	case DriveRename:
		if !c.CanRenameDrive {
			return Errorf(ClassForbidden, "this account cannot rename %s; that is an organizer's to do.", d.Name)
		}
	case DriveRestrict:
		if !c.CanChangeDriveMembersOnly && !c.CanChangeDomainUsersOnly && !c.CanChangeCopyRequiresWriter {
			return Errorf(ClassForbidden, "this account cannot change the restrictions on %s. When a drive is "+
				"administrator-managed, only an administrator can.", d.Name)
		}
	}
	// Hiding is a per-person sidebar setting, so every member may do it
	// and Drive publishes no capability for it.
	return nil
}

// driveRestrictionPatch turns the switches a caller passed into a
// restrictions body and the before-and-after list that goes with it. A
// switch already at the value asked for is left out of both: Drive would
// accept it, and the result would claim a change that did not happen.
func driveRestrictionPatch(current *gdrive.DriveRestrictions, want map[string]bool) (*gdrive.DriveRestrictions, []render.Change, error) {
	if len(want) == 0 {
		return nil, nil, nil
	}
	// Drive replaces the whole restrictions object, so the patch starts
	// from what is there: sending only the switch being changed would
	// silently clear the others.
	out := &gdrive.DriveRestrictions{}
	if current != nil {
		*out = *current
	}
	var changes []render.Change
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		on := want[name]
		if !model.SetDriveRestriction(out, name, on) {
			return nil, nil, Errorf(ClassInvalid, "restriction %q is not one of %s", name,
				strings.Join(model.DriveRestrictionNames, ", "))
		}
		was := model.DriveRestriction(current, name)
		if was == on {
			continue
		}
		changes = append(changes, render.Change{Field: "restriction " + name, From: yesNo(was), To: yesNo(on)})
	}
	return out, changes, nil
}

// driveResult renders a shared drive the way write renders a file: one
// description of what happened, laid out once as prose and once as
// structure, so the two cannot say different things.
func driveResult(d *model.Drive, out outcome) *Result {
	text := render.DriveCard(d, render.DriveCardOptions{
		Action: out.Action, DryRun: out.DryRun, Changes: out.Changes, Note: out.Note,
	})
	json := &render.WriteJSON{Summary: text, Action: string(out.Action), Note: out.Note,
		DryRun: out.DryRun, Changes: out.Changes, Drive: render.NewDriveJSON(d)}
	return &Result{Text: text, JSON: json}
}

// forgetDrives drops the cached drive list, which a create, rename or
// hide has just made wrong. Every location line and every drive: path
// reads through it.
func (s *Service) forgetDrives() {
	s.mu.Lock()
	s.drives, s.drivesAt = nil, time.Time{}
	s.mu.Unlock()
}

func hiddenWords(hidden bool) string {
	if hidden {
		return "hidden"
	}
	return "in the sidebar"
}

// requestID is the idempotency key drives.create requires: a random
// value that identifies this attempt, so a retry is recognised as the
// same request rather than as a second one.
func requestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", Errorf(ClassUnexpected, "could not generate a request id: %s", err)
	}
	return hex.EncodeToString(b[:]), nil
}
