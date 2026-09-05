package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/render"
)

// DeleteFileInput names one item to remove for good.
type DeleteFileInput struct {
	File string
	// Confirm is the acknowledgement that this cannot be undone. The
	// deployer's GDRIVE_ENABLE_DESTRUCTIVE decides whether the tool
	// exists at all; this decides whether one particular call goes
	// through, because a tool that is registered is a tool a model will
	// eventually reach for.
	Confirm bool
	DryRun  bool
}

// DeleteFile removes a file permanently, skipping the trash. There is no
// undo at any level: not in Drive, not for an administrator, not through
// support.
func (s *Service) DeleteFile(ctx context.Context, in DeleteFileInput) (*Result, error) {
	if err := s.destructive("delete_file"); err != nil {
		return nil, err
	}
	// A shortcut is deleted as itself; following it would destroy the
	// wrong thing, and that mistake is not recoverable here.
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: false, Fresh: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	if err := s.notADriveRoot(ctx, f); err != nil {
		return nil, err
	}
	if f.Capabilities != nil && !f.Capabilities.CanDelete {
		return nil, Errorf(ClassForbidden, "this account cannot delete %s. In My Drive only the owner can; "+
			"in a shared drive it takes an organizer on the folder it is in.", f.Name)
	}
	what := describeDeleteSubject(f)
	if in.DryRun {
		return s.report(ctx, res, outcome{Action: render.ActionDeleted, DryRun: true,
			Note: what + " would be gone for good, with no way back."}), nil
	}
	if err := s.confirmed(in.Confirm, "delete_file", what+" permanently, with no way back. "+
		"trash_file removes it reversibly instead"); err != nil {
		return nil, err
	}
	if err := s.api.DeleteFile(ctx, f.ID); err != nil {
		return nil, wrap(err, "permanently deleting "+f.Name)
	}
	// Everything that pointed here now points at nothing.
	s.forget(f, true)
	return s.report(ctx, res, outcome{Action: render.ActionDeleted,
		Note: what + " is gone for good. It was not put in the trash, so there is nothing to restore."}), nil
}

// notADriveRoot refuses to destroy the top of a drive. "root" is Drive's
// alias for My Drive's root folder and a shared drive's id is its own
// root folder's id, so either can be passed anywhere a file can — and a
// call meant to remove one file would take an entire Drive with it.
//
// Google refuses both today through capabilities.canDelete, and that is
// the whole reason to state it here as well: a bounded call becoming an
// unbounded one must not rest on a field the other side computes. Drive
// answering differently one day, or not sending capabilities at all,
// would be enough.
func (s *Service) notADriveRoot(ctx context.Context, f *gdrive.File) error {
	switch {
	case f.ID == RootAlias || f.ID == s.rootID(ctx):
		return Errorf(ClassForbidden, "that is the root of My Drive, not a file in it. Deleting it is not "+
			"something this server will attempt. Name the item to remove, or trash a folder inside it.")
	case f.DriveID != "" && f.ID == f.DriveID:
		return Errorf(ClassForbidden, "%s is the top of a shared drive, and deleting it would be deleting the "+
			"drive. delete_drive does that, and only for a drive that already holds nothing.", f.Name)
	}
	return nil
}

// describeDeleteSubject names what is about to be destroyed, saying when
// a folder takes its contents with it.
func describeDeleteSubject(f *gdrive.File) string {
	if f.IsFolder() {
		return f.Name + " (a folder, with everything inside it that this account owns)"
	}
	return f.Name
}

// EmptyTrashInput names whose trash to empty.
type EmptyTrashInput struct {
	// Drive empties one shared drive's trash instead of this account's.
	Drive   string
	Confirm bool
	DryRun  bool
}

// EmptyTrash removes everything in the trash for good. It is the one
// destructive call whose subject is not named item by item, so it says
// how much it is about to destroy before it does.
func (s *Service) EmptyTrash(ctx context.Context, in EmptyTrashInput) (*Result, error) {
	if err := s.destructive("empty_trash"); err != nil {
		return nil, err
	}
	driveID, driveName := "", ""
	if name := strings.TrimSpace(in.Drive); name != "" {
		d, err := s.findDrive(ctx, name)
		if err != nil {
			return nil, err
		}
		driveID, driveName = d.ID, d.Name
	}
	whose := "this account's trash"
	if driveName != "" {
		whose = "the trash of the shared drive " + driveName
	}
	count, counted := s.trashCount(ctx, driveID)
	what := whose
	if counted {
		what = fmt.Sprintf("%s (%s in it right now)", whose, model.Plural(count, "item", "items"))
	}

	if in.DryRun {
		return emptyTrashResult(outcome{Action: render.ActionEmptied, DryRun: true,
			Note: "everything in " + what + " would be gone for good."}), nil
	}
	if err := s.confirmed(in.Confirm, "empty_trash", "destroy everything in "+what+
		", with no way back. Items in the trash can be restored one by one with restore_file until this runs"); err != nil {
		return nil, err
	}
	if err := s.api.EmptyTrash(ctx, driveID); err != nil {
		return nil, wrap(err, "emptying "+whose)
	}
	return emptyTrashResult(outcome{Action: render.ActionEmptied,
		Note: whose + " is empty. Everything that was in it is gone for good."}), nil
}

// trashCount counts what is in the trash, so a call that names no item
// can still say what it is about to destroy. It is a courtesy: a listing
// that fails must not stop the call the caller asked for, so the count
// is reported as unknown rather than as zero.
func (s *Service) trashCount(ctx context.Context, driveID string) (int, bool) {
	// Without a drive the subject is this account's own trash, and a
	// listing defaults to every drive it can see: counting that way would
	// report a shared drive's trashed items as part of what this call is
	// about to destroy, which it is not.
	q := gapi.ListQuery{Q: "trashed = true", PageSize: gapi.MaxPageSize,
		Fields: "files(id)", DriveID: driveID}
	if driveID == "" {
		// files.emptyTrash with no driveId deletes "all of the user's
		// trashed files", so the count has to be the same set. A listing
		// defaults to every drive this account can see and includes
		// items it merely has access to: counting that way reported a
		// shared drive's trash, and files owned by other people, as part
		// of what this call is about to destroy. It is not.
		q.Corpora = gapi.CorporaUser
		q.Q += " and 'me' in owners"
	}
	list, err := s.api.ListFiles(ctx, q)
	if err != nil {
		s.log.DebugContext(ctx, "trash count unavailable", "class", gapi.Class(err))
		return 0, false
	}
	// A full page means there may be more; saying "1000" when it is
	// 4000 would be worse than saying nothing.
	if list.NextPageToken != "" {
		return 0, false
	}
	return len(list.Files), true
}

// emptyTrashResult renders a destruction that names no file, so there is
// no card to show.
func emptyTrashResult(out outcome) *Result {
	var b strings.Builder
	if out.DryRun {
		b.WriteString("would have emptied the trash\n")
	} else {
		b.WriteString("emptied the trash\n")
	}
	b.WriteString("note: " + out.Note + "\n")
	if out.DryRun {
		b.WriteString("NOTHING WAS CHANGED: this was a dry run. Call it again without dry_run to do it.\n")
	}
	text := b.String()
	return &Result{Text: text, JSON: &render.WriteJSON{Summary: text, Action: string(out.Action),
		Note: out.Note, DryRun: out.DryRun}}
}

// DeleteDriveInput names a shared drive to remove.
type DeleteDriveInput struct {
	Drive   string
	Confirm bool
	DryRun  bool
}

// DeleteDrive removes an empty shared drive. Drive refuses one that
// still holds untrashed items, which is the safeguard that matters, and
// this server never asks for the override that would delete the contents
// too.
func (s *Service) DeleteDrive(ctx context.Context, in DeleteDriveInput) (*Result, error) {
	if err := s.destructive("delete_drive"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Drive) == "" {
		return nil, Errorf(ClassInvalid, "drive is required: name the shared drive to delete, by name or id")
	}
	d, err := s.findDrive(ctx, in.Drive)
	if err != nil {
		return nil, err
	}
	if d.Capabilities != nil && !d.Capabilities.CanDeleteDrive {
		return nil, Errorf(ClassForbidden, "this account cannot delete the shared drive %s; that is an "+
			"organizer's to do, and only when the drive is empty.", d.Name)
	}
	what := "the shared drive " + d.Name
	if in.DryRun {
		return driveResult(model.NewDrive(d), outcome{Action: render.ActionDeleted, DryRun: true,
			Note: what + " would be gone for good. Drive refuses it while anything untrashed is still in it."}), nil
	}
	if err := s.confirmed(in.Confirm, "delete_drive", "destroy "+what+", with no way back"); err != nil {
		return nil, err
	}
	if err := s.api.DeleteDrive(ctx, d.ID); err != nil {
		return nil, s.deleteDriveError(err, d)
	}
	s.forgetDrives()
	return driveResult(model.NewDrive(d), outcome{Action: render.ActionDeleted,
		Note: what + " is gone for good."}), nil
}

// deleteDriveError names the refusal that has an obvious next step: a
// drive that still holds something.
func (s *Service) deleteDriveError(err error, d *gdrive.Drive) error {
	if gapi.Class(err) == ClassForbidden || gapi.Class(err) == ClassInvalid {
		return &Error{Class: gapi.Class(err), Message: fmt.Sprintf(
			"Drive refused to delete the shared drive %s. It only deletes one that holds nothing untrashed, "+
				"and this server never asks for the override that would take the contents with it: trash "+
				"what is in there first, or empty_trash it. Google said: %s", d.Name, gapi.Message(err)), Err: err}
	}
	return wrap(err, "deleting the shared drive "+d.Name)
}

// DeleteRevisionInput names one revision to remove.
type DeleteRevisionInput struct {
	File     string
	Revision string
	Confirm  bool
	DryRun   bool
}

// DeleteRevision removes one version of a file's content for good. Drive
// allows it only for a file with bytes of its own, and never for the
// version the file is currently at.
func (s *Service) DeleteRevision(ctx context.Context, in DeleteRevisionInput) (*Result, error) {
	if err := s.destructive("delete_revision"); err != nil {
		return nil, err
	}
	revisionID := strings.TrimSpace(in.Revision)
	if revisionID == "" {
		return nil, Errorf(ClassInvalid, "revision is required: list_revisions shows the ids")
	}
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	if f.IsWorkspaceDoc() {
		return nil, Errorf(ClassUnsupported, "%s is %s, and Drive only deletes revisions of a file with "+
			"bytes of its own. A Google document's history belongs to the editor, not to this API.",
			f.Name, model.KindWithArticle(f))
	}
	if revisionID == f.HeadRevisionID {
		return nil, Errorf(ClassInvalid, "revision %s is the version %s is at now, and Drive will not delete "+
			"the current one. update_content replaces it; list_revisions shows the older ones.", revisionID, f.Name)
	}
	if _, err := s.api.GetRevision(ctx, f.ID, revisionID); err != nil {
		return nil, s.revisionError(err, f, revisionID)
	}
	what := fmt.Sprintf("revision %s of %s", revisionID, f.Name)
	if in.DryRun {
		return s.report(ctx, res, outcome{Action: render.ActionDeleted, DryRun: true,
			Note: what + " would be gone for good; the file's current content is untouched."}), nil
	}
	if err := s.confirmed(in.Confirm, "delete_revision", "destroy "+what+
		", with no way back. The file's current content is untouched"); err != nil {
		return nil, err
	}
	if err := s.api.DeleteRevision(ctx, f.ID, revisionID); err != nil {
		return nil, wrap(err, "deleting "+what)
	}
	return s.report(ctx, res, outcome{Action: render.ActionDeleted,
		Note: what + " is gone for good. The file's current content is untouched."}), nil
}

// destructive refuses every permanent removal unless the deployer turned
// them on. The tools are not registered otherwise, so this is the second
// line: it holds if a later tool forgets to ask.
func (s *Service) destructive(tool string) error {
	if err := s.writable(tool); err != nil {
		return err
	}
	if !s.opts.Destructive {
		return Errorf(ClassForbidden, "%s destroys something with no way back, and this server was not "+
			"started with GDRIVE_ENABLE_DESTRUCTIVE=true, so it is not available. trash_file removes an item "+
			"reversibly and restore_file brings it back.", tool)
	}
	return nil
}

// confirmed refuses a destruction that was not acknowledged on the call
// itself. The deployer's switch decides whether the tool exists; this
// decides whether one particular call goes through, and the two are
// different questions: a registered tool is one a model will reach for
// eventually, and the second half of "are you sure" belongs at the call.
func (s *Service) confirmed(confirm bool, tool, what string) error {
	if confirm {
		return nil
	}
	return Errorf(ClassForbidden, "%s would %s. Pass confirm: true on the same call if that is really "+
		"what is wanted; dry_run: true shows what it would do and changes nothing.", tool, what)
}
