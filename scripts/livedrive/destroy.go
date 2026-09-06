package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The five tools that remove something for good are registered only
// under GDRIVE_ENABLE_DESTRUCTIVE, and until now none of them had ever
// run against Drive. §17a said why: empty_trash cannot be scoped to a
// folder — without a shared drive it takes the whole account's trash —
// so the driver could not reach it without risking somebody's real
// deleted work, and turning the gate on to reach delete_comment turned
// the other four on with it.
//
// A shared drive this driver makes and destroys again is the answer, and
// it is the same thing the approved state and lock_file were waiting for:
// an approved file is locked, and a scratch FOLDER holding something the
// driver cannot clean up would break its contract. A whole drive can be
// deleted, so it can hold one.
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

// makeFile creates a file in the scratch drive and remembers it.
func (d *destroyRun) makeFile(name, content string) string {
	args := map[string]any{"name": name, "parent": d.driveID, "content": content}
	if content == "" {
		delete(args, "content")
		args["kind"] = "doc"
	}
	id := d.createAndKeepID("create_file", args)
	if id != "" {
		d.living = append(d.living, id)
	}
	return id
}

// destroyFile deletes one for good and stops counting it as living, so
// cleanup does not try again and count a not_found as a failure.
func (d *destroyRun) destroyFile(id string) {
	d.call(call{tool: "delete_file", args: map[string]any{"file": id, "confirm": true}})
	d.living = slices.DeleteFunc(d.living, func(v string) bool { return v == id })
}

// runDestructive makes the scratch shared drive, exercises everything
// that needs one, and destroys the drive again.
//
// It returns without doing anything if the drive cannot be made, because
// every call below is only safe inside one.
func runDestructive(w *writeRun, name string) {
	d := &destroyRun{writeRun: w, driveName: name}
	fmt.Println("\n--- destructive five, in a shared drive of their own ---")
	fmt.Println("(everything below happens in a shared drive this run creates and destroys again)")

	d.driveID = w.createAndKeepID("manage_drive", map[string]any{
		"action": "create", "name": name,
	})
	if d.driveID == "" {
		w.problem("the scratch shared drive could not be created, so nothing destructive can run safely",
			errors.New("manage_drive create returned no id"))
		return
	}

	// Deferred rather than last in the list: the drive is this run's
	// whole cleanup contract, and a step below returning early or
	// panicking must not be the reason a shared drive is left in
	// somebody's Workspace.
	defer d.deleteTheDrive()

	d.refusalsWithoutConfirm()
	d.deleteAFile()
	d.deleteARevision()
	d.deleteAComment()
	d.approvalLock()
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
// TestEmptyTrashIsNamedInOnePlace holds it.
func (d *destroyRun) emptyTrash(c call) {
	if d.driveID == "" {
		fmt.Println("\n=== empty_trash: skipped, there is no scratch drive to scope it to ===")
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
	id := d.makeFile("refusal subject.txt", "nothing here matters\n")
	d.expecting("delete_file", id, map[string]any{"file": id},
		"a permanent delete without confirm: true")
	d.emptyTrash(call{expectError: true, why: "emptying a trash without confirm: true"})
	d.call(call{tool: "delete_drive", args: map[string]any{"drive": d.driveID},
		expectError: true, why: "destroying a shared drive without confirm: true"})

	// And the dry runs, which say what would go without anything going.
	d.needing("delete_file", id, map[string]any{"file": id, "dry_run": true, "confirm": true})
	d.emptyTrash(call{args: map[string]any{"dry_run": true, "confirm": true}})
	d.call(call{tool: "delete_drive", args: map[string]any{
		"drive": d.driveID, "dry_run": true, "confirm": true,
	}})
	// The dry run must not have taken it. A dry run that deleted would
	// otherwise show up as nothing at all.
	d.needing("get_file", id, map[string]any{"file": id})
	if id != "" {
		d.destroyFile(id)
	}
}

// deleteAFile destroys one for good and then proves it is gone, because
// a delete that reported success and left the file is the failure worth
// catching and the only way to see it is to look.
func (d *destroyRun) deleteAFile() {
	id := d.makeFile("delete me.txt", "gone for good\n")
	if id == "" {
		fmt.Println("\n=== delete_file: skipped, the file it needs was never created ===")
		return
	}
	d.destroyFile(id)
	d.call(call{tool: "get_file", args: map[string]any{"file": id},
		expectError: true, why: "a permanently deleted file is not there any more"})
}

// deleteARevision removes an old version and leaves the current one.
//
// Drive allows this only for a file with bytes of its own and never for
// the version the file is at now, so the run makes a second revision
// first and deletes the first one.
func (d *destroyRun) deleteARevision() {
	id := d.makeFile("revisions.txt", "first\n")
	if id == "" {
		fmt.Println("\n=== delete_revision: skipped, the file it needs was never created ===")
		return
	}
	d.call(call{tool: "update_content", args: map[string]any{
		"file": id, "content": "second\n", "keep_previous_revision": true,
	}})
	first := d.olderRevision(id)
	if first == "" {
		fmt.Println("\n=== delete_revision: skipped, no earlier revision to remove ===")
		return
	}
	d.call(call{tool: "delete_revision", args: map[string]any{
		"file": id, "revision": first, "dry_run": true, "confirm": true,
	}})
	d.call(call{tool: "delete_revision", args: map[string]any{
		"file": id, "revision": first, "confirm": true,
	}})
	// The content the file is at now must have survived it.
	d.call(call{tool: "read_file", args: map[string]any{"file": id}})
	d.call(call{tool: "list_revisions", args: map[string]any{"file": id}})
}

// olderRevision reads the id of a revision that is NOT the current one.
//
// writeRun.firstRevision takes the first row of the listing, which is
// what pinning wants and is exactly wrong here: Drive lists the current
// revision first, and refuses to delete the one a file is at now. The
// first destructive run asked for that one twice and got two refusals it
// had counted as failures.
func (d *destroyRun) olderRevision(id string) string {
	if id == "" {
		return ""
	}
	out, isError, err := d.sess.CallTool("list_revisions", map[string]any{"file": id})
	if err != nil || isError {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && looksLikeDate(fields[1]) && !strings.Contains(line, "current") {
			return fields[0]
		}
	}
	return ""
}

// deleteAComment removes a thread, which is the one destructive tool
// that could always have run in a scratch folder and never did, because
// it shares its gate with the other four.
func (d *destroyRun) deleteAComment() {
	id := d.makeFile("commented.txt", "worth discussing\n")
	if id == "" {
		fmt.Println("\n=== delete_comment: skipped, the file it needs was never created ===")
		return
	}
	comment := commentFromResult(d.call(call{tool: "add_comment", args: map[string]any{
		"file": id, "content": "this thread is about to be removed",
	}}))
	if comment == "" {
		fmt.Println("\n=== delete_comment: skipped, the comment it needs was never made ===")
		return
	}
	d.call(call{tool: "delete_comment", args: map[string]any{"file": id, "comment": comment},
		expectError: true, why: "removing a comment without confirm: true"})
	d.call(call{tool: "delete_comment", args: map[string]any{
		"file": id, "comment": comment, "confirm": true,
	}})
	// Drive keeps the thread with its words removed, which is worth
	// seeing rather than asserting: the listing is what a person would
	// look at afterwards.
	d.call(call{tool: "list_comments", args: map[string]any{"file": id, "include_deleted": true}})
}

// approvalLock reaches the two states §17a said needed a drive that can
// be deleted whole: a file locked while an approval is open, and a file
// approved, which locks it again with no way to reopen it.
//
// The driver has never approved one before. It cancelled instead, and
// the reason was cleanup: an approved file is locked and a scratch
// folder cannot be trashed around it. Here the whole drive goes at the
// end, so the lock has somewhere to live.
func (d *destroyRun) approvalLock() {
	me := d.accountAddress()
	if me == "" {
		fmt.Println("\n=== approval lock: skipped, this run could not read its own address ===")
		return
	}
	// A blob, not a Google Doc. The promise being checked is that the
	// file's CONTENT cannot be changed, and this server never writes a
	// Doc's content at all — it refuses that for its own reasons, which
	// would look exactly like the lock working.
	id := d.makeFile("locked.txt", "before the approval\n")
	if id == "" {
		fmt.Println("\n=== approval lock: skipped, the file it needs was never created ===")
		return
	}

	// lock_file on an OPEN approval, first and on its own, so whatever
	// it does can be told apart from what approving does.
	locked := approvalFromResult(d.call(call{tool: "manage_approval", args: map[string]any{
		"file": id, "action": "start", "reviewers": []string{me},
		"message": "a scratch approval that locks the file", "lock_file": true,
	}}))
	fmt.Println("\n(the card below is the whole question: manage_approval has just said the file " +
		"is LOCKED, so a content restriction should be on it)")
	d.call(call{tool: "get_file", args: map[string]any{"file": id}})
	// NOT expected to be refused, which is the finding: two runs agree
	// that lock_file at the start applies no content restriction, and
	// the content change goes through. Left as a plain call so that a
	// Drive which starts locking shows up here as an unexpected refusal
	// rather than as silence.
	fmt.Println("\n(this content change is expected to SUCCEED: lock_file applied no restriction)")
	d.call(call{tool: "update_content", args: map[string]any{
		"file": id, "content": "changed while locked\n",
	}})
	// A rename is not a content change, so this is an observation rather
	// than an expectation: the reference says a content restriction stops
	// the title too, and whether lock_file sets one is what this is for.
	d.call(call{tool: "update_file", args: map[string]any{
		"file": id, "name": "renamed while locked.txt",
	}})
	if locked == "" {
		fmt.Println("\n=== approval lock: the approval id was not readable, so it cannot be approved ===")
		return
	}

	// And the approved state, which has never run: approving completes
	// the approval and locks the file for good, with no way to reopen it.
	d.call(call{tool: "manage_approval", args: map[string]any{
		"file": id, "action": "approve", "approval": locked,
	}})
	d.call(call{tool: "list_approvals", args: map[string]any{"file": id}})
	d.call(call{tool: "get_file", args: map[string]any{"file": id}})
	d.call(call{tool: "update_content", args: map[string]any{
		"file": id, "content": "changed after approval\n",
	}, expectError: true, why: "an approved file is locked"})

	// The reference says a content restriction stops content, comments
	// and renames, and says nothing about removal. If that is wrong this
	// is where the run finds out, and the drive would not delete below.
	d.destroyFile(id)
}

// emptyTheDriveTrash puts something in the scratch drive's trash and
// then empties it, which is the only way to see that the call did
// anything: it is the one destructive tool that does not name what it
// removed.
func (d *destroyRun) emptyTheDriveTrash() {
	if d.driveID == "" {
		return
	}
	id := d.makeFile("trash me.txt", "into the trash\n")
	d.needing("trash_file", id, map[string]any{"file": id})
	d.emptyTrash(call{args: map[string]any{"dry_run": true, "confirm": true}})
	d.emptyTrash(call{args: map[string]any{"confirm": true}})
	// Whether the file survived is the whole question — files.emptyTrash
	// has no response, so a look is the only evidence there is. This
	// asks rather than expects, because a live run found the answer is
	// "sometimes": Drive's view of its own trash lags, and a file
	// trashed seconds earlier came back.
	//
	// It must NOT be restore_file. Restoring un-trashes the file, and an
	// untrashed file is exactly what stops the drive being deleted at
	// the end — the first run of this checked with a restore and
	// stranded a shared drive in a real Workspace doing it.
	d.needing("get_file", id, map[string]any{"file": id})
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
	// with revisions. The first two runs of this stranded a shared drive
	// in a real Workspace precisely because cleanup assumed they had
	// tidied up after themselves.
	for _, id := range slices.Clone(d.living) {
		d.destroyFile(id)
	}
	d.emptyTrash(call{args: map[string]any{"confirm": true}})
	out := d.call(call{tool: "delete_drive", args: map[string]any{
		"drive": d.driveID, "confirm": true,
	}})
	if strings.Contains(out, "gone for good") {
		return
	}
	// One retry: the usual reason is Drive not yet agreeing the trash is
	// empty, which is the same eventual consistency the changes feed and
	// the property index both needed patience for.
	again := d.call(call{tool: "delete_drive", args: map[string]any{
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
// both are recognisable as this driver's.
func driveScratchName(stamp string) string {
	return fmt.Sprintf("%s drive %s", scratchPrefix, stamp)
}
