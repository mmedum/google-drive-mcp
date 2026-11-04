package main

import (
	"errors"
	"fmt"
	"slices"

	"github.com/mmedum/google-drive-mcp/v3/scripts/internal/mcpstdio"
	"strings"
	"time"
)

// The five tools that remove something for good are registered only
// under GDRIVE_ENABLE_DESTRUCTIVE. empty_trash cannot be scoped to a
// folder — without a shared drive it takes the whole account's trash —
// so a scratch folder cannot hold it without risking somebody's real
// deleted work, and turning the gate on to reach delete_comment turns
// the other four on with it.
//
// So they run in a shared drive this driver makes and destroys again,
// which also holds the approved state and lock_file: an approved file is
// locked, and a scratch FOLDER holding something the driver cannot clean
// up would break its contract. A whole drive can be deleted, so it can
// hold one.
//
// The reference settles the one thing that could strand it: a content
// restriction makes a file read-only — no new revision, no comment, no
// rename — and says nothing about trashing or deletion, and files.delete
// takes no parameter that a restriction could refuse. drives.delete's
// allowItemDeletion needs useDomainAdminAccess, which this server does
// not offer, so the drive has to be emptied before it can go.

// destroyRun exercises the destructive five and the approval lock inside
// one throwaway shared drive.
//
// Every call it makes is scoped to driveID, and that is the whole safety
// argument: empty_trash without a drive empties the account's own trash,
// which on a real account is somebody's deleted work sitting in its
// thirty-day window. The scoping is not left to whoever writes the next
// call — emptyTrash below is the only place the tool is named, and it
// cannot be called before makeDrive has succeeded.
type destroyRun struct {
	*writeRun
	// driveID is the shared drive everything here happens in. While it
	// is empty nothing destructive may run at all.
	driveID string
	// driveName is what to tell a person to remove by hand if the run
	// cannot remove it itself.
	driveName string
	// living are the ids this run has made and not yet destroyed. Drive
	// refuses to delete a drive holding anything untrashed, so cleanup
	// has to take them, and a list kept as they are made is exact where
	// reading them back out of a listing would be a parser.
	living []string
}

// makeFile creates a file in the scratch drive and remembers it. The
// step c carries the name; makeFile builds the call.
func (d *destroyRun) makeFile(c call, name, content string) string {
	id := d.createAndKeepID(call{name: c.name, tool: "create_file",
		args: map[string]any{"name": name, "parent": d.driveID, "content": content}})
	if id != "" {
		d.living = append(d.living, id)
	}
	return id
}

// destroyFile deletes one for good and stops counting it as living, so
// cleanup does not try again. The step c carries the name; destroyFile
// builds the call.
//
// A tolerant step is for the cleanup pass, where emptying the drive's
// trash may already have taken the file — that is what the empty is for
// — and a refusal is the run working rather than failing.
func (d *destroyRun) destroyFile(c call, id string) {
	d.call(call{name: c.name, tool: "delete_file", args: map[string]any{"file": id, "confirm": true},
		tolerant: c.tolerant})
	d.living = slices.DeleteFunc(d.living, func(v string) bool { return v == id })
}

// runDestructive makes the scratch shared drive, exercises everything
// that needs one, and destroys the drive again.
//
// It returns without doing anything if the drive cannot be made, because
// every call below is only safe inside one.
func runDestructive(w *writeRun, name string) {
	d := &destroyRun{writeRun: w, driveName: name}
	d.out.Say("\n--- destructive five, in a shared drive of their own ---")
	d.out.Say("(everything below happens in a shared drive this run creates and destroys again)")

	d.driveID = w.createAndKeepID(call{name: "a scratch shared drive is created",
		tool: "manage_drive", args: map[string]any{
			"action": "create", "name": name,
		}})
	if d.driveID == "" {
		w.problem("the scratch shared drive could not be created, so nothing destructive can run safely",
			errors.New("manage_drive create returned no id"))
		return
	}

	// Deferred rather than last in the list: the drive is this run's
	// whole cleanup contract, and a step below returning early or
	// panicking must not be the reason a shared drive is left in
	// somebody's Workspace.
	defer d.cleanup(d.deleteTheDrive)

	d.driveScopedArguments()
	d.refusalsWithoutConfirm()
	d.deleteAFile()
	d.deleteARevision()
	d.deleteAComment()
	d.approvalLock()
	d.approvalNoAction()
	d.emptyTheDriveTrash()
}

// emptyTrash is the ONLY place the empty_trash tool is named, and it
// always names the scratch drive.
//
// Called with no drive the tool empties this ACCOUNT's own trash, which
// on a real account is somebody's deleted work sitting inside its
// thirty-day window, and nothing brings that back. Routing every call
// through here is the difference between a rule the code enforces and a
// rule whoever adds the next call has to remember —
// TestEmptyTrashIsNamedInOnePlace holds it. Its callers choose the
// arguments, confirm and dry_run, so it names the tool on the call it is
// given rather than building one.
func (d *destroyRun) emptyTrash(c call) {
	if d.driveID == "" {
		d.skipped(c.name, "there is no scratch drive to scope it to")
		return
	}
	c.tool = "empty_trash"
	if c.args == nil {
		c.args = map[string]any{}
	}
	c.args["drive"] = d.driveID
	d.call(c)
}

// refusalsWithoutConfirm checks the guard before the guarded thing.
//
// Every one of these tools refuses without confirm: true, and that
// refusal is the only thing standing between a model that misreads a
// request and something nobody can get back. It is worth more live proof
// than the deletions are.
func (d *destroyRun) refusalsWithoutConfirm() {
	id := d.makeFile(call{name: "a file for the confirm refusals is made in the scratch drive"},
		"refusal subject.txt", "nothing here matters\n")
	d.needing(id, call{name: "a permanent delete is refused without confirm",
		tool: "delete_file", args: map[string]any{"file": id},
		why: "a permanent delete without confirm: true"})
	d.emptyTrash(call{name: "emptying the drive's trash is refused without confirm",
		why: "emptying a trash without confirm: true"})
	d.call(call{name: "deleting a shared drive is refused without confirm",
		tool: "delete_drive", args: map[string]any{"drive": d.driveID},
		why: "destroying a shared drive without confirm: true"})

	// And the dry runs, which say what would go without anything going.
	d.needing(id, call{name: "a dry run of a permanent delete says what would go",
		tool: "delete_file", args: map[string]any{"file": id, "dry_run": true, "confirm": true}})
	d.emptyTrash(call{name: "a dry run of emptying the drive's trash says what would go",
		args: map[string]any{"dry_run": true, "confirm": true}})
	d.call(call{name: "a dry run of deleting the drive says what would go", tool: "delete_drive", args: map[string]any{
		"drive": d.driveID, "dry_run": true, "confirm": true,
	}})
	// The dry run must not have taken it. A dry run that deleted would
	// otherwise show up as nothing at all.
	d.needing(id, call{name: "a file survives a dry run of its permanent delete",
		tool: "get_file", args: map[string]any{"file": id}})
	if id != "" {
		d.destroyFile(call{name: "the file the dry runs left is deleted for good"}, id)
	}
}

// deleteAFile destroys one for good and then proves it is gone, because
// a delete that reported success and left the file is the failure worth
// catching and the only way to see it is to look.
func (d *destroyRun) deleteAFile() {
	id := d.makeFile(call{name: "a file to delete for good is made in the scratch drive"},
		"delete me.txt", "gone for good\n")
	if id == "" {
		d.out.Say("\n=== delete_file: skipped, the file it needs was never created ===")
		return
	}
	d.destroyFile(call{name: "a file is deleted for good"}, id)
	d.call(call{name: "a file deleted for good is not found", tool: "get_file", args: map[string]any{"file": id},
		why: "a permanently deleted file is not there any more"})
}

// deleteARevision removes an old version and leaves the current one.
//
// Drive allows this only for a file with bytes of its own and never for
// the version the file is at now, so the run makes a second revision
// first and deletes the first one.
func (d *destroyRun) deleteARevision() {
	id := d.makeFile(call{name: "a file with revisions to delete is made in the scratch drive"},
		"revisions.txt", "first\n")
	if id == "" {
		d.out.Say("\n=== delete_revision: skipped, the file it needs was never created ===")
		return
	}
	d.call(call{name: "a second revision is written so the first can be deleted",
		tool: "update_content", args: map[string]any{
			"file": id, "content": "second\n", "keep_previous_revision": true,
		}})
	first := d.revisionID(id, true)
	if first == "" {
		// The listing lags the write that made the second revision, so a
		// run can find only the current one and skip the whole section,
		// and a step that never ran must not pass as verified. A pause is
		// fine here: this is a driver, not a tool call somebody is waiting
		// on.
		d.out.Say("\n(only the current revision is listed yet; waiting for Drive to catch up)")
		d.pause(10 * time.Second)
		first = d.revisionID(id, true)
	}
	if first == "" {
		// UNVERIFIED rather than a failure, as the property search treats
		// the identical thing: Drive did not present the state, and
		// nothing went wrong. Counting it would make a healthy run exit
		// non-zero for a reason outside anybody's control, which teaches
		// whoever runs this to stop reading exit codes. It stays loud,
		// because a step that never ran must not pass as verified.
		d.unverified("delete_revision was not exercised: no revision but the current one is listed, "+
			"even after waiting", errors.New("no older revision to delete"))
		return
	}
	d.call(call{name: "a dry run of deleting a revision says what would go", tool: "delete_revision", args: map[string]any{
		"file": id, "revision": first, "dry_run": true, "confirm": true,
	}})
	gone := d.call(call{name: "an old revision is deleted for good", tool: "delete_revision", args: map[string]any{
		"file": id, "revision": first, "confirm": true,
	}, tolerant: true})
	if !strings.Contains(gone, "gone for good") {
		// The dry run above found this revision and the delete did not,
		// which is Drive disagreeing with itself for a moment after a
		// write. One retry after a pause, the same treatment the drive
		// delete gets and for the same reason.
		d.out.Say("\n(the revision was there for the dry run and not for the delete; waiting)")
		d.pause(10 * time.Second)
		d.call(call{name: "an old revision is deleted again after a pause", tool: "delete_revision", args: map[string]any{
			"file": id, "revision": first, "confirm": true,
		}})
	}
	// The content the file is at now must have survived it.
	d.call(call{name: "the current content survives deleting an old revision",
		tool: "read_file", args: map[string]any{"file": id}})
	d.call(call{name: "the revisions left after a delete are listed",
		tool: "list_revisions", args: map[string]any{"file": id}})
}

// deleteAComment removes a thread. It is the one destructive tool a
// scratch folder could hold, and runs here because it shares its gate
// with the other four.
func (d *destroyRun) deleteAComment() {
	id := d.makeFile(call{name: "a file to comment on is made in the scratch drive"},
		"commented.txt", "worth discussing\n")
	if id == "" {
		d.out.Say("\n=== delete_comment: skipped, the file it needs was never created ===")
		return
	}
	comment := commentFromResult(d.call(call{name: "a comment is added to a file in a shared drive",
		tool: "add_comment", args: map[string]any{
			"file": id, "content": "this thread is about to be removed",
		}}))
	if comment == "" {
		d.out.Say("\n=== delete_comment: skipped, the comment it needs was never made ===")
		return
	}
	// A reply, so there is one to delete on its own. Deleting a reply and
	// deleting the thread it sits in are different calls with different
	// consequences, so both run.
	d.call(call{name: "a reply is added so it can be removed on its own", tool: "reply_comment", args: map[string]any{
		"file": id, "comment": comment, "content": "and this reply with it",
	}})
	d.call(call{name: "removing a comment is refused without confirm",
		tool: "delete_comment", args: map[string]any{"file": id, "comment": comment},
		why: "removing a comment without confirm: true"})
	d.call(call{name: "a dry run of removing a comment says what would go",
		tool: "delete_comment", args: map[string]any{"file": id, "comment": comment, "dry_run": true}})
	// One reply first, then the whole thread. Deleting a single reply
	// needs its id, which lives only in a listing — the renderer prints
	// it indented under its thread rather than on an `id:` line — so it
	// is read from there.
	listing := d.call(call{name: "the thread is listed for its reply ids",
		tool: "list_comments", args: map[string]any{"file": id}})
	if replies := mcpstdio.ReplyIDs(listing); len(replies) > 0 {
		d.call(call{name: "one reply is removed from its thread", tool: "delete_comment", args: map[string]any{
			"file": id, "comment": comment, "reply": replies[0], "confirm": true,
		}})
	} else {
		d.unverified("delete_comment.reply was not exercised: the listing showed no reply id",
			errors.New("the thread this run made has no reply, or the renderer has changed shape"))
	}
	d.call(call{name: "a whole comment thread is removed", tool: "delete_comment", args: map[string]any{
		"file": id, "comment": comment, "confirm": true,
	}})
	// Drive keeps the thread with its words removed, which is worth
	// seeing rather than asserting: the listing is what a person would
	// look at afterwards.
	d.call(call{name: "a removed thread is listed with its words gone",
		tool: "list_comments", args: map[string]any{"file": id, "include_deleted": true}})
}

// approvalLock reaches the two states that need a drive that can be
// deleted whole: a file locked while an approval is open, and a file
// approved, which locks it again with no way to reopen it.
//
// The write run cancels its approval instead, because an approved file
// is locked and a scratch folder cannot be trashed around it. Here the
// whole drive goes at the end, so the lock has somewhere to live.
func (d *destroyRun) approvalLock() {
	me := d.accountAddress()
	if me == "" {
		d.out.Say("\n=== approval lock: skipped, this run could not read its own address ===")
		return
	}
	// A blob, not a Google Doc. The promise being checked is that the
	// file's CONTENT cannot be changed, and this server never writes a
	// Doc's content at all — it refuses that for its own reasons, which
	// would look exactly like the lock working.
	id := d.makeFile(call{name: "a file to lock with an approval is made in the scratch drive"},
		"locked.txt", "before the approval\n")
	if id == "" {
		d.out.Say("\n=== approval lock: skipped, the file it needs was never created ===")
		return
	}

	// lock_file on an OPEN approval, first and on its own, so whatever
	// it does can be told apart from what approving does.
	locked := approvalFromResult(d.call(call{name: "an approval starts with lock_file set",
		tool: "manage_approval", args: map[string]any{
			"file": id, "action": "start", "reviewers": []string{me},
			"message": "a scratch approval that locks the file", "lock_file": true,
		}}))
	d.out.Say("\n(the card below is the whole question: manage_approval has just said the file " +
		"is LOCKED, so a content restriction should be on it)")
	d.call(call{name: "the card shows what lock_file did to the file", tool: "get_file", args: map[string]any{"file": id}})
	// NOT expected to be refused: lock_file at the start of an approval
	// applies no content restriction, and the content change goes
	// through. Left as a plain call so that a Drive which starts locking
	// shows up here as an unexpected refusal rather than as silence.
	d.out.Say("\n(this content change is expected to SUCCEED: lock_file applied no restriction)")
	d.call(call{name: "the content of a file under lock_file is changed", tool: "update_content", args: map[string]any{
		"file": id, "content": "changed while locked\n",
	}})
	// A rename is not a content change, so this is an observation rather
	// than an expectation: the reference says a content restriction stops
	// the title too, and whether lock_file sets one is what this is for.
	d.call(call{name: "a file under lock_file is renamed", tool: "update_file", args: map[string]any{
		"file": id, "name": "renamed while locked.txt",
	}})
	if locked == "" {
		d.out.Say("\n=== approval lock: the approval id was not readable, so it cannot be approved ===")
		return
	}

	// And the approved state: approving completes the approval and locks
	// the file for good, with no way to reopen it.
	d.call(call{name: "the lock_file approval is approved", tool: "approve_file", args: map[string]any{
		"file": id, "approval": locked,
	}})
	d.call(call{name: "the approved approval is listed", tool: "list_approvals", args: map[string]any{"file": id}})
	d.call(call{name: "the approved file's card is read", tool: "get_file", args: map[string]any{"file": id}})
	d.call(call{name: "a content change to an approved file is refused", tool: "update_content", args: map[string]any{
		"file": id, "content": "changed after approval\n",
	}, why: "an approved file is locked"})

	// The reference says a content restriction stops content, comments
	// and renames, and says nothing about removal. If that is wrong this
	// is where the run finds out, and the drive would not delete below.
	d.destroyFile(call{name: "an approved, locked file is deleted for good"}, id)
}

// approvalNoAction starts an approval with on_content_change no_action,
// approves it, and changes the content after. The approvals guide says
// such an approval does not lock the file once approved; this is where
// a run finds out if Drive disagrees. It runs in the scratch drive all
// the same, so a lock it did not expect is deleted with the drive.
func (d *destroyRun) approvalNoAction() {
	me := d.accountAddress()
	id := d.makeFile(call{name: "a file to approve without a lock is made in the scratch drive"},
		"approved and still open.txt", "before the approval\n")
	if me == "" || id == "" {
		d.out.Say("\n=== approval without a lock: skipped, the address or the file it needs is missing ===")
		return
	}
	started := d.call(call{name: "an approval starts with on_content_change no_action",
		tool: "manage_approval", args: map[string]any{
			"file": id, "action": "start", "reviewers": []string{me}, "on_content_change": "no_action",
			"message": "a scratch approval that should not lock the file",
		}})
	if !strings.Contains(started, "approving does not lock the file") {
		d.problem("the start of an approval with on_content_change no_action does not say approving leaves "+
			"the file unlocked", errors.New("no NO_APPROVAL_ACTION in the answer"))
	}
	approval := approvalFromResult(started)
	if approval == "" {
		d.out.Say("\n=== approval without a lock: the approval id was not readable, so it cannot be approved ===")
		d.destroyFile(call{name: "a file whose approval id is unreadable is deleted for good"}, id)
		return
	}
	d.call(call{name: "the no_action approval is approved",
		tool: "approve_file", args: map[string]any{"file": id, "approval": approval}})
	if card := d.call(call{name: "a file approved under no_action stays unlocked",
		tool: "get_file", args: map[string]any{"file": id}}); strings.Contains(card, "content locked") {
		d.problem("an approval started with no_action locked the file once approved", errors.New("locked"))
	}
	d.out.Say("\n(this content change is expected to SUCCEED: the approval did not lock the file)")
	d.call(call{name: "a file approved under no_action takes a content change",
		tool: "update_content", args: map[string]any{"file": id, "content": "changed after approval\n"}})
	d.destroyFile(call{name: "the file approved without a lock is deleted for good"}, id)
}

// emptyTheDriveTrash puts something in the scratch drive's trash and
// then empties it, which is the only way to see that the call did
// anything: it is the one destructive tool that does not name what it
// removed.
func (d *destroyRun) emptyTheDriveTrash() {
	if d.driveID == "" {
		return
	}
	id := d.makeFile(call{name: "a file to empty from the trash is made in the scratch drive"},
		"trash me.txt", "into the trash\n")
	d.needing(id, call{name: "a file in the scratch drive is trashed",
		tool: "trash_file", args: map[string]any{"file": id}})
	d.emptyTrash(call{name: "a dry run of emptying the trash counts a file trashed seconds ago",
		args: map[string]any{"dry_run": true, "confirm": true}})
	d.emptyTrash(call{name: "the drive's trash is emptied", args: map[string]any{"confirm": true}})
	// Whether the file survived is the whole question — files.emptyTrash
	// has no response, so a look is the only evidence there is. Marked
	// tolerant because both answers are the server behaving correctly:
	// a refusal means the empty took, a card means Drive's view of its
	// own trash has not caught up. Expecting either one would count a
	// failure exactly when the other happens.
	//
	// It must NOT be restore_file. Restoring un-trashes the file, and an
	// untrashed file is exactly what stops the drive being deleted at
	// the end, stranding a shared drive in a real Workspace.
	if id != "" {
		d.call(call{name: "a file is looked for after the drive's trash is emptied",
			tool: "get_file", args: map[string]any{"file": id}, tolerant: true})
	}
}

// deleteTheDrive destroys the scratch drive, which is this run's whole
// cleanup contract: everything above lives inside it.
//
// Drive refuses while the drive still holds anything untrashed, so a
// failure here means something was left, and it says so with the name
// rather than counting it and moving on — the same treatment the shared
// drive move-back gets, and for the same reason.
func (d *destroyRun) deleteTheDrive() {
	if d.driveID == "" {
		return
	}
	// Whatever is left goes first. Drive refuses to delete a drive
	// holding anything untrashed, and the sections above deliberately
	// leave files behind — the one with a deleted comment on it, the one
	// with revisions. Cleanup that assumed they had tidied up after
	// themselves would strand a shared drive in a real Workspace.
	// Taken and cleared before the loop: destroyFile drops each id from
	// d.living, and shortening a slice while ranging over it reads
	// shifted elements.
	left := d.living
	d.living = nil
	for _, id := range left {
		d.destroyFile(call{name: "a file left in the scratch drive is deleted for good", tolerant: true}, id)
	}
	d.emptyTrash(call{name: "the drive's trash is emptied before the drive is deleted",
		args: map[string]any{"confirm": true}})
	out := d.call(call{name: "the scratch shared drive is deleted", tool: "delete_drive", args: map[string]any{
		"drive": d.driveID, "confirm": true,
	}})
	if strings.Contains(out, "gone for good") {
		return
	}
	// One retry, AFTER a pause. The reason for the first refusal is
	// Drive not yet agreeing the trash is empty, and an immediate retry
	// is the one thing least likely to help something that is waiting to
	// catch up: the same delete succeeds minutes later.
	d.pause(10 * time.Second)
	again := d.call(call{name: "the scratch shared drive is deleted again after a pause",
		tool: "delete_drive", args: map[string]any{
			"drive": d.driveID, "confirm": true,
		}})
	if strings.Contains(again, "gone for good") {
		return
	}
	d.problem("A SCRATCH SHARED DRIVE IS STILL THERE, named "+d.driveName+
		", and this run cannot remove it. Trash what is left inside it, empty its trash, "+
		"and delete it by hand", errors.New("delete_drive failed twice"))
}

// driveScratchName is what the shared drive is called. It says "drive"
// so that a drive left behind by an interrupted run is not mistaken for
// the My Drive folder the same run makes, and carries the same prefix so
// both are recognizable as this driver's.
func driveScratchName(stamp string) string {
	return fmt.Sprintf("%s drive %s", scratchPrefix, stamp)
}

// driveScopedArguments drives the options that need a shared drive, which
// this run is the only part of the driver to have one of.
//
// Every one of these is one argument on a call, given somewhere to point
// it. Restricting a drive is safe here for the reason the whole section
// is safe — the drive belongs to this run and is destroyed at the end of
// it.
func (d *destroyRun) driveScopedArguments() {
	d.out.Say("\n--- the drive-scoped options nothing had sent ---")
	d.needing(d.driveID, call{name: "a shared drive is found by name with hidden drives included",
		tool: "list_drives", args: map[string]any{
			"name": d.driveName, "include_hidden": true,
		}})
	d.needing(d.driveID, call{name: "a search is scoped to one shared drive", tool: "search_files", args: map[string]any{
		"drive": d.driveID, "in_folder": d.driveID,
	}})
	d.needing(d.driveID, call{name: "the changes feed is read for one shared drive",
		tool: "list_changes", args: map[string]any{"drive": d.driveID, "limit": 5}})
	d.needing(d.driveID, call{name: "a dry run of restricting a drive says what would change",
		tool: "manage_drive", args: map[string]any{
			"action": "restrict", "drive": d.driveID, "dry_run": true,
			"restrictions": map[string]any{"copy_requires_writer_permission": true},
		}})
	d.needing(d.driveID, call{name: "a drive restriction is turned on", tool: "manage_drive", args: map[string]any{
		"action": "restrict", "drive": d.driveID,
		"restrictions": map[string]any{"copy_requires_writer_permission": true},
	}})
	// Turning it off again loosens the drive, and is an ordinary write.
	d.needing(d.driveID, call{name: "a drive restriction is turned off again", tool: "manage_drive", args: map[string]any{
		"action": "restrict", "drive": d.driveID,
		"restrictions": map[string]any{"copy_requires_writer_permission": false},
	}})
	d.liftDomainUsersOnly()
}

// liftDomainUsersOnly turns domain_users_only on with manage_drive, then
// off again, which manage_drive refuses naming lift_drive_restriction:
// with it off, what is in the drive can be shared outside the
// organization. lift_drive_restriction refuses without confirm, dry runs,
// and lifts it, read back from the drive. members_only off stays
// manage_drive's, since whether turning it off restores access already
// granted to non-members is unverified.
func (d *destroyRun) liftDomainUsersOnly() {
	d.needing(d.driveID, call{name: "a drive is limited to the organization's users",
		tool: "manage_drive", args: map[string]any{
			"action": "restrict", "drive": d.driveID,
			"restrictions": map[string]any{"domain_users_only": true},
		}})
	d.refusing(d.driveID, call{name: "manage_drive refuses to lift domain_users_only",
		tool: "manage_drive", args: map[string]any{
			"action": "restrict", "drive": d.driveID,
			"restrictions": map[string]any{"domain_users_only": false},
		}, why: "lift_drive_restriction does it"})
	d.refusing(d.driveID, call{name: "lifting a drive restriction is refused without confirm",
		tool: "lift_drive_restriction", args: map[string]any{
			"drive": d.driveID, "drive_name": d.driveName, "restriction": "domain_users_only",
		}, why: "confirm: true"})
	d.refusing(d.driveID, call{name: "lifting a drive restriction is refused under a stale drive name",
		tool: "lift_drive_restriction", args: map[string]any{
			"drive": d.driveID, "drive_name": d.driveName + " (another)", "restriction": "domain_users_only",
			"confirm": true,
		}, why: "[stale]"})
	d.needing(d.driveID, call{name: "a dry run of lifting a drive restriction says what would change",
		tool: "lift_drive_restriction", args: map[string]any{
			"drive": d.driveID, "drive_name": d.driveName, "restriction": "domain_users_only", "dry_run": true,
		}})
	lifted := d.call(call{name: "a drive restriction is lifted and read back",
		tool: "lift_drive_restriction", args: map[string]any{
			"drive": d.driveID, "drive_name": d.driveName, "restriction": "domain_users_only", "confirm": true,
		}})
	if !strings.Contains(lifted, "restriction domain_users_only: yes → no") {
		d.problem("lift_drive_restriction does not report domain_users_only going from on to off",
			errors.New("the change was not read back"))
	}
}
