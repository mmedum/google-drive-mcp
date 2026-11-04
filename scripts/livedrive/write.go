package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/v3/internal/service"
	"github.com/mmedum/google-drive-mcp/v3/scripts/internal/mcpstdio"
	"github.com/mmedum/google-drive-mcp/v3/scripts/internal/transcript"
)

// writeRun exercises every tool that changes Drive, inside one scratch
// folder it makes and trashes again. Nothing outside that folder is
// touched: the folder is created first, every call names it or something
// inside it, and the last call trashes it whole.
//
// It is opt-in (-write) because it is the only part of this driver that
// changes anything in a real account.
type writeRun struct {
	// sess is the server, behind the check for a stop signal: every call
	// this run sends goes through it.
	sess session
	// out is the only way this run prints anything; see the transcript
	// package, and `gates transcript`, which is what makes that true.
	out *transcript.Transcript
	// dir is the local directory the server was told to use, and the one
	// place uploads come from and downloads land in.
	dir string
	// scratchID is the folder everything happens in, and scratchName
	// its name.
	scratchID, scratchName string
	// drive is the shared drive to move a file through, or empty.
	drive string
	// share is an address to grant access to, or empty. It is the half
	// of the sharing surface that needs a second person. The grant is
	// taken back and read back before the run ends.
	share string
	// transfer is the address the ownership transfer hands a file to for
	// good, or empty, when the transfer runs only as a dry run and its
	// refusals.
	// It is a flag of its own rather than share's address: a grant is
	// taken back, and an owner is not.
	transfer string
	// blocked is an address the organization's own sharing policy
	// refuses, or empty. It needs a Workspace administrator to have put
	// it out of bounds, so it is the only way a run sees a policy refusal.
	blocked string
	// labels and activity say whether the server was started with those
	// features on, which is also what decides whether their tools are
	// registered at all.
	labels   bool
	activity bool
	// destructive says whether the server was started with
	// GDRIVE_ENABLE_DESTRUCTIVE, which is what registers the shares that
	// reach past the organization.
	destructive bool
	// started is when this run began, in the form Drive's date filters
	// take. Everything the run makes is created after it, which is what
	// makes a date filter checkable rather than merely accepted.
	started string
	// dueDate is a date in the future, which is the only kind an
	// approval accepts.
	dueDate string
	// failures counts calls that did not behave as expected.
	failures int
	// pause waits for Drive to catch up; time.Sleep, but for a test.
	pause func(time.Duration)
	// stop is where a stop signal arrives. cleaning counts the cleanups
	// under way, which a stop does not interrupt; stopNoticed is set once
	// the transcript says the run stopped, and unwinding once the stop
	// has begun unwinding it.
	stop                   *stopper
	cleaning               int
	stopNoticed, unwinding bool
}

// session is what a write run needs of the server: *mcpstdio.Session,
// or a stand-in in a test.
type session interface {
	CallTool(name string, args map[string]any) (text string, isError bool, err error)
	ReadResource(uri string) (text, mime string, err error)
	StderrTail(n int) []string
}

// recordIt is what the transcript asks of a reader when a run settles a
// belief about Drive: the finding belongs beside the code it bears on.
const recordIt = "Record it in the comment at the code that depends on it."

// scratchPrefix names the folder this driver works in, so a folder left
// behind by an interrupted run is recognizable and safe to remove.
const scratchPrefix = "google-drive-mcp livedrive scratch"

// runWrites drives the whole write surface and reports how many calls
// behaved unexpectedly.
//
// A stop signal ends the steps early. The cleanups still run: those the
// steps deferred as they unwind, and the trash of the scratch folder
// here.
func runWrites(s session, t *transcript.Transcript, dir string, o options) (int, error) {
	now := time.Now().UTC()
	w := newWriteRun(s, t, dir, o, now)
	stamp := now.Format("2006-01-02 15:04:05")
	w.scratchName = fmt.Sprintf("%s %s", scratchPrefix, stamp)
	args := map[string]any{"name": w.scratchName}
	if o.parent != "" {
		args["parent"] = o.parent
	}
	var err error
	w.untilStopped(func() {
		w.scratchID = w.createAndKeepID(call{name: "the scratch folder is created", tool: "create_folder", args: args})
		if w.scratchID == "" {
			err = fmt.Errorf("the scratch folder could not be created, so nothing else can run safely")
			return
		}
		w.out.Say("\n(everything below happens inside that folder, and it is trashed at the end)")

		w.exercise()
		if o.destructive {
			runDestructive(w, driveScratchName(stamp))
		} else {
			w.out.Say("\n(pass -destructive to also exercise the five tools that remove things for " +
				"good, in a shared drive this run creates and destroys again, and the shares that reach past " +
				"the organization)")
		}
	})
	w.cleanup(w.trashScratch)
	return w.failures, err
}

// newWriteRun is a write run that has made nothing yet.
func newWriteRun(s session, t *transcript.Transcript, dir string, o options, now time.Time) *writeRun {
	w := &writeRun{out: t, dir: dir, drive: o.drive, share: o.share, transfer: o.transfer, blocked: o.blocked,
		labels: o.labels, activity: o.activity, destructive: o.destructive, pause: o.pause, stop: o.stop,
		started: now.Format(time.RFC3339), dueDate: now.AddDate(0, 0, 7).Format(time.RFC3339)}
	if w.pause == nil {
		w.pause = time.Sleep
	}
	w.sess = stoppable{session: s, w: w}
	return w
}

// exercise runs the whole write surface. A call that fails is counted
// and the run carries on: a live run costs a person's attention and a
// real account, so one of them should surface every problem there is,
// not the first. Only the calls that need an id a failed create never
// produced are skipped, and the skip says so.
func (w *writeRun) exercise() {
	ids := w.create()
	w.readBack(ids)
	w.officeFiles()
	w.ocr()
	w.organize(ids)
	w.markdownFile()
	w.underFolder()
	w.downloads(ids)
	w.limitedAccess()
	w.moveExposure()
	w.access(ids)
	w.ownershipTransfer()
	w.history(ids)
	w.collaboration(ids)
	w.extraParameters(ids)
	w.approvals(ids)
	w.labeling(ids)
	w.activityFeed(ids)
	w.policyRefusal(ids)
	w.resources(ids)
	w.refusals(ids)
	w.spareArguments(ids)
	w.sharedDrive(ids)
}

// extraParameters covers the three Drive parameters nothing else here
// reaches: bringing a file's comments along on a copy, marking a file as
// opened, and finding a file by a custom property.
//
// The property search runs LAST of the three deliberately: it looks for
// the key update_file set earlier in this run, so it is checking that
// what this server wrote is what Drive indexed, not merely that a query
// parses. An empty answer after the wait is reported as unverified
// rather than counted as a failure: nothing here knows how slow Drive's
// index can be.
func (w *writeRun) extraParameters(m made) {
	w.out.Say("\n--- copy_comments, viewed and a property search ---")
	// One kind of each, because the answer differs by kind: an uploaded
	// CSV's threads do not come across, and a Google Doc's, anchored to a
	// passage rather than to the file, do.
	w.copyAndCheckComments(m.text, "rows with its comments.csv", "a csv", false)
	w.copyAndCheckComments(m.doc, "Notes with its comments", "a Google Doc", true)
	// A Sheet is Drive's own format and not a Doc, so it is what holds
	// the rule to "Drive's own formats yes, uploaded bytes no" rather
	// than to "Docs yes".
	w.copyAndCheckComments(m.sheet, "Figures with its comments", "a Google Sheet", true)
	if m.text != "" {
		w.needing(m.text, call{name: "a file is marked as viewed",
			tool: "update_file", args: map[string]any{"file": m.text, "viewed": true}})
	}

	// A property set HERE, on purpose. The earlier one is deleted before
	// this step runs, so searching for it could only ever answer nothing
	// — a check whose pass and fail look identical, which is no check.
	if m.text != "" {
		w.needing(m.text, call{name: "a file is tagged with a property to search for",
			tool: "update_file", args: map[string]any{
				"file": m.text, "properties": map[string]any{"livedrive_found": "yes"},
			}})
		w.pollSearch(call{name: "a property search finds the file this run tagged",
			tool: "search_files", args: map[string]any{"property": "livedrive_found=yes"}}, m.text)
	}
	w.needing(w.scratchID, call{name: "a property clause with no key is refused",
		tool: "search_files", args: map[string]any{"property": "=nothing"},
		why: "a property clause with no key"})
	// The form Google's own guide gives as an example and Drive refuses.
	// It is checked here so that a day when Drive starts accepting it
	// shows up as this server being needlessly strict, rather than never
	// showing up at all.
	w.needing(w.scratchID, call{name: "a property key with no value is refused",
		tool: "search_files", args: map[string]any{"property": "livedrive"},
		why: "a key with no value, which Drive answers Invalid Value despite its guide"})
}

// pollSearch searches everywhere under the scratch folder until the
// search finds the file, and reports whether it did; the search's name
// says what finding it shows. Every attempt reads every page, since such
// a search can match more files than one page holds.
//
// Google does not say how soon a property or sharing change reaches the
// search index. Live, the first search after each change finds the file,
// but one run is not a bound, so an empty answer is waited out and then
// reported as unverified rather than as a defect. Checked by the live
// step "a property search finds the file this run tagged".
// https://developers.google.com/workspace/drive/api/guides/ref-search-terms
//
// It searches under the scratch folder, not in it: the file is moved
// into a folder inside it early on, so a search of the scratch folder's
// direct children finds nothing at any speed.
func (w *writeRun) pollSearch(search call, id string) bool {
	search.args["under_folder"] = w.scratchID
	const attempts = 10
	for i := range attempts {
		if i > 0 {
			w.pause(3 * time.Second)
		}
		if w.searchAllPages(search, id) {
			return true
		}
		w.out.Sayf("(the search has not found it yet; waiting — attempt %d of %d)", i+1, attempts)
	}
	w.out.Sayf("UNVERIFIED THIS RUN: %s. The search did not find the file after 30 seconds, under the "+
		"scratch folder and on every page. Drive's index is eventually consistent, so run the same search "+
		"by hand a few minutes from now.", search.name)
	return false
}

// searchAllPages runs a search and follows its page tokens to the end,
// and reports whether any page names id.
func (w *writeRun) searchAllPages(search call, id string) bool {
	page := search
	for range 20 {
		out := w.call(page)
		if strings.Contains(out, id) {
			return true
		}
		token := tokenIn(out)
		if token == "" {
			return false
		}
		page.args = maps.Clone(search.args)
		page.args["page_token"] = token
	}
	w.out.Say("(stopped after 20 pages)")
	return false
}

// readersHoldWriters settles the belief shared_with is built around: the
// reference does not say whether readers holds the people who can edit.
// It asks readers alone for a file the -share address can edit, after
// shared_with has shown the index knows the grant.
func (w *writeRun) readersHoldWriters(id string) {
	if w.searchAllPages(call{name: "a readers query looks for a file the address can edit",
		tool: "search_files", args: map[string]any{
			"raw_query": "'" + w.share + "' in readers", "under_folder": w.scratchID,
		}}, id) {
		w.out.Say("(readers alone found a file the address can edit: readers holds the writers. " + recordIt + ")")
		return
	}
	w.out.Say("(readers alone did NOT find a file the address can edit: readers does not hold the writers. " +
		"shared_with asks both, so it is right either way. " + recordIt + ")")
}

// approvals exercises the approval tools, and stops short of one verb
// on purpose.
//
// It starts an approval, reads it back, comments on it and CANCELS it.
// It never approves one: the default file-content-change behavior is
// RESET_APPROVAL, and under that an approved file is LOCKED — which
// would leave the scratch folder holding something this driver cannot
// clean up, and cleaning up after itself is the whole contract of the
// -write mode. Canceling leaves nothing locked.
//
// lock_file is not used here either, for the same reason. approvalLock
// does both, inside a shared drive the destructive run deletes whole.
//
// Every verb here mails somebody. The reviewer is the signed-in account,
// so the only mail this sends is to the person running it.
func (w *writeRun) approvals(m made) {
	if m.doc == "" {
		w.out.Say("\n(no document was created, so the approval surface is skipped)")
		return
	}
	w.out.Say("\n--- approvals ---")
	w.needing(m.doc, call{name: "a document's approvals are listed",
		tool: "list_approvals", args: map[string]any{"file": m.doc}})

	account := w.accountAddress()
	if account == "" {
		w.problem("get_account did not report an address, so there is nobody to review the approval",
			errors.New("no address"))
		return
	}
	started := w.call(call{name: "an approval is started with a future due date",
		tool: "manage_approval", args: map[string]any{
			"file": m.doc, "action": "start", "reviewers": []any{account},
			"message": "a scratch approval from the live driver",
			// A due date has to be in the FUTURE: Drive answers "Invalid
			// value at field dueTime" to one already past, which this run's
			// own start time is by the time the call is made.
			"due": w.dueDate,
		}})
	id := approvalFromResult(started)
	if id == "" {
		// Not necessarily a defect: approvals are a Workspace feature and
		// not every edition has them. What matters is that the refusal
		// said so rather than looking like a bug.
		w.out.Say("(no approval id came back — read the refusal above: an edition without approvals " +
			"is a legitimate answer, a confusing error is not)")
		return
	}
	w.needing(m.doc, call{name: "the started approval is listed",
		tool: "list_approvals", args: map[string]any{"file": m.doc}})
	w.needing(m.doc, call{name: "a comment is added to an approval", tool: "manage_approval", args: map[string]any{
		"file": m.doc, "action": "comment", "approval": id, "message": "still looking",
	}})
	w.needing(m.doc, call{name: "an approval comment with nothing to say is refused",
		tool: "manage_approval", args: map[string]any{
			"file": m.doc, "action": "comment", "approval": id,
		}, why: "a comment with nothing to say"})
	w.needing(m.doc, call{name: "an approval is canceled", tool: "manage_approval", args: map[string]any{
		"file": m.doc, "action": "cancel", "approval": id,
	}})
	w.refusing(m.doc, call{name: "manage_approval refuses to approve and names approve_file",
		tool: "manage_approval", args: map[string]any{
			"file": m.doc, "action": "approve", "approval": id,
		}, why: "approve_file does"})
	w.needing(m.doc, call{name: "answering a finished approval is refused", tool: "approve_file", args: map[string]any{
		"file": m.doc, "approval": id, "message": "too late",
	}, why: "answering an approval that is already finished"})
}

// labeling exercises the two label tools. They are registered only with
// GDRIVE_LABELS=true, and even then the account may have no label
// published to it, which is not a failure.
func (w *writeRun) labeling(m made) {
	if !w.labels {
		w.out.Say("\n(pass -labels to exercise the label tools, which need GDRIVE_LABELS and its scopes)")
		return
	}
	w.out.Say("\n--- labels ---")
	w.needing(w.scratchID, call{name: "the labels that can be applied are listed",
		tool: "list_labels", args: map[string]any{"appliable": true, "page_size": 10}})
	w.needing(m.text, call{name: "a card is read with its labels",
		tool: "get_file", args: map[string]any{"file": m.text, "include_labels": true}})
	listing := w.call(call{name: "every label published to the account is listed",
		tool: "list_labels", args: map[string]any{}})
	id, field, choice := labelFromResult(listing)
	if id == "" {
		w.out.Say("(no label is published to this account, so there is nothing to apply — " +
			"the listing above should say so in as many words)")
		return
	}
	if m.doc == "" {
		return
	}
	w.needing(m.doc, call{name: "a label is applied to a document",
		tool: "manage_labels", args: map[string]any{"file": m.doc, "label": id, "action": "apply"}})
	if field != "" && choice != "" {
		w.needing(m.doc, call{name: "a label field is set to one of its choices", tool: "manage_labels", args: map[string]any{
			"file": m.doc, "label": id, "action": "set_field", "field": field, "values": []any{choice},
		}})
		w.needing(m.doc, call{name: "a label field value nobody defined is refused",
			tool: "manage_labels", args: map[string]any{
				"file": m.doc, "label": id, "action": "set_field", "field": field,
				"values": []any{"not-a-choice-anybody-defined"},
			}, why: "a value the field's definition does not offer"})
		w.needing(m.doc, call{name: "a label field is unset", tool: "manage_labels", args: map[string]any{
			"file": m.doc, "label": id, "action": "unset_field", "field": field,
		}})
	}
	// The card is where a label is normally read, so the round trip is
	// what proves the two halves agree.
	w.needing(m.doc, call{name: "the document's card shows its label",
		tool: "get_file", args: map[string]any{"file": m.doc}})
	w.needing(m.doc, call{name: "a label is removed from a document",
		tool: "manage_labels", args: map[string]any{"file": m.doc, "label": id, "action": "remove"}})
	w.needing(m.doc, call{name: "a label id that names nothing is refused", tool: "manage_labels", args: map[string]any{
		"file": m.doc, "label": "a-label-that-was-never-published", "action": "apply",
	}, why: "a label id that names nothing"})
}

// activityFeed exercises list_activity. Drive's feed is eventually
// consistent, so an empty answer moments after a write is a real
// possibility rather than a defect, as it is for the changes feed.
func (w *writeRun) activityFeed(m made) {
	if !w.activity {
		w.out.Say("\n(pass -activity to exercise list_activity, which needs GDRIVE_ACTIVITY and its scope)")
		return
	}
	w.out.Say("\n--- activity ---")
	w.needing(w.scratchID, call{name: "the scratch folder's activity is read recursively",
		tool: "list_activity", args: map[string]any{"file": w.scratchID, "recursive": true}})
	feed := w.call(call{name: "the activity feed pages from the start of the run",
		tool: "list_activity", args: map[string]any{
			"file": w.scratchID, "recursive": true, "page_size": 1, "since": w.started,
		}})
	if token := tokenIn(feed); token != "" {
		w.needing(w.scratchID, call{name: "the activity feed's second page is read",
			tool: "list_activity", args: map[string]any{
				"file": w.scratchID, "recursive": true, "page_size": 1, "page_token": token,
			}})
	} else {
		w.unverified("list_activity.page_token was not exercised: the feed returned no token",
			errors.New("the activity feed is eventually consistent and may still be empty"))
	}
	if m.doc != "" {
		w.needing(m.doc, call{name: "a document's activity is read",
			tool: "list_activity", args: map[string]any{"file": m.doc}})
		w.needing(m.doc, call{name: "a document's activity is filtered by action",
			tool: "list_activity", args: map[string]any{"file": m.doc, "actions": []any{"create", "edit"}}})
	}
	w.needing(m.doc, call{name: "an activity action nobody implemented is refused",
		tool: "list_activity", args: map[string]any{"file": m.doc, "actions": []any{"eaten"}},
		why: "an action filter nobody implemented"})
	w.needing(m.doc, call{name: "a recursive activity read of a file is refused",
		tool: "list_activity", args: map[string]any{"file": m.doc, "recursive": true},
		why: "recursive on a file, where the API's ancestorName means nothing"})
	w.out.Say("(an empty feed moments after a write is Drive being eventually consistent, not a defect)")
}

// collaboration exercises comments, the access requests nobody here can
// create, and a recursive copy.
//
// The comment surface is the one part of this driver that leaves marks
// other people can see — everybody the file is shared with sees a
// comment — but every file it touches is inside the scratch folder, and
// the folder is trashed at the end.
func (w *writeRun) collaboration(m made) {
	// A thread on a blob and a thread on a Google document: Drive stores
	// them the same way, and that is the claim worth checking live,
	// because every other server puts comments in the Docs API.
	for _, target := range []struct{ id, what string }{
		{m.text, "a csv"}, {m.doc, "a Google Doc"}, {m.sheet, "a Google Sheet"},
	} {
		if target.id == "" {
			continue
		}
		w.out.Sayf("\n--- comments on %s ---", target.what)
		w.needing(target.id, call{name: "a file's comment threads are listed",
			tool: "list_comments", args: map[string]any{"file": target.id}})
		id := commentFromResult(w.call(call{name: "a comment thread is added to a file",
			tool: "add_comment", args: map[string]any{
				"file": target.id, "content": "is this row still right?",
			}}))
		if id == "" {
			w.problem("no comment id came back from add_comment, so the rest of the thread cannot run",
				errors.New("the result carried no comment id"))
			continue
		}
		w.needing(target.id, call{name: "a reply is added to a thread", tool: "reply_comment", args: map[string]any{
			"file": target.id, "comment": id, "content": "checked, it is",
		}})
		w.needing(target.id, call{name: "a thread is resolved", tool: "reply_comment", args: map[string]any{
			"file": target.id, "comment": id, "action": "resolve",
		}})
		// The same resolve again: it must report that nothing changed
		// rather than adding a second reply everybody can see.
		w.needing(target.id, call{name: "resolving a resolved thread changes nothing",
			tool: "reply_comment", args: map[string]any{
				"file": target.id, "comment": id, "action": "resolve",
			}})
		w.needing(target.id, call{name: "a resolved thread is reopened", tool: "reply_comment", args: map[string]any{
			"file": target.id, "comment": id, "action": "reopen",
		}})
		w.needing(target.id, call{name: "a thread's first comment is edited", tool: "reply_comment", args: map[string]any{
			"file": target.id, "comment": id, "action": "edit",
			"content": "is this row still right? (edited)",
		}})
		listing := w.call(call{name: "a file's threads are listed with their reply ids", tool: "list_comments",
			args: map[string]any{"file": target.id, "include_deleted": true}})
		// A reply's id is only ever in a listing — it opens no card and
		// the renderer prints it indented under its thread — so the reply
		// to edit is read from there.
		if replies := mcpstdio.ReplyIDs(listing); len(replies) > 0 {
			w.needing(target.id, call{name: "one reply is edited by its id", tool: "reply_comment", args: map[string]any{
				"file": target.id, "comment": id, "reply": replies[0], "action": "edit",
				"content": "checked, it is (edited)",
			}})
		} else {
			w.unverified("reply_comment.reply was not exercised: the listing showed no reply id",
				errors.New("the thread this run made has no reply, or the renderer has changed shape"))
		}
		w.needing(target.id, call{name: "a thread action nobody implemented is refused",
			tool: "reply_comment", args: map[string]any{
				"file": target.id, "comment": id, "action": "close", "content": "x",
			}, why: "an action nobody implemented"})
		w.needing(target.id, call{name: "a comment id that names nothing is refused",
			tool: "reply_comment", args: map[string]any{
				"file": target.id, "comment": "a-comment-that-was-never-made", "content": "x",
			}, why: "a comment id that names nothing"})
	}
	w.needing(w.scratchID, call{name: "a folder's comments are refused",
		tool: "list_comments", args: map[string]any{"file": w.scratchID},
		why: "a folder, which Drive has no comments on"})
	w.needing(m.text, call{name: "a comment with nothing in it is refused",
		tool: "add_comment", args: map[string]any{"file": m.text},
		why: "a comment with nothing in it"})

	// Access requests: nobody here can make one, so what this checks is
	// that the listing works, and that the account is told when it is
	// not an approver.
	w.needing(m.text, call{name: "a file's access requests are listed",
		tool: "list_access_requests", args: map[string]any{"file": m.text}})
	w.needing(m.text, call{name: "an access request that is not pending is refused",
		tool: "resolve_access_request", args: map[string]any{
			"file": m.text, "request": "a-request-nobody-made", "action": "accept",
		}, why: "an access request that is not pending"})

	// The recursive copy, which is the write with the most steps: a dry
	// run first, then the copy, then a read-back of what it made.
	if m.folder == "" {
		return
	}
	w.needing(m.folder, call{name: "a dry run of a recursive folder copy says what would be copied",
		tool: "copy_file", args: map[string]any{
			"file": m.folder, "to": w.scratchID, "name": "Reports copy", "recursive": true, "dry_run": true,
		}})
	w.needing(m.folder, call{name: "a folder is copied recursively", tool: "copy_file", args: map[string]any{
		"file": m.folder, "to": w.scratchID, "name": "Reports copy", "recursive": true,
	}})
	w.needing(w.scratchID, call{name: "a recursive copy is read back as a tree", tool: "list_folder", args: map[string]any{
		"folder": w.scratchID, "recursive": true, "max_depth": 4,
	}})
	w.needing(m.folder, call{name: "a folder copy without recursive is refused",
		tool: "copy_file", args: map[string]any{"file": m.folder, "to": w.scratchID},
		why: "a folder copied without recursive"})
	w.needing(m.folder, call{name: "a folder copy into itself is refused", tool: "copy_file", args: map[string]any{
		"file": m.folder, "to": m.folder, "recursive": true,
	}, why: "a folder copied into itself"})
	w.needing(m.folder, call{name: "a copy budget above the ceiling is refused", tool: "copy_file", args: map[string]any{
		"file": m.folder, "to": w.scratchID, "recursive": true, "max_items": 9000,
	}, why: "a budget above the ceiling"})
}

// commentFromResult reads the comment id out of add_comment's note,
// which says "comment <id> added to <file>". The id is read before
// redaction and never printed.
func commentFromResult(out string) string {
	m := commentIDInNote.FindStringSubmatch(out)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

var commentIDInNote = regexp.MustCompile(`comment (\S+) added to `)

// approvalFromResult pulls the new approval's id out of the note that
// manage_approval start writes. It is the only place the id appears:
// there is no listing to find it in before the approval exists.
func approvalFromResult(out string) string {
	m := approvalIDInNote.FindStringSubmatch(out)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

var approvalIDInNote = regexp.MustCompile(`Approval (\S+) started on `)

// labelFromResult picks the first label out of a list_labels result,
// with a field and one of its choices where there is one. The driver
// cannot know what an organization has published, so it works with
// whatever the account actually has.
func labelFromResult(out string) (label, field, choice string) {
	if m := labelInListing.FindStringSubmatch(out); len(m) > 1 {
		label = m[1]
	}
	if m := fieldInListing.FindStringSubmatch(out); len(m) > 1 {
		field = m[1]
	}
	if m := choiceInListing.FindStringSubmatch(out); len(m) > 1 {
		choice = m[1]
	}
	if field == "" {
		// A choice without the field it belongs to is no use.
		choice = ""
	}
	return label, field, choice
}

var (
	labelInListing  = regexp.MustCompile(`(?m)^\S.* — (\S+)$`)
	fieldInListing  = regexp.MustCompile(`(?m)^\s*field (\S+):`)
	choiceInListing = regexp.MustCompile(`(?m)^\s*choices:\s*(\S+?)(?:\s|\(|,|$)`)
)

// accountAddress reads the signed-in address out of get_account, which
// is what an approval needs for its reviewer. The driver reviews with
// the account running it, so the only mail a run sends goes to whoever
// started it.
func (w *writeRun) accountAddress() string {
	out, _, err := w.quiet(call{name: "the signed-in address is read for a reviewer",
		tool: "get_account", args: map[string]any{}})
	if err != nil {
		return ""
	}
	return addressInAccount.FindString(out)
}

// The hyphen is last in the class so it is a hyphen. Written as `+-…` it
// is a range that takes in `<`, and the reviewer sent to Drive keeps the
// angle bracket of "Name <address>".
var addressInAccount = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

// access exercises the sharing surface. Everything happens on files
// inside the scratch folder, and every grant made here is removed again
// before the folder is trashed: a share that outlived the run would be
// the one thing trashing the folder does not undo.
//
// share_file takes named people and groups inside the organization, and
// refuses every wider grant, naming the tool that makes it. Those tools
// exist only with -destructive. -share NAME@EXAMPLE.COM adds the
// half that needs a second person: a real grant to a real address, taken
// back and read back. The ownership transfer is ownershipTransfer's, and
// never goes to this address.
func (w *writeRun) access(m made) {
	w.needing(m.text, call{name: "a file's permissions are listed",
		tool: "list_permissions", args: map[string]any{"file": m.text}})

	// The wide grants share_file refuses, each naming its own tool.
	w.refusing(m.text, call{name: "share_file refuses anyone and names share_by_link",
		tool: "share_file", args: map[string]any{
			"file": m.text, "principal": "anyone", "role": "reader",
		}, why: "share_by_link does it"})
	w.refusing(m.text, call{name: "share_file refuses a domain and names share_by_link",
		tool: "share_file", args: map[string]any{
			"file": m.text, "principal": "domain:example.com", "role": "reader",
		}, why: "share_by_link does it"})
	w.refusing(m.text, call{name: "share_file refuses an outside address and names share_outside_organization",
		tool: "share_file", args: map[string]any{
			"file": m.text, "principal": "someone@example.org", "role": "reader",
		}, why: "share_outside_organization does it"})
	w.refusing(m.text, call{name: "share_file refuses an owner and names transfer_ownership",
		tool: "share_file", args: map[string]any{
			"file": m.text, "principal": "someone@example.org", "role": "owner",
		}, why: "transfer_ownership does it"})
	w.needing(m.text, call{name: "a principal that is not one is refused", tool: "share_file", args: map[string]any{
		"file": m.text, "principal": "not-an-address", "role": "reader",
	}, why: "a principal that is not one"})
	if domain := w.accountDomain(); domain != "" {
		// An address at the account's own domain is inside, so the role
		// is what is refused, before anything is written. On a personal
		// account the address is outside and refused for that instead.
		w.needing(m.text, call{name: "a shared-drive role on a My Drive file is refused",
			tool: "share_file", args: map[string]any{
				"file": m.text, "principal": "nobody-livedrive@" + domain, "role": "organizer",
			}, why: "a shared-drive role on a file in My Drive"})
	}

	if w.destructive {
		w.linkShare(m)
	} else {
		w.out.Say("\n(pass -destructive to also exercise share_by_link, share_outside_organization and " +
			"transfer_ownership, which only GDRIVE_ENABLE_DESTRUCTIVE registers)")
	}

	if w.share == "" {
		w.out.Say("\n(pass -share SOMEONE@EXAMPLE.COM to also exercise a real grant and an expiry)")
		return
	}
	tool := w.shareToolFor(m.text)
	if tool == "" {
		return
	}
	// Deferred before the grant, so a stop signal after it still takes it
	// back. In a run nobody stops, it runs where it always did: last.
	defer w.revoke(m.text, w.share)
	// Granting again to the same principal changes the role rather than
	// adding a second grant, which is Drive's one-per-principal rule.
	switch tool {
	case "share_file":
		w.needing(m.text, call{name: "the second person is granted read access with an expiry",
			tool: "share_file", args: map[string]any{
				"file": m.text, "principal": w.share, "role": "reader", "expires": "7d",
			}})
		w.needing(m.text, call{name: "a second grant to the same person changes the role",
			tool: "share_file", args: map[string]any{"file": m.text, "principal": w.share, "role": "writer"}})
	case "share_outside_organization":
		w.needing(m.text, call{name: "an outside second person is granted read access with an expiry",
			tool: "share_outside_organization", args: map[string]any{
				"file": m.text, "file_name": textRenamed, "principal": w.share, "role": "reader", "expires": "7d",
				"confirm": true,
			}})
		w.needing(m.text, call{name: "a second outside grant to the same person changes the role",
			tool: "share_outside_organization", args: map[string]any{
				"file": m.text, "file_name": textRenamed, "principal": w.share, "role": "writer", "confirm": true,
			}})
	}
	w.needing(m.text, call{name: "the second person's grant is listed",
		tool: "list_permissions", args: map[string]any{"file": m.text}})
	if w.pollSearch(call{name: "shared_with finds a file the address can edit",
		tool: "search_files", args: map[string]any{"shared_with": w.share}}, m.text) {
		w.readersHoldWriters(m.text)
	}
}

// revoke takes -share's grant off a file and reads the file's
// permissions back. A grant that outlives the run is the one thing
// trashing the scratch folder does not undo, and unshare_file answering
// is not proof it went: the listing is.
func (w *writeRun) revoke(file, principal string) {
	w.cleanup(func() {
		w.call(call{name: "the second person's grant is taken back",
			tool: "unshare_file", args: map[string]any{"file": file, "principal": principal}})
		if w.grantGone(file, principal) {
			return
		}
		// Once more after a pause. Tolerant, because a removal Drive had
		// taken and not yet listed is refused as having nothing to remove.
		w.pause(10 * time.Second)
		w.call(call{name: "the second person's grant is taken back again after a pause",
			tool: "unshare_file", args: map[string]any{"file": file, "principal": principal},
			tolerant: true})
		if w.grantGone(file, principal) {
			return
		}
		w.problem("A GRANT IS STILL THERE: "+principal+" can still reach a scratch file, and trashing the "+
			"scratch folder does not take that away. Remove it by hand, in Drive's share dialog or with "+
			"unshare_file; the file's id is in the results above",
			errors.New("list_permissions still names the address after two removals"))
	})
}

// grantGone reports whether the file's permissions were read and name
// principal nowhere. A listing that could not be read is not gone.
func (w *writeRun) grantGone(file, principal string) bool {
	out, isError := w.do(call{name: "the permissions no longer name the second person",
		tool: "list_permissions", args: map[string]any{"file": file}})
	return !isError && out != "" && !strings.Contains(out, "what is here is unknown") &&
		!strings.Contains(strings.ToLower(out), strings.ToLower(principal))
}

// shareToolFor is the tool that grants -share's address access: share_file
// when it is inside the organization, and share_outside_organization when
// share_file refuses it as outside, which needs -destructive. A dry run of
// share_file decides, so nothing is granted by asking. Empty when no tool
// this run has can grant it.
func (w *writeRun) shareToolFor(file string) string {
	if file == "" {
		return ""
	}
	out := w.call(call{name: "a dry-run grant decides which tool reaches the second person",
		tool: "share_file", args: map[string]any{
			"file": file, "principal": w.share, "role": "reader", "dry_run": true,
		}, tolerant: true})
	switch {
	case !strings.Contains(out, "share_outside_organization does it"):
		return "share_file"
	case w.destructive:
		w.out.Say("(the address is outside this account's organization, so share_outside_organization grants it)")
		return "share_outside_organization"
	}
	w.out.Say("(the address is outside this account's organization; pass -destructive to grant it with " +
		"share_outside_organization)")
	return ""
}

// accountDomain is the signed-in account's email domain, or empty.
func (w *writeRun) accountDomain() string {
	_, domain, _ := strings.Cut(w.accountAddress(), "@")
	return domain
}

// linkShare is the link grant, the exposure worth seeing before and
// after, then removed again; and a domain grant's dry run. Both are
// share_by_link's, which needs confirm.
func (w *writeRun) linkShare(m made) {
	w.refusing(m.text, call{name: "a link grant is refused without confirm", tool: "share_by_link", args: map[string]any{
		"file": m.text, "file_name": textRenamed, "principal": "anyone", "role": "reader",
	}, why: "confirm: true"})
	w.refusing(m.text, call{name: "share_by_link refuses an address", tool: "share_by_link", args: map[string]any{
		"file": m.text, "file_name": textRenamed, "principal": "someone@example.org", "role": "reader",
		"confirm": true,
	}, why: "not an address"})
	// The name it had before organize renamed it: the witness is read
	// from Drive, not from what this run once called the file.
	w.refusing(m.text, call{name: "a link grant is refused under the file's old name",
		tool: "share_by_link", args: map[string]any{
			"file": m.text, "file_name": "rows.csv", "principal": "anyone", "role": "reader", "confirm": true,
		}, why: "[stale]"})
	w.needing(m.text, call{name: "a dry run of a link grant says who would reach the file",
		tool: "share_by_link", args: map[string]any{
			"file": m.text, "file_name": textRenamed, "principal": "anyone", "role": "reader", "dry_run": true,
		}})
	w.needing(m.text, call{name: "a file is shared with anyone who has the link",
		tool: "share_by_link", args: map[string]any{
			"file": m.text, "file_name": textRenamed, "principal": "anyone", "role": "reader", "discoverable": false,
			"confirm": true,
		}})
	w.needing(m.text, call{name: "the link grant is listed",
		tool: "list_permissions", args: map[string]any{"file": m.text}})
	// The link grant is the one reach this run can give: visibility
	// link and anyone must find the file, and limited must not.
	if m.text != "" {
		w.pollSearch(call{name: "visibility link finds a file anyone with the link can open",
			tool: "search_files", args: map[string]any{"visibility": "link"}}, m.text)
		w.pollSearch(call{name: "visibility anyone finds the same file",
			tool: "search_files", args: map[string]any{"visibility": "anyone"}}, m.text)
	}
	w.needing(m.text, call{name: "a dry run of a domain grant says who would reach the file",
		tool: "share_by_link", args: map[string]any{
			"file": m.text, "file_name": textRenamed, "principal": "domain:example.com", "role": "reader", "dry_run": true,
		}})
	w.needing(m.text, call{name: "a dry run of removing a link says what would go",
		tool: "unshare_file", args: map[string]any{"file": m.text, "remove_link": true, "dry_run": true}})
	// By id rather than by principal, which is the path for a grant with
	// no address to name it by. Drive gives an anyone-with-the-link
	// permission the well-known id "anyoneWithLink", so this needs no
	// reader — and if Drive disagrees, the run says so, which is the
	// only way anybody here would find out.
	w.needing(m.text, call{name: "a link grant is found by its well-known permission id",
		tool: "unshare_file", args: map[string]any{
			"file": m.text, "permission_id": "anyoneWithLink", "dry_run": true,
		}})
	w.needing(m.text, call{name: "a link grant is removed",
		tool: "unshare_file", args: map[string]any{"file": m.text, "remove_link": true}})
	if m.text != "" {
		w.pollSearch(call{name: "visibility limited finds the file once its link is gone",
			tool: "search_files", args: map[string]any{"visibility": "limited"}}, m.text)
	}
	w.needing(m.text, call{name: "a dry run of an outside grant says who would reach the file",
		tool: "share_outside_organization", args: map[string]any{
			"file": m.text, "file_name": textRenamed, "principal": "someone@example.org", "role": "reader", "dry_run": true,
		}})
}

// transferStandIn is who the ownership transfer's dry run and refusals
// name without -transfer: an address at a domain reserved for examples
// (RFC 2606), so that not even a server that ignored dry_run could hand
// the file to a person.
const transferStandIn = "new-owner@example.org"

// ownershipTransfer hands a file to a new owner, the only call in this
// run whose effect cannot be undone from this account. Google also
// emails the new owner, whatever the call asks.
//
// So the real transfer needs a flag of its own, -transfer, which names
// the address. -share and -destructive do not make it: without
// -transfer the step is the refusals and a dry run, at a reserved
// address, and none of them carries confirm: true.
//
// It runs on a file created for it, never on one the rest of the run
// needs. Afterwards this account is a writer rather than the owner, and
// Drive moves the file to the new owner's root — so it leaves the
// scratch folder, and trashing that folder will not take it.
//
// The transfer is READ BACK. A call succeeding and a call telling the
// truth are different questions; for a transfer the second is the only
// one that matters, and the answer is in the owner line, not the status.
func (w *writeRun) ownershipTransfer() {
	// access has already said to pass -destructive for it.
	if !w.destructive {
		return
	}
	const name = "ownership transfer probe"
	transfer := w.createAndKeepID(call{name: "a file is made to hand to a new owner",
		tool: "create_file", args: map[string]any{
			"name": name, "parent": w.scratchID,
			"content": "this file is about to change hands\n", "mime_type": "text/plain",
		}})
	if transfer == "" {
		w.out.Say("\n=== ownership transfer: skipped, the file it needs was never created ===")
		return
	}
	to := transferStandIn
	if w.transfer != "" {
		to = w.transfer
	}
	before := ownerFromResult(w.call(call{name: "the owner is read before an ownership transfer",
		tool: "get_file", args: map[string]any{"file": transfer}}))

	w.refusing(transfer, call{name: "an ownership transfer is refused without confirm",
		tool: "transfer_ownership", args: map[string]any{
			"file": transfer, "file_name": name, "to": to,
		}, why: "confirm: true"})
	// Without confirm as well, so a server that skipped the name check
	// would still refuse it.
	w.refusing(transfer, call{name: "an ownership transfer is refused under a stale file name",
		tool: "transfer_ownership", args: map[string]any{
			"file": transfer, "file_name": name + " (another)", "to": to,
		}, why: "[stale]"})
	w.needing(transfer, call{name: "a dry run of an ownership transfer says who would own the file",
		tool: "transfer_ownership", args: map[string]any{
			"file": transfer, "file_name": name, "to": to, "dry_run": true,
		}})
	if w.transfer == "" {
		if after := ownerFromResult(w.call(call{name: "the owner stays put when no transfer was asked for",
			tool: "get_file", args: map[string]any{"file": transfer}})); after != before {
			w.problem("the owner changed although no transfer was asked for", errors.New("owner changed"))
		}
		w.out.Say("\n(pass -transfer ADDRESS as well, an account you control, to make the real transfer. " +
			"It cannot be undone, and Google emails that address)")
		return
	}
	w.out.Say("\n!! the next call hands this file to " + w.transfer + " for good, and Google emails them.")
	w.needing(transfer, call{name: "an ownership transfer hands the file to its new owner",
		tool: "transfer_ownership", args: map[string]any{
			"file": transfer, "file_name": name, "to": w.transfer, "message": "a scratch file from the live driver",
			"confirm": true,
		}})

	// The proof. A transfer that answers 200 and leaves the owner where
	// it was would look identical to one that worked, from the result
	// alone.
	after := ownerFromResult(w.call(call{name: "the owner is read back after an ownership transfer",
		tool: "get_file", args: map[string]any{"file": transfer}}))
	w.call(call{name: "the permissions are listed after an ownership transfer",
		tool: "list_permissions", args: map[string]any{"file": transfer}})

	switch after {
	case "":
		w.problem("the file could not be read back after the transfer, so whether ownership actually "+
			"moved is unknown. Open it in Drive and look", errors.New("no owner line in the result"))
	case before:
		w.problem("transfer_ownership reported an ownership transfer and the owner DID NOT CHANGE. That is "+
			"the failure reading it back exists to catch", errors.New("the owner is the same as before"))
	default:
		w.out.Say("\n(the owner changed, so the ownership transfer really happened. " +
			"This account is a writer on it now.)")
	}
	w.out.Say("!! that file now belongs to " + w.transfer + " and has moved to their Drive. Trashing the " +
		"scratch folder does not take it back: only its new owner can remove it.")
}

// ownerFromResult reads the "owner:" line of a file card, before
// redaction, so a transfer can be compared with what came before it. The
// value is never printed: only whether it changed.
func ownerFromResult(out string) string { return cardField(out, "owner") }

// cardField reads one "name: value" line of a file card, or "".
func cardField(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), name+": "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// history exercises the version history and the changes feed. The feed
// is the one part that reports on the whole account rather than the
// scratch folder, so it is read but never acted on.
func (w *writeRun) history(m made) {
	w.needing(m.text, call{name: "a csv's revisions are listed",
		tool: "list_revisions", args: map[string]any{"file": m.text}})
	w.needing(m.doc, call{name: "a Google Doc's revisions are listed",
		tool: "list_revisions", args: map[string]any{"file": m.doc}})
	w.needing(w.scratchID, call{name: "a folder's revisions are refused",
		tool: "list_revisions", args: map[string]any{"file": w.scratchID},
		why: "a folder has no content and so no versions"})

	// A pin, then the same call again, which must report that nothing
	// changed rather than a write that did not happen.
	if rev := w.firstRevision(m.text); rev != "" {
		w.needing(m.text, call{name: "a revision is kept forever", tool: "manage_revision", args: map[string]any{
			"file": m.text, "revision": rev, "action": "keep",
		}})
		w.needing(m.text, call{name: "keeping a kept revision changes nothing", tool: "manage_revision", args: map[string]any{
			"file": m.text, "revision": rev, "action": "keep",
		}})
		w.needing(m.text, call{name: "the current revision is unpinned", tool: "manage_revision", args: map[string]any{
			"file": m.text, "revision": rev, "action": "unkeep",
		}})
	}
	w.needing(m.text, call{name: "a revision id that names nothing is refused",
		tool: "manage_revision", args: map[string]any{
			"file": m.text, "revision": "a-revision-that-was-never-current", "action": "keep",
		}, why: "a revision id that names nothing"})
	// The revision keep_previous_revision pinned is no longer current,
	// and Drive refuses to unpin a kept revision unless it is the
	// current one.
	if m.text != "" {
		if old := w.pinnedOldRevision(m.text); old == "" {
			w.problem("no revision of the replaced file is kept forever, though keep_previous_revision pinned one",
				errors.New("pin not read back"))
		} else {
			w.needing(m.text, call{name: "a kept revision that is no longer current cannot be unpinned",
				tool: "manage_revision", args: map[string]any{
					"file": m.text, "revision": old, "action": "unkeep",
				}, why: "a revision kept forever that is no longer current cannot be unpinned"})
		}
	}

	// The feed: a starting point, then the changes this run has made
	// since it. Everything above happened before the token, so the
	// second call should be quiet — which is itself worth seeing.
	token := tokenFromResult(w.call(call{name: "the changes feed hands out a starting token",
		tool: "list_changes", args: map[string]any{}}))
	w.needing(m.text, call{name: "a file is unstarred for the changes feed to report",
		tool: "update_file", args: map[string]any{"file": m.text, "starred": false}})
	if token != "" {
		w.pollChanges(token)
	}
	w.call(call{name: "a page token from no feed is refused",
		tool: "list_changes", args: map[string]any{"page_token": "not-a-real-token"},
		why: "a page token from no feed at all"})
}

// pollChanges reads the feed until it reports the change this run just
// made, or gives up and says which happened.
//
// Drive's changes feed is eventually consistent, which the reference does
// not say: asked about a second after a write, it can answer "0 changes",
// and a working feed then looks exactly like a broken one. A single read
// is no evidence of absence, so this waits, and a run that gives up says
// so. Checked by the live step "the changes feed reports the write this
// run made".
// https://developers.google.com/workspace/drive/api/reference/rest/v3/changes/list
func (w *writeRun) pollChanges(token string) {
	const attempts = 6
	for i := range attempts {
		if i > 0 {
			w.pause(2 * time.Second)
		}
		out := w.call(call{name: "the changes feed reports the write this run made",
			tool: "list_changes", args: map[string]any{"page_token": token, "limit": 20}})
		if !strings.Contains(out, "0 changes") {
			return
		}
		w.out.Sayf("(the feed reports nothing yet; it is eventually consistent, waiting — attempt %d of %d)",
			i+1, attempts)
	}
	w.problem("the changes feed never reported the write this run made. That is either Drive's own lag "+
		"or a real fault, and this run cannot tell which: check it by hand before trusting list_changes",
		errors.New("the feed stayed empty for "+fmt.Sprint(attempts*2)+" seconds"))
}

// pinnedOldRevision is a revision of the file that is kept forever and
// is no longer current, or "".
func (w *writeRun) pinnedOldRevision(id string) string {
	out, isError, err := w.quiet(call{name: "a kept revision that is no longer current is found", tool: "list_revisions",
		args: map[string]any{"file": id}})
	if err != nil || isError {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !looksLikeDate(fields[1]) || strings.Contains(line, "current") ||
			!strings.Contains(line, "kept forever") {
			continue
		}
		return fields[0]
	}
	return ""
}

// firstRevision reads a revision id out of a list_revisions result, so
// the pin has something real to act on.
func (w *writeRun) firstRevision(id string) string {
	return w.revisionID(id, false)
}

// revisionID reads a revision id out of a list_revisions result.
//
// A row of that listing is "<id>  <date> ...", and the date is what
// identifies it: the lines around it are a subject line and prose, and
// neither has a second field beginning with a four-digit year.
//
// skipCurrent picks one that is NOT the version the file is at now,
// which is what deleting wants: Drive lists the current revision first
// and refuses to delete it. One parser rather than two, because the row
// format is the thing being read.
func (w *writeRun) revisionID(id string, skipCurrent bool) string {
	if id == "" {
		return ""
	}
	out, isError, err := w.quiet(call{name: "a revision id is read out of the listing", tool: "list_revisions",
		args: map[string]any{"file": id}})
	if err != nil || isError {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !looksLikeDate(fields[1]) {
			continue
		}
		if skipCurrent && strings.Contains(line, "current") {
			continue
		}
		return fields[0]
	}
	return ""
}

// looksLikeDate reports whether a field is the "2026-03-04" a listing
// stamps a row with.
func looksLikeDate(v string) bool {
	if len(v) != len("2006-01-02") {
		return false
	}
	for i, r := range v {
		switch i {
		case 4, 7:
			if r != '-' {
				return false
			}
		default:
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// tokenFromResult reads the page token a changes result offers.
func tokenFromResult(out string) string {
	_, rest, ok := strings.Cut(out, `page_token "`)
	if !ok {
		return ""
	}
	token, _, ok := strings.Cut(rest, `"`)
	if !ok {
		return ""
	}
	return token
}

// made are the ids the create step produced. An empty one means that
// create failed, and the calls needing it say they were skipped.
type made struct {
	folder, doc, sheet, text, blob, small string
}

// textRenamed is the name organize gives the text file, and so the one
// the wide shares after it carry as file_name.
const textRenamed = "rows renamed.csv"

// create makes one of everything, because several of Drive's rules turn
// out to be per format rather than per kind of thing.
func (w *writeRun) create() made {
	var m made
	m.folder = w.createAndKeepID(call{name: "a folder is created with a description",
		tool: "create_folder", args: map[string]any{
			"name": "Reports", "parent": w.scratchID, "description": "made by the live driver",
		}})
	m.doc = w.createAndKeepID(call{name: "a Google Doc is created", tool: "create_file", args: map[string]any{
		"name": "Notes", "kind": "doc", "parent": w.scratchID,
	}})
	m.text = w.createAndKeepID(call{name: "a csv is created from inline text", tool: "create_file", args: map[string]any{
		"name": "rows.csv", "parent": w.scratchID,
		"content": "name,amount\nfirst,1\nsecond,2\n", "mime_type": "text/csv",
	}})
	// The import path: markdown becomes a formatted Google Doc.
	w.createAndKeepID(call{name: "markdown is imported as a Google Doc", tool: "create_file", args: map[string]any{
		"name": "Imported notes", "parent": w.scratchID,
		"content": "# Heading\n\nA paragraph.\n", "mime_type": "text/markdown", "convert_to": "doc",
	}})
	// One of every Google kind, because the id rule is per format and a
	// run that tries one of five proves one of five.
	for _, kind := range []string{"sheet", "slides", "drawing", "form"} {
		id := w.createAndKeepID(call{name: "a file of every Google kind is created",
			tool: "create_file", args: map[string]any{
				"name": "New " + kind, "kind": kind, "parent": w.scratchID,
			}})
		if kind == "sheet" {
			m.sheet = id
		}
	}
	// A duplicate name, asked for explicitly: the guard has to be a
	// guard and not a wall.
	w.createAndKeepID(call{name: "a duplicate folder name is allowed when asked for",
		tool: "create_folder", args: map[string]any{
			"name": "Reports", "parent": w.scratchID, "allow_duplicate": true,
		}})

	// Both upload paths. The small one goes in a single request; the
	// large one is chunked and is the half no fake can prove.
	small := filepath.Join(w.dir, "livedrive-small.csv")
	if err := os.WriteFile(small, []byte("name,amount\nfirst,1\n"), 0o600); err != nil {
		w.problem("could not write the small file to upload", err)
	} else {
		defer func() { _ = os.Remove(small) }()
		m.small = w.createAndKeepID(call{name: "a small file is uploaded in one request",
			tool: "upload_file", args: map[string]any{
				"local_path": "livedrive-small.csv", "parent": w.scratchID, "name": "small upload.csv",
			}})
	}
	big := filepath.Join(w.dir, "livedrive-upload.bin")
	if err := writeFiller(big, 6<<20); err != nil {
		w.problem("could not write the file to upload", err)
	} else {
		defer func() { _ = os.Remove(big) }()
		m.blob = w.createAndKeepID(call{name: "a large file is uploaded in resumable chunks",
			tool: "upload_file", args: map[string]any{
				"local_path": "livedrive-upload.bin", "parent": w.scratchID, "name": "large upload.bin",
			}})
	}
	return m
}

// readBack asks for everything that was just written, which is the only
// way to see that a write did what its result claimed.
func (w *writeRun) readBack(m made) {
	w.needing(m.text, call{name: "the csv's card is read back", tool: "get_file", args: map[string]any{"file": m.text}})
	w.needing(m.doc, call{name: "the Google Doc's card is read back",
		tool: "get_file", args: map[string]any{"file": m.doc}})
	w.call(call{name: "the scratch folder is listed", tool: "list_folder", args: map[string]any{"folder": w.scratchID}})
	w.call(call{name: "the scratch folder is listed as a tree", tool: "list_folder", args: map[string]any{
		"folder": w.scratchID, "recursive": true, "max_depth": 3,
	}})
	w.call(call{name: "a search in the scratch folder finds every kind",
		tool: "search_files", args: map[string]any{"in_folder": w.scratchID, "kind": "any"}})
	w.call(call{name: "a name search matches the beginnings of words",
		tool: "search_files", args: map[string]any{"in_folder": w.scratchID, "name": "rows"}})

	w.needing(m.text, call{name: "the csv is read as text", tool: "read_file", args: map[string]any{"file": m.text}})
	// A window, then the continuation it offered: paging is the part a
	// model actually has to use, and an offset that does not line up is
	// invisible until someone follows it.
	w.needing(m.text, call{name: "a read stops at its window",
		tool: "read_file", args: map[string]any{"file": m.text, "max_chars": 12}})
	w.needing(m.text, call{name: "a read continues at the offset its window gave",
		tool: "read_file", args: map[string]any{"file": m.text, "offset": 12, "max_chars": 12}})
	w.needing(m.text, call{name: "a read past the end says so",
		tool: "read_file", args: map[string]any{"file": m.text, "offset": 9000}})
	w.needing(m.doc, call{name: "a Google Doc is read as text", tool: "read_file", args: map[string]any{"file": m.doc}})
	w.needing(m.sheet, call{name: "a Google Sheet is read as text",
		tool: "read_file", args: map[string]any{"file": m.sheet}})
	w.needing(m.sheet, call{name: "a Google Sheet is read as tab-separated values",
		tool: "read_file", args: map[string]any{"file": m.sheet, "format": "tsv"}})

	w.needing(m.text, call{name: "a csv is downloaded to the local directory",
		tool: "download_file", args: map[string]any{"file": m.text}})
	w.needing(m.doc, call{name: "a Google Doc is exported as a PDF",
		tool: "download_file", args: map[string]any{"file": m.doc, "format": "pdf"}})
	w.needing(m.doc, call{name: "a Google Doc is exported in its default format",
		tool: "download_file", args: map[string]any{"file": m.doc}})
	w.needing(m.sheet, call{name: "a Google Sheet is exported in its default format",
		tool: "download_file", args: map[string]any{"file": m.sheet}})
	w.needing(m.blob, call{name: "a large file is downloaded in full",
		tool: "download_file", args: map[string]any{"file": m.blob}})
}

// organize runs everything that changes a file without replacing it.
func (w *writeRun) organize(m made) {
	w.needing(m.text, call{name: "new content keeps the previous revision", tool: "update_content", args: map[string]any{
		"file": m.text, "content": "name,amount\nfirst,10\n", "keep_previous_revision": true,
	}})
	// The other half of update_content: new bytes from a local file.
	replacement := filepath.Join(w.dir, "livedrive-replacement.csv")
	if err := os.WriteFile(replacement, []byte("name,amount\nfirst,100\nsecond,200\n"), 0o600); err != nil {
		w.problem("could not write the replacement file", err)
	} else {
		defer func() { _ = os.Remove(replacement) }()
		w.needing(m.small, call{name: "new content is sent from a local file", tool: "update_content", args: map[string]any{
			"file": m.small, "local_path": "livedrive-replacement.csv",
		}})
	}

	w.needing(m.text, call{name: "a file is renamed, starred and given a property",
		tool: "update_file", args: map[string]any{
			"file": m.text, "name": textRenamed, "starred": true,
			"properties": map[string]any{"livedrive": "yes"},
		}})
	// An empty value deletes a property, and an empty description clears
	// it: the two places where "" means something.
	w.needing(m.text, call{name: "a file is given a description", tool: "update_file", args: map[string]any{
		"file": m.text, "description": "written by the live driver",
	}})
	w.needing(m.text, call{name: "an empty value clears a description and deletes a property",
		tool: "update_file", args: map[string]any{
			"file": m.text, "description": "", "properties": map[string]any{"livedrive": ""},
		}})
	// A patch that changes nothing has to say so rather than reporting a
	// write that did not happen.
	w.needing(m.text, call{name: "a patch that changes nothing says so",
		tool: "update_file", args: map[string]any{"file": m.text, "name": textRenamed}})
	w.needing(m.folder, call{name: "a folder is given a color",
		tool: "update_file", args: map[string]any{"file": m.folder, "color": "#4986e7"}})

	if m.text != "" && m.folder != "" {
		w.call(call{name: "a dry run of a move says where the file would go",
			tool: "move_file", args: map[string]any{"file": m.text, "to": m.folder, "dry_run": true}})
		w.call(call{name: "a file moves into a folder",
			tool: "move_file", args: map[string]any{"file": m.text, "to": m.folder}})
		// Moving it where it already is changes nothing, and says so.
		w.call(call{name: "a move to where a file already is changes nothing",
			tool: "move_file", args: map[string]any{"file": m.text, "to": m.folder}})
	}

	w.needing(m.text, call{name: "a csv is copied",
		tool: "copy_file", args: map[string]any{"file": m.text, "name": "rows copy.csv", "to": w.scratchID}})
	// Copying a Google Doc is the case where the copy is a Doc too, and
	// so cannot carry a generated id either.
	w.needing(m.doc, call{name: "a Google Doc is copied",
		tool: "copy_file", args: map[string]any{"file": m.doc, "name": "Notes copy", "to": w.scratchID}})
	// The import route: a csv becomes a Sheet, which is the conversion
	// Drive offers for it. Asking for a Doc is refused before the call,
	// with what it can become instead.
	w.needing(m.text, call{name: "a csv is copied into a Google Sheet", tool: "copy_file", args: map[string]any{
		"file": m.text, "name": "rows as a sheet", "to": w.scratchID, "convert_to": "sheet",
	}})
	w.needing(m.text, call{name: "a csv copied into a Google Doc is refused", tool: "copy_file", args: map[string]any{
		"file": m.text, "name": "rows as a doc", "to": w.scratchID, "convert_to": "doc",
	}, why: "Google imports a csv as a Sheet, not as a Doc"})
	w.needing(m.text, call{name: "a shortcut to a file is created", tool: "create_shortcut", args: map[string]any{
		"target": m.text, "parent": w.scratchID, "name": "rows shortcut",
	}})

	w.needing(m.text, call{name: "a dry run of a trash says what would go",
		tool: "trash_file", args: map[string]any{"file": m.text, "dry_run": true}})
	w.needing(m.text, call{name: "a file is trashed", tool: "trash_file", args: map[string]any{"file": m.text}})
	w.needing(m.text, call{name: "a trashed file is restored", tool: "restore_file", args: map[string]any{"file": m.text}})
	// Restoring what is not in the trash changes nothing.
	w.needing(m.text, call{name: "restoring a file not in the trash changes nothing",
		tool: "restore_file", args: map[string]any{"file": m.text}})
}

// moveExposure is a move that would let anyone with the link reach a
// file, which is refused naming them, and the same move once the file is
// shared by link itself, which then gains nobody and goes. A folder made
// here is opened to anyone with the link, which needs share_by_link and
// so -destructive. The server works out who can reach the file after the
// move before making it, then reads it back, and says when the two
// differ; a step here fails when they do, because that prediction of how
// Drive re-applies sharing on a move is what this run exists to check.
// Every link is removed again before the folder is trashed.
//
// Moving the file into the open folder and back out leaves it private:
// once the folder also gives the link grant, Drive lists the file's own
// as inherited only and drops it with the folder's. So it is shared by
// link again before each step that needs it shared.
func (w *writeRun) moveExposure() {
	if !w.destructive {
		w.out.Say("\n(pass -destructive to check that a move or a copy that would reach anyone with the " +
			"link is refused: opening a folder by link is share_by_link's)")
		return
	}
	const openName, fileName = "Open by link", "moves into the open folder.txt"
	open := w.createAndKeepID(call{name: "a folder to open by link is created",
		tool: "create_folder", args: map[string]any{"name": openName, "parent": w.scratchID}})
	file := w.createAndKeepID(call{name: "a file to move into an open folder is created",
		tool: "create_file", args: map[string]any{
			"name": fileName, "parent": w.scratchID,
			"content": "who can reach this depends on its folder\n", "mime_type": "text/plain",
		}})
	if open == "" || file == "" {
		w.out.Say("\n=== move exposure: skipped, the folder or file it needs was never created ===")
		return
	}
	w.shareByLink(call{name: "a folder is opened to anyone with the link"}, open, openName)
	w.refusing(file, call{name: "a dry-run move that would reach anyone with the link is refused",
		tool: "move_file", args: map[string]any{"file": file, "to": open, "dry_run": true}, why: "anyone with the link"})
	w.refusing(file, call{name: "a move that would reach anyone with the link is refused",
		tool: "move_file", args: map[string]any{"file": file, "to": open}, why: "anyone with the link"})
	w.shareByLink(call{name: "the file is shared by link before its move"}, file, fileName)
	moved := w.call(call{name: "a file shared by link moves into an open folder",
		tool: "move_file", args: map[string]any{"file": file, "to": open}})
	w.unpredicted(moved)
	if !strings.Contains(moved, "anyone with the link can view") {
		w.problem("the file moved into a folder anyone with the link can open does not say anyone with "+
			"the link can view it", errors.New("no link in the result"))
	}
	w.unpredicted(w.call(call{name: "a file moves out of an open folder",
		tool: "move_file", args: map[string]any{"file": file, "to": w.scratchID}}))
	w.shareByLink(call{name: "the file is shared by link again before a move of several"}, file, fileName)
	w.moveSeveral(open, file)
	w.shareByLink(call{name: "the file is shared by link again before a copy"}, file, fileName)
	w.copyExposure(open, file)
	w.needing(file, call{name: "the moved file's link is removed",
		tool: "unshare_file", args: map[string]any{"file": file, "remove_link": true}})
	w.needing(open, call{name: "the open folder's link is removed",
		tool: "unshare_file", args: map[string]any{"file": open, "remove_link": true}})
}

// shareByLink opens id to anyone with the link. Only share_by_link does
// that, so only a run with -destructive can. The step c carries the
// name; shareByLink fills in the call.
func (w *writeRun) shareByLink(c call, id, name string) {
	w.needing(id, call{name: c.name, tool: "share_by_link", args: map[string]any{
		"file": id, "file_name": name, "principal": "anyone", "role": "reader", "confirm": true,
	}})
}

// copyExposure is the copy's side of the same check, which no page of
// Google's states: a copy takes on who can reach the folder it lands in,
// and none of the grants made on the original. A copy of a private file
// into the open folder is refused; a copy of a file shared by link into
// it gains nobody, goes, and anyone with the link can view it; and a
// copy of that file into the scratch folder is private.
func (w *writeRun) copyExposure(open, file string) {
	private := w.createAndKeepID(call{name: "a private file to copy is created", tool: "create_file", args: map[string]any{
		"name": "private original.txt", "parent": w.scratchID,
		"content": "nobody but this account can reach this\n", "mime_type": "text/plain",
	}})
	if private != "" {
		w.refusing(private, call{name: "a dry-run copy that would reach anyone with the link is refused",
			tool: "copy_file", args: map[string]any{
				"file": private, "to": open, "name": "copied into the open folder.txt", "dry_run": true,
			}, why: "anyone with the link"})
		w.refusing(private, call{name: "a copy that would reach anyone with the link is refused",
			tool: "copy_file", args: map[string]any{
				"file": private, "to": open, "name": "copied into the open folder.txt",
			}, why: "anyone with the link"})
	}
	copied := w.call(call{name: "a copy into an open folder takes the folder's link",
		tool: "copy_file", args: map[string]any{
			"file": file, "to": open, "name": "a link-shared file copied into the open folder.txt"}})
	w.unpredicted(copied)
	if !strings.Contains(copied, "Who can reach the copy: anyone with the link can view.") {
		w.problem("a copy into a folder anyone with the link can open does not say anyone with the link "+
			"can view it", errors.New("no link in the result"))
	}
	alone := w.call(call{name: "a copy takes none of the original's grants", tool: "copy_file", args: map[string]any{
		"file": file, "to": w.scratchID, "name": "copy of a file shared by link.txt"}})
	w.unpredicted(alone)
	if !strings.Contains(alone, "Who can reach the copy: private to you.") {
		w.problem("a copy took the link granted on the original, which the server says it does not",
			errors.New("the copy is not private"))
	}
}

// downloads sets a file's download restriction each way and reads it
// back, then checks what the legacy switch does to it. The guide says
// false lifts the restriction on editors too; what true does to editors
// it does not say, and the card after it does.
func (w *writeRun) downloads(m made) {
	if m.doc == "" {
		w.out.Say("\n=== download restrictions: skipped, the document was never created ===")
		return
	}
	expect := func(c call, want, not string) {
		c.args["file"] = m.doc
		out := w.call(c)
		switch {
		case want != "" && !strings.Contains(out, want):
			w.problem("the card after "+mcpstdio.Encode(c.args)+" does not say "+want, errors.New("restriction not read back"))
		case not != "" && strings.Contains(out, not):
			w.problem("the card after "+mcpstdio.Encode(c.args)+" still says "+not, errors.New("restriction not read back"))
		}
	}
	editors := "downloads: viewers, commenters and editors cannot"
	viewers := "downloads: viewers and commenters cannot"
	expect(call{name: "downloads are restricted for editors too", tool: "update_file",
		args: map[string]any{"restrict_download": "editors"}}, editors, "")
	// From editors down to viewers: the editors may download again and
	// the viewers and commenters still may not.
	expect(call{name: "the download restriction drops to viewers and commenters", tool: "update_file",
		args: map[string]any{"restrict_download": "viewers"}}, viewers, editors)
	expect(call{name: "the legacy switch off lifts every download restriction", tool: "update_file",
		args: map[string]any{"copy_requires_writer_permission": false}}, "", "downloads:")
	out := w.call(call{name: "the legacy download switch is turned on",
		tool: "update_file", args: map[string]any{"file": m.doc, "copy_requires_writer_permission": true}})
	switch {
	case strings.Contains(out, editors):
		w.out.Say("(copy_requires_writer_permission true restricted editors too. " + recordIt + ")")
	case strings.Contains(out, viewers):
		w.out.Say("(copy_requires_writer_permission true restricted viewers and commenters only. " + recordIt + ")")
	default:
		w.problem("the card after copy_requires_writer_permission true says nobody is restricted",
			errors.New("restriction not read back"))
	}
	expect(call{name: "the download restriction is lifted", tool: "update_file",
		args: map[string]any{"restrict_download": "none"}}, "", "downloads:")
	w.needing(m.doc, call{name: "the download restriction and the legacy switch together are refused",
		tool: "update_file", args: map[string]any{
			"file": m.doc, "restrict_download": "viewers", "copy_requires_writer_permission": true,
		}, why: "the current switch and the legacy one together, which Google warns can conflict"})
}

// limitedAccess gives a folder limited access, reads who it keeps out,
// and turns it off again. With -destructive the folder around it is
// opened by link first: Drive's listing then settles whether a link
// grant from above shows as a metadata view, which the access guide
// shows for people only, and turning limited access off is refused
// while the link would open it, naming anyone with the link. Off goes
// once the link is gone.
func (w *writeRun) limitedAccess() {
	const openName = "Open around a limited one"
	open := w.createAndKeepID(call{name: "a folder to hold a limited one is created",
		tool: "create_folder", args: map[string]any{"name": openName, "parent": w.scratchID}})
	inner := ""
	if open != "" {
		inner = w.createAndKeepID(call{name: "a folder to limit is created",
			tool: "create_folder", args: map[string]any{"name": "Limited", "parent": open}})
	}
	if inner == "" {
		w.out.Say("\n=== limited access: skipped, the folders it needs were never created ===")
		return
	}
	if w.destructive {
		w.shareByLink(call{name: "the folder around a limited one is opened by link"}, open, openName)
	}
	card := w.call(call{name: "a folder is given limited access",
		tool: "update_file", args: map[string]any{"file": inner, "limited_access": true}})
	if !strings.Contains(card, "limited access: only people added") {
		w.problem("the card after limited_access true does not say the folder has limited access",
			errors.New("limited access not read back"))
	}
	perms := w.call(call{name: "a limited folder lists who it keeps out",
		tool: "list_permissions", args: map[string]any{"file": inner}})
	if w.destructive {
		if strings.Contains(perms, "anyone with the link can see it but not open it") {
			w.out.Say("(Drive lists the link grant from above as a metadata view. " + recordIt + ")")
		} else {
			w.out.Say("(Drive does NOT list the link grant from above as a metadata view; read the listing above " +
				"and record what it shows in the comment at the code that depends on it.)")
		}
		w.refusing(inner, call{name: "turning limited access off under an open folder is refused",
			tool: "update_file", args: map[string]any{"file": inner, "limited_access": false}, why: "anyone with the link"})
		w.needing(open, call{name: "the link around the limited folder is removed",
			tool: "unshare_file", args: map[string]any{"file": open, "remove_link": true}})
	}
	opened := w.call(call{name: "a folder's limited access is turned off",
		tool: "update_file", args: map[string]any{"file": inner, "limited_access": false}})
	if strings.Contains(opened, "limited access: only people added") {
		w.problem("the card after limited_access false still says the folder has limited access",
			errors.New("limited access not read back"))
	}
}

// underFolder searches a folder two levels deep with under_folder, pages
// it, and checks a page_token is refused under another folder. Then it
// looks for where Drive refuses a long `in parents` group, which nothing
// Google publishes says.
func (w *writeRun) underFolder() {
	outer := w.createAndKeepID(call{name: "a folder to search under is created",
		tool: "create_folder", args: map[string]any{"name": "Under", "parent": w.scratchID}})
	inner := ""
	if outer != "" {
		inner = w.createAndKeepID(call{name: "a folder two levels down is created",
			tool: "create_folder", args: map[string]any{"name": "Inner", "parent": outer}})
	}
	if inner == "" {
		w.out.Say("\n=== under_folder: skipped, the folders it needs were never created ===")
		return
	}
	var files []string
	for _, name := range []string{"found under a folder one.txt", "found under a folder two.txt"} {
		files = append(files, w.createAndKeepID(call{name: "a file is created two levels down",
			tool: "create_file", args: map[string]any{
				"name": name, "parent": inner, "content": "two levels down\n", "mime_type": "text/plain",
			}}))
	}
	search := map[string]any{"under_folder": outer, "name": "found", "limit": 1}
	page := ""
	for i := range 10 {
		if i > 0 {
			w.pause(3 * time.Second)
		}
		page = w.call(call{name: "under_folder finds a file two levels down", tool: "search_files", args: search})
		if files[0] != "" && strings.Contains(page, files[0]) || files[1] != "" && strings.Contains(page, files[1]) {
			break
		}
	}
	token := tokenIn(page)
	if token == "" {
		w.unverified("search_files.under_folder found no page to continue: the index has not caught up with "+
			"the two files this run made two levels down", errors.New("no page_token"))
		return
	}
	w.call(call{name: "under_folder continues on its second page", tool: "search_files", args: map[string]any{
		"under_folder": outer, "name": "found", "limit": 1, "page_token": token,
	}})
	w.needing(outer, call{name: "a page token under another folder is refused",
		tool: "search_files", args: map[string]any{
			"under_folder": inner, "name": "found", "limit": 1, "page_token": token,
		}, why: "a page_token from a search under another folder"})
	w.queryLimit()
}

// queryLimit looks for the longest `in parents` group Drive takes, by
// doubling one until Drive refuses it. under_folder stops at
// service.MaxUnderFolders folders, half of what Drive takes. It repeats
// the scratch folder's id, so it measures length and term count, not
// distinct folders. Nothing here fails the run: it reports the answer,
// for the comment at the code that depends on it.
func (w *writeRun) queryLimit() {
	probe := call{name: "a long in-parents group is sent until Drive refuses it", tool: "search_files",
		args: map[string]any{"raw_query": "", "limit": 1}}
	w.out.Sayf("\n=== %s ===\n%s, doubling the terms until Drive refuses (under_folder stops at %d)",
		probe.name, probe.tool, service.MaxUnderFolders)
	accepted := 0
	for n := service.MaxUnderFolders / 2; n <= 32*service.MaxUnderFolders; n *= 2 {
		terms := make([]string, n)
		for i := range terms {
			terms[i] = "'" + w.scratchID + "' in parents"
		}
		raw := strings.Join(terms, " or ")
		probe.args["raw_query"] = raw
		out, isError, err := w.quiet(probe)
		if err != nil || isError {
			answer, _, _ := strings.Cut(out, "\n")
			if err != nil {
				answer = err.Error()
			}
			w.out.Sayf("(Drive refused %d terms, %d bytes of query, and took %d. "+recordIt+" HTTP status: %s. "+
				"The answer: %s)", n, len(raw), accepted, statusOf(answer, w.sess.StderrTail(40)), answer)
			return
		}
		accepted = n
	}
	w.out.Sayf("(Drive took %d terms, and refused none tried. "+recordIt+")", accepted)
}

// statusOf is the HTTP status of a refusal: from the tool's answer when
// it names one, as it does for a refusal Google's front end made, or
// else from the server's debug log of the last failed request.
func statusOf(answer string, logs []string) string {
	if m := statusInAnswer.FindStringSubmatch(answer); len(m) > 1 {
		return m[1]
	}
	status := ""
	for _, line := range logs {
		if !strings.Contains(line, "drive api error") {
			continue
		}
		if m := statusInLog.FindStringSubmatch(line); len(m) > 1 {
			status = m[1]
		}
	}
	if status == "" {
		return "not in the answer, and not logged: run with GDRIVE_LOG_LEVEL=debug"
	}
	return status
}

var (
	statusInAnswer = regexp.MustCompile(`HTTP (\d{3})`)
	// The text log writes status=400, the JSON log "status":400.
	statusInLog = regexp.MustCompile(`"?status"?[=:](\d{3})`)
)

// moveSeveral moves two files into the open folder in one call, with the
// folder itself listed as a third item. The file shared by link gains
// nobody and moves; the private one would reach anyone with the link
// and is refused, naming them; the folder is refused because nothing
// moves into itself. Each gets its own outcome.
func (w *writeRun) moveSeveral(open, file string) {
	other := w.createAndKeepID(call{name: "a second file to move together is created",
		tool: "create_file", args: map[string]any{
			"name": "moves with the other one.txt", "parent": w.scratchID,
			"content": "moved in the same call\n", "mime_type": "text/plain",
		}})
	if other == "" {
		w.out.Say("\n=== move of several: skipped, the second file was never created ===")
		return
	}
	in := map[string]any{"files": []any{file, other, open}, "to": open}
	dry := w.call(call{name: "a dry run of a move of several says which items would move",
		tool: "move_file", args: map[string]any{"files": in["files"], "to": open, "dry_run": true}})
	if !strings.Contains(dry, "would move 1 of 3 items") {
		w.problem("the dry run of a move of several does not say one of three would move",
			errors.New("unexpected dry run"))
	}
	moved := w.call(call{name: "a move of several gives each item its own outcome", tool: "move_file", args: in})
	w.unpredicted(moved)
	switch {
	case !strings.Contains(moved, "moved 1 of 3 items"):
		w.problem("the move of several does not say one of three moved", errors.New("unexpected result"))
	case !strings.Contains(moved, "inside this account's organization: anyone with the link"):
		// The refusal's own clause: the file that moved is shared by link
		// too, and its sharing line says "anyone with the link" whatever
		// the refusal says.
		w.problem("the item a move of several refused does not name who it would reach",
			errors.New("unexpected result"))
	}
	w.unpredicted(w.call(call{name: "a file moved with others moves back out",
		tool: "move_file", args: map[string]any{"files": []any{file}, "to": w.scratchID}}))
}

// unpredicted fails the step when a move's result says Drive answered
// other than the server worked out before the move.
func (w *writeRun) unpredicted(out string) {
	if strings.Contains(out, "that is not who this server worked out") {
		w.problem("after the move or copy Drive reports other access than the server predicted; the "+
			"beliefs about what a move or a copy does to sharing are wrong somewhere", errors.New("prediction missed"))
	}
}

// commentedMarkdown is a Markdown file holding one comment, written the
// way the Google Docs editor writes one into the file's text: a marker
// on each side of the passage and an entry in the block ending the file.
// Synthetic, like every fixture.
const commentedMarkdown = "# Notes\n\n<!-- gdoc_comment_begin: c1 -->Ship in May<!-- gdoc_comment_end: c1 -->.\n\n" +
	"---\ngdoc_comments:\n  c1:\n    status: ACTIVE\n    head:\n      author: Live Driver\n" +
	"      time: '2026-01-02T03:04:05.000Z'\n      body: is May real?\n      quote: |-\n        Ship in May\n---\n"

// markdownFile is a Markdown file Google Docs edits in place, which keeps
// the comments made on it there in its own text. What is checked
// is what this server does with the bytes Drive holds: the type a .md is
// given and keeps, what a read and a listing say, and a replacement that
// would lose the comment refused until it is asked for.
func (w *writeRun) markdownFile() {
	id := w.createAndKeepID(call{name: "a Markdown file with a comment is created",
		tool: "create_file", args: map[string]any{
			"name": "commented notes.md", "parent": w.scratchID, "content": commentedMarkdown,
		}})
	if id == "" {
		w.out.Say("\n=== markdown file: skipped, the file was never created ===")
		return
	}
	kindIs := func(c call, kind, after string) {
		card := w.call(c)
		if !strings.Contains(card, kind) {
			w.problem("after "+after+" the file is not a "+kind, errors.New("type not as expected"))
		}
	}
	kindIs(call{name: "a Markdown file created without a type is Markdown",
		tool: "get_file", args: map[string]any{"file": id}},
		"Markdown file", "create_file with no mime_type")
	if out := w.call(call{name: "a Markdown file's read says where its comments are",
		tool: "read_file", args: map[string]any{"file": id}}); !strings.Contains(out, "gdoc_comments block") {
		w.problem("read_file of a Markdown file does not say where its comments are", errors.New("note missing"))
	}
	if out := w.call(call{name: "a Markdown file's comment listing says they are in its text",
		tool: "list_comments", args: map[string]any{"file": id}}); !strings.Contains(out, "kept in the file's own text") {
		w.problem("list_comments of a Markdown file does not say its Google Docs comments are not listed",
			errors.New("note missing"))
	}
	w.needing(id, call{name: "new content that loses a kept comment is refused",
		tool: "update_content", args: map[string]any{"file": id, "content": "# Notes\n\nShip in May.\n"},
		why: "the new content would delete the comment kept in the file's text"})
	kept := strings.Replace(commentedMarkdown, "# Notes", "# Release notes", 1)
	if out := w.call(call{name: "new content that keeps the comment is taken",
		tool: "update_content", args: map[string]any{"file": id, "content": kept}}); !strings.Contains(out, "still holds") {
		w.problem("a replacement that keeps the comment does not say the text still holds it", errors.New("note missing"))
	}
	// A local file whose name gives another type: the file keeps its
	// own, which is the type Drive would otherwise take from the upload.
	local := filepath.Join(w.dir, "livedrive-notes.txt")
	if err := os.WriteFile(local, []byte(kept+"\n"), 0o600); err != nil {
		w.problem("could not write the local Markdown file", err)
	} else {
		defer func() { _ = os.Remove(local) }()
		w.call(call{name: "a Markdown file takes new content from a .txt file",
			tool: "update_content", args: map[string]any{"file": id, "local_path": "livedrive-notes.txt"}})
		kindIs(call{name: "a Markdown file stays Markdown after a .txt upload",
			tool: "get_file", args: map[string]any{"file": id}},
			"Markdown file", "update_content from a .txt file")
		w.needing(id, call{name: "content sent as text/plain is refused while it would lose the comment",
			tool: "update_content", args: map[string]any{
				"file": id, "local_path": "livedrive-notes.txt", "mime_type": "text/plain",
			}, why: "sent as text/plain, the file stops being Markdown and its comment is lost"})
		// What that keeps from happening, measured: Drive takes the
		// uploaded type as the file's, and gives the type back only to
		// another upload.
		w.call(call{name: "content sent as text/plain is taken when comment changes are allowed",
			tool: "update_content", args: map[string]any{
				"file": id, "local_path": "livedrive-notes.txt", "mime_type": "text/plain", "allow_comment_changes": true,
			}})
		kindIs(call{name: "a Markdown file uploaded as text/plain becomes text",
			tool: "get_file", args: map[string]any{"file": id}},
			"text file", "an upload sent as text/plain")
		w.call(call{name: "content sent as text/markdown gives the type back", tool: "update_content", args: map[string]any{
			"file": id, "local_path": "livedrive-notes.txt", "mime_type": "text/markdown",
		}})
		kindIs(call{name: "a file uploaded as text/markdown is Markdown again",
			tool: "get_file", args: map[string]any{"file": id}},
			"Markdown file", "an upload sent as text/markdown")
	}
	w.call(call{name: "content that drops the comment is taken when comment changes are allowed",
		tool: "update_content", args: map[string]any{
			"file": id, "content": "# Notes\n\nThe comment is gone.\n", "allow_comment_changes": true,
		}})
}

// refusals are the calls that must not work. A refusal that is expected
// proves as much as a success: it is how the server says no to something
// it should not do.
func (w *writeRun) refusals(m made) {
	w.call(call{name: "a folder name two folders already hold is refused",
		tool: "create_folder", args: map[string]any{"name": "Reports", "parent": w.scratchID},
		why: "two folders of that name are already there, so the guard cannot pick one"})
	w.call(call{name: "an upload from outside the local directory is refused",
		tool: "upload_file", args: map[string]any{"local_path": "/etc/hostname"},
		why: "a path outside the local directory"})
	w.call(call{name: "an upload path that climbs out of the local directory is refused",
		tool: "upload_file", args: map[string]any{"local_path": "../../etc/hostname"},
		why: "a relative path that climbs out of it"})
	w.call(call{name: "a folder's text is refused", tool: "read_file", args: map[string]any{"file": w.scratchID},
		why: "a folder has no text"})
	w.needing(m.blob, call{name: "a binary file's text is refused, naming the ways forward",
		tool: "read_file", args: map[string]any{"file": m.blob},
		why: "a binary file is not text, and the refusal names the two ways forward"})
	w.needing(m.doc, call{name: "new content for a Google Doc is refused",
		tool: "update_content", args: map[string]any{"file": m.doc, "content": "no"},
		why: "a Google Doc's content belongs to the Docs API"})
	w.needing(m.text, call{name: "new content at an unexpected revision is refused",
		tool: "update_content", args: map[string]any{
			"file": m.text, "content": "no", "expect_head_revision": "a-revision-that-was-never-current",
		}, why: "the file is not at the revision the caller expected"})
	w.needing(m.folder, call{name: "a plain copy of a folder is refused",
		tool: "copy_file", args: map[string]any{"file": m.folder},
		why: "Drive has no copy for a folder"})
	w.needing(m.text, call{name: "a color on a file is refused",
		tool: "update_file", args: map[string]any{"file": m.text, "color": "#4986e7"},
		why: "a color belongs to a folder"})
	w.needing(m.text, call{name: "an export format for a file with its own bytes is refused",
		tool: "download_file", args: map[string]any{"file": m.text, "format": "docx"},
		why: "format applies to a Google document, not to a file with bytes of its own"})
}

// sharedDrive is the half of the shared-drive work that writes: moving a
// file in and out, and the refusal that a My Drive folder cannot follow
// it. It is opt-in (-drive) because it is the only part of this run that
// touches anything outside the scratch folder.
func (w *writeRun) sharedDrive(m made) {
	if w.drive == "" {
		w.out.Say("\n(pass -drive NAME_OR_ID to also exercise moving a file into a shared drive and back)")
		return
	}
	if m.small == "" || m.folder == "" {
		w.out.Say("\n=== shared drive: skipped, the files it needs were never created ===")
		return
	}
	target := "drive:" + w.drive
	// The drive's members reach whatever moves in. Members inside the
	// organization make an ordinary move; a domain or someone outside it
	// among them refuses the move, naming who, and then nothing moved
	// and there is nothing to bring back. Either is correct.
	dry := w.call(call{name: "a dry run of a move into a shared drive says who would reach the file",
		tool: "move_file", args: map[string]any{"file": m.small, "to": target, "dry_run": true},
		tolerant: true})
	if strings.HasPrefix(dry, "[blocked]") {
		w.out.Say("(the drive reaches past the organization, so the move is refused, naming who)")
		w.refusing(m.small, call{name: "a move into a drive that reaches past the organization is refused",
			tool: "move_file", args: map[string]any{"file": m.small, "to": target}, why: "[blocked]"})
	} else {
		// Once moved, the file is outside the scratch folder, so trashing
		// that folder will not take it, and the drive's members can see
		// it. Bringing it back is the only cleanup there is. Deferred
		// before the move, so a stop signal after it still brings it
		// back.
		defer w.recover(m.small)
		out := w.call(call{name: "a file moves into a shared drive",
			tool: "move_file", args: map[string]any{"file": m.small, "to": target}})
		w.unpredicted(dry)
		w.unpredicted(out)
	}
	w.call(call{name: "a My Drive folder is refused a move into a shared drive",
		tool: "move_file", args: map[string]any{"file": m.folder, "to": target},
		why: "a My Drive folder cannot move into a shared drive"})
}

// recover brings a file back out of the shared drive and reads back
// that it is in the scratch folder again. A move that answered "moved:"
// is not proof the file left: get_file is. A failure here is the one
// thing this run can leave in somebody's shared drive, so it says so
// loudly, with what to do, rather than counting it and moving on.
func (w *writeRun) recover(id string) {
	w.cleanup(func() {
		w.unpredicted(w.call(call{name: "a file moves back out of the shared drive",
			tool: "move_file", args: map[string]any{"file": id, "to": w.scratchID}}))
		if w.inScratch(id) {
			return
		}
		// A second attempt: the usual reason is a rate limit, and this
		// is worth one retry before asking a person to do it by hand.
		w.call(call{name: "a file moves back out of the shared drive again",
			tool: "move_file", args: map[string]any{"file": id, "to": w.scratchID}, tolerant: true})
		if w.inScratch(id) {
			return
		}
		w.problem("A FILE IS NOT BACK IN THE SCRATCH FOLDER and may still be in the shared drive "+w.drive+
			", and this run cannot get it back. Its id is in the result above; move or trash it by hand",
			errors.New("get_file does not place it in the scratch folder after two moves back"))
	})
}

// inScratch reports whether get_file places a file in the scratch
// folder: in the drive the folder is in, which a location names first,
// and directly under the folder's name.
func (w *writeRun) inScratch(id string) bool {
	folder, folderRefused := w.do(call{name: "the scratch folder's location is read",
		tool: "get_file", args: map[string]any{"file": w.scratchID}})
	file, fileRefused := w.do(call{name: "a file brought back is in the scratch folder",
		tool: "get_file", args: map[string]any{"file": id}})
	home, at := cardField(folder, "location"), cardField(file, "location")
	driveOf := func(location string) string {
		drive, _, _ := strings.Cut(location, "/")
		return drive
	}
	return !folderRefused && !fileRefused && home != "" &&
		driveOf(at) == driveOf(home) && strings.HasSuffix(at, "/"+w.scratchName)
}

// unverified says a step could not run because Drive did not present
// the state it needs, which is not a failure: nothing is wrong, and the
// same run an hour later would do it. It is loud because the thing it
// guards against is a step being counted as verified by a run that never
// reached it.
func (w *writeRun) unverified(what string, err error) {
	w.out.Sayf("!! UNVERIFIED THIS RUN: %s (%v). Nothing is wrong: this is Drive being eventually "+
		"consistent. It means the step was not checked, rather than checked and passed.", what, err)
}

// problem records something that went wrong outside a tool call.
func (w *writeRun) problem(what string, err error) {
	w.out.Sayf("!! %s: %v", what, err)
	w.failures++
}

// needing runs a call that depends on an id an earlier create produced,
// and says so when it cannot.
func (w *writeRun) needing(id string, c call) {
	if id == "" {
		w.skipped(c.name, neverCreated)
		return
	}
	w.call(c)
}

// refusing is a call that ought to be refused, saying its why. The
// refusal is what is being checked, so one that does not say it fails
// the step.
func (w *writeRun) refusing(id string, c call) {
	if id == "" {
		w.skipped(c.name, neverCreated)
		return
	}
	out := w.call(c)
	if !strings.Contains(out, c.why) {
		w.failures++
		w.out.Say("!! the refusal does not say " + c.why)
	}
}

// neverCreated is why a step that needs an id an earlier create never
// produced did not run.
const neverCreated = "the file it needs was never created"

// skipped says the step name did not run, and why.
func (w *writeRun) skipped(name, why string) {
	w.out.Sayf("\n=== %s: skipped, %s ===", name, why)
}

// trashScratch removes the folder and everything the run put in it.
func (w *writeRun) trashScratch() {
	if w.scratchID == "" {
		return
	}
	w.call(call{name: "the scratch folder is trashed", tool: "trash_file", args: map[string]any{"file": w.scratchID}})
	w.out.Say("\n(the scratch folder is in the trash; empty it yourself if you want it gone for good)")
}

// call runs one tool and prints the redacted result.
func (w *writeRun) call(c call) string {
	out, _ := w.do(c)
	return out
}

// quiet sends a step without printing it, for a read whose answer the
// steps after it use rather than show, or a step that prints its own
// account of what came back.
func (w *writeRun) quiet(c call) (out string, isError bool, err error) {
	return w.sess.CallTool(c.tool, c.args)
}

// do is call, saying as well whether the tool refused.
func (w *writeRun) do(c call) (out string, isError bool) {
	out, isError, err := w.send(c)
	if err != nil {
		return "", true
	}
	switch {
	case c.tolerant:
		w.out.Say("(either outcome is correct here; it was " + outcomeWord(isError) + ")")
	case isError != (c.why != ""):
		w.failures++
		if c.why != "" {
			w.out.Say("!! expected a refusal and did not get one")
		} else {
			w.out.Say("!! unexpected tool error")
		}
	}
	return out, isError
}

// send prints a step's banner, runs it and prints the answer, leaving
// the verdict to its caller. A transport failure is printed and counted.
func (w *writeRun) send(c call) (out string, isError bool, err error) {
	// The arguments are echoed unwrapped because the transcript redacts
	// the whole line. They carry addresses nobody typed: starting an
	// approval puts the SIGNED-IN account's address there on every
	// -write run.
	w.out.Say(banner(c))
	out, isError, err = w.sess.CallTool(c.tool, c.args)
	if err != nil {
		w.out.Sayf("!! transport failure: %v", err)
		w.failures++
		return "", true, err
	}
	w.out.Say(out)
	return out, isError, nil
}

// createAndKeepID runs a create and pulls the new id out of the result,
// because the calls after it need one. A create that failed returns "",
// and the calls that needed it say they were skipped rather than the run
// stopping there. The id is read before redaction and never printed.
func (w *writeRun) createAndKeepID(c call) string {
	return mcpstdio.IDIn(w.call(c))
}

// writeFiller writes n bytes of compressible filler, which is enough to
// push an upload onto the resumable path without being a large file to
// carry around.
func writeFiller(path string, n int) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	block := []byte(strings.Repeat("livedrive filler ", 64))
	for written := 0; written < n; {
		size := min(len(block), n-written)
		if _, err := f.Write(block[:size]); err != nil {
			return err
		}
		written += size
	}
	return f.Close()
}

// indexableText checks that use_content_as_indexable_text makes an
// upload's words searchable. Drive reads a text file's words on its own,
// so the bytes go up as a type it does not read, holding a word that is
// in no name or description under the scratch folder, and the step
// searches for that word. The same bytes go up again without the flag:
// only that copy staying out of the results shows the flag did it.
func (w *writeRun) indexableText() {
	const word = "quokkalantern"
	path := filepath.Join(w.dir, "indexable upload.bin")
	if err := os.WriteFile(path, []byte("the one word only this text holds is "+word+"\n"), 0o600); err != nil {
		w.problem("writing the indexable-text fixture", err)
		return
	}
	flagged := w.createAndKeepID(call{name: "an upload is made with use_content_as_indexable_text",
		tool: "upload_file", args: map[string]any{
			"local_path": "indexable upload.bin", "parent": w.scratchID, "name": "indexed upload.bin",
			"mime_type": "application/octet-stream", "use_content_as_indexable_text": true, "allow_duplicate": true,
		}})
	control := w.createAndKeepID(call{name: "the same bytes are uploaded without the flag",
		tool: "upload_file", args: map[string]any{
			"local_path": "indexable upload.bin", "parent": w.scratchID, "name": "control upload.bin",
			"mime_type": "application/octet-stream", "allow_duplicate": true,
		}})
	if flagged == "" {
		w.out.Say("\n=== indexable text: skipped, the upload it needs was never made ===")
		return
	}
	if !w.pollSearch(call{name: "use_content_as_indexable_text makes an upload's words searchable",
		tool: "search_files", args: map[string]any{"text": word}}, flagged) || control == "" {
		return
	}
	// The index has caught up with the flagged copy, which went up first,
	// so one search is a fair look for the control.
	if w.searchAllPages(call{name: "the upload without the flag is looked for by the same word",
		tool: "search_files", args: map[string]any{"text": word, "under_folder": w.scratchID}},
		control) {
		w.out.Say("(the copy uploaded without use_content_as_indexable_text was found by the same word: " +
			"Drive reads this type on its own, so this run does not show what the flag does. " + recordIt + ")")
		return
	}
	w.out.Say("(the copy uploaded without use_content_as_indexable_text was not found by the same word, " +
		"and the one with it was: the flag made the difference. " + recordIt + ")")
}

// resources reads the three gdrive:// templates, which are the part of
// the surface no tool call reaches. A resource read is a different
// method with a different failure shape, so a driver that only calls
// tools cannot show whether they work at all.
func (w *writeRun) resources(m made) {
	for _, r := range []resourceRead{
		{name: "a file's text is read as a resource", id: m.text},
		{name: "a file's card is read as a resource", id: m.text, suffix: "meta"},
		{name: "a folder's first page is read as a resource", id: w.scratchID, suffix: "children"},
		{name: "a folder's text is refused as a resource", id: w.scratchID, why: "a folder has no text of its own"},
		{name: "a file's children are refused as a resource", id: m.text, suffix: "children", why: "a file is not a folder"},
		{name: "an id that names nothing is refused as a resource", id: "1SyntheticFixtureFileIdAAAAAAAAAAAA",
			why: "an id that names nothing"},
	} {
		// The same guard every other call in this file gets from
		// needing: an id an earlier create never produced is a skip that
		// says so, not a silent one.
		if r.id == "" {
			w.skipped(r.name, neverCreated)
			continue
		}
		uri := service.ResourceURI(r.id, r.suffix)
		w.out.Say(header(r.name, "resource "+uri, r.why))
		text, mime, err := w.sess.ReadResource(uri)
		var refused *mcpstdio.RPCError
		switch {
		case errors.As(err, &refused):
			w.out.Say(refused.Detail)
			if r.why == "" {
				w.out.Say("!! unexpected refusal")
				w.failures++
			}
			continue
		case err != nil:
			w.out.Sayf("!! transport failure: %v", err)
			w.failures++
			continue
		}
		if r.why != "" {
			w.out.Say("!! expected a refusal and did not get one")
			w.failures++
		}
		w.out.Sayf("mime: %s\n%s", mime, head(text, 12))
	}
}

// resourceRead is one read of a gdrive:// template: the id, the suffix
// that picks the template, and why it ought to be refused, if it should.
type resourceRead struct {
	// name says what the read checks, as a call's name does.
	name, id, suffix, why string
}

// head keeps the first n lines, so a transcript shows the shape of a
// resource without printing a whole file.
func head(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= n {
		return text
	}
	more := len(lines) - n
	word := "lines"
	if more == 1 {
		word = "line"
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n… (%d more %s)", more, word)
}

// policyRefusal checks a share that the ORGANIZATION refuses, rather
// than one Drive refuses, which needs an administrator to have put the
// -blocked address out of bounds.
//
// The distinction is not cosmetic. A policy refusal maps to [blocked] —
// "your organization's sharing policy does not allow this, and no option
// here can work around it" — and everything else 403 maps to
// [forbidden], which tells a model its role is wrong and invites it to
// try something else. The mapping is built from three reason strings
// Google documents (domainPolicy, invalidSharingRequest,
// shareOutNotPermittedForContent) and is unverified against a real
// policy refusal. If Drive answers with a fourth, every policy refusal
// is reported as the wrong kind of failure.
//
// The reason itself is not in the tool result — it should not be — so it
// is read from the server's own debug log, where a request failure logs
// its class and reason and nothing else about the call.
func (w *writeRun) policyRefusal(m made) {
	if w.blocked == "" || m.text == "" {
		return
	}
	w.out.Say("\n--- a share the organization's policy refuses ---")
	if !w.destructive {
		w.out.Say("(pass -destructive as well: share_file refuses an address outside the organization " +
			"itself, so the organization's refusal is reached through share_outside_organization)")
		return
	}
	refusal := call{name: "a share the organization's policy refuses is classified as blocked",
		tool: "share_outside_organization", args: map[string]any{
			"file": m.text, "file_name": textRenamed, "principal": w.blocked, "role": "reader", "confirm": true,
		}, why: "the organization's sharing policy, not Drive's own rules"}
	// The verdict is this function's own: the class matters as much as
	// the refusal.
	out, isError, err := w.send(refusal)
	if err != nil {
		return
	}

	if !isError {
		w.problem("the share was ALLOWED. Either the admin setting is not in place for this account's "+
			"organizational unit yet, or "+w.blocked+" is inside it or on the allowlist. "+
			"Nothing was learned about the mapping, and the grant is still on the file",
			errors.New("no refusal to classify"))
		// It really did share, inside the scratch folder; take it back.
		w.call(call{name: "a policy share that went through is taken back",
			tool: "unshare_file", args: map[string]any{"file": m.text, "principal": w.blocked}})
		return
	}

	class, reason := classAndReason(out, w.sess.StderrTail(40))
	w.out.Sayf("\nGoogle's reason: %s\nthis server classified it as: [%s]", orUnknown(reason), orUnknown(class))
	// The class is one field of the refusal being checked, so a class of
	// blocked cannot confirm the mapping: a refusal that is not a policy
	// one but lands in the same class looks identical. The verdict prints
	// Google's message and says to read it, and names both readings when
	// the class is not blocked. It is the live step "a share the
	// organization's policy refuses is classified as blocked".
	switch {
	case class == "blocked":
		w.out.Say("VERDICT: classified as [blocked] — but READ GOOGLE'S MESSAGE ABOVE before recording " +
			"that as confirmation. This check cannot tell a policy refusal from any other refusal that " +
			"lands in the same class. If the message does not describe the organization refusing, the " +
			"mapping is wrong for this reason and this is not a confirmation.")
	case reason == "":
		w.problem("the refusal carried no reason this driver could read out of the debug log, so the "+
			"mapping is still unverified. Run with GDRIVE_LOG_LEVEL=debug",
			errors.New("no reason logged"))
	default:
		// Two things look the same here and want opposite fixes, so the
		// message names both rather than prescribing one. -blocked was
		// given an address believed to be refused by policy; if it was,
		// the mapping is missing a reason. If it was refused for some
		// other reason — a typo, an address with no Google account —
		// then [blocked] would have been the wrong answer and this is
		// the right one.
		w.problem("Google's reason "+reason+" mapped to ["+class+"], not [blocked]. Read Google's message "+
			"above and decide which of these it is: a POLICY refusal, in which case "+reason+" belongs in "+
			"the blocked reasons in internal/gapi/errors.go; or a refusal the caller can act on, in which "+
			"case ["+class+"] is correct and the address given to -blocked is not policy-refused",
			errors.New("reason "+reason+" is not classified as a policy refusal"))
	}
}

// classAndReason reads the class out of the tool's own "[class] message"
// and the reason out of the last logged request failure. The reason is a
// Google error code — never a name, an address or a file — which is why
// it is in the log at all.
func classAndReason(toolError string, logs []string) (class, reason string) {
	if m := classInResult.FindStringSubmatch(toolError); len(m) > 1 {
		class = m[1]
	}
	for _, line := range logs {
		if !strings.Contains(line, "drive api error") {
			continue
		}
		if m := reasonInLog.FindStringSubmatch(line); len(m) > 1 && m[1] != "" {
			reason = m[1]
		}
	}
	return class, reason
}

var (
	classInResult = regexp.MustCompile(`^\[([a-z_]+)\]`)
	reasonInLog   = regexp.MustCompile(`reason=(\S*)`)
)

func orUnknown(s string) string {
	if s == "" {
		return "(not recorded)"
	}
	return s
}

// copyAndCheckComments copies a file asking for its comment threads, and
// then LOOKS at the copy. carries is what the recorded rule expects of
// the kind: Drive's own formats bring their threads along and an
// uploaded file does not. The server reports only that Drive was asked,
// so looking at the copy is the only check that the threads arrived.
//
// When the threads came across, a second copy is made WITHOUT
// copy_comments, after the threads exist. Only when that one has none
// did the flag make the difference; without it, "Drive honored
// copy_comments" is said of a flag that may change nothing.
//
// It says so out loud rather than leaving it in the transcript. The
// answer is one line among thousands, and "all calls behaved as
// expected" is true of it either way.
func (w *writeRun) copyAndCheckComments(id, name, kind string, carries bool) {
	if id == "" {
		w.out.Sayf("\n=== copy_file with its comments: skipped, the %s it needs was never created ===", kind)
		return
	}
	has, made := w.copyHasThreads(call{name: "a copy is made with copy_comments"}, id, name, kind, true)
	if !made {
		return
	}
	if !has {
		if carries {
			w.out.Sayf("!! the copy of %s asked for the comment threads and has NONE, where the recorded rule "+
				"says Drive's own formats carry them. List them again in a minute; if there are still none, "+
				"the rule has changed. "+recordIt, kind)
		} else {
			w.out.Sayf("(the copy of %s has no comment threads, as the recorded rule says of an uploaded file)", kind)
		}
		return
	}
	if !carries {
		w.out.Sayf("!! the copy of %s carried its comment threads, where the recorded rule says an uploaded "+
			"file's do not. "+recordIt, kind)
	}
	has, made = w.copyHasThreads(call{name: "a control copy is made without copy_comments"}, id,
		"control copy of "+name, kind, false)
	if !made {
		return
	}
	if has {
		w.out.Sayf("!! a copy of %s made WITHOUT copy_comments carried the threads too, so this run cannot "+
			"say copy_comments made the difference. "+recordIt, kind)
		return
	}
	w.out.Sayf("(the copy of %s carried its comment threads and the copy without copy_comments did not: "+
		"Drive honored copy_comments here)", kind)
}

// copyHasThreads copies a file, with or without its comment threads, and
// reports whether the copy lists any, and whether the copy was made. The
// step c carries the copy's name; copyHasThreads fills in the call.
func (w *writeRun) copyHasThreads(c call, id, name, kind string, withComments bool) (has, made bool) {
	args := map[string]any{"file": id, "name": name, "to": w.scratchID, "allow_duplicate": true}
	if withComments {
		args["copy_comments"] = true
	}
	copied := w.createAndKeepID(call{name: c.name, tool: "copy_file", args: args})
	if copied == "" {
		w.out.Sayf("\n=== list_comments: skipped, the copy of %s was never made ===", kind)
		return false, false
	}
	threads := w.call(call{name: "a copy's comment threads are counted",
		tool: "list_comments", args: map[string]any{"file": copied, "include_deleted": false}})
	return !strings.Contains(threads, "0 comment threads"), true
}

// spareArguments drives the options `gates live-cover` recorded as
// undriven: ones a live step COULD send and none did.
//
// They are here rather than added to the calls above on purpose. Those
// calls are verified — each has run against Drive and had its result
// read — and adding an argument to a verified call changes what was
// verified. A new call with the argument on it proves the same thing and
// risks nothing that already works.
func (w *writeRun) spareArguments(m made) {
	w.out.Say("\n--- the options nothing had sent ---")

	// Creates, with the arguments a create takes and the run never gave
	// it. The duplicate-name guard is the interesting one: it is refused
	// by default and these say so.
	w.needing(w.scratchID, call{name: "a folder is created with a color", tool: "create_folder", args: map[string]any{
		"name": "a colored folder", "parent": w.scratchID, "color": "#4986e7",
	}})
	w.needing(w.scratchID, call{name: "a file is created described and starred", tool: "create_file", args: map[string]any{
		"name": "described and starred.txt", "parent": w.scratchID, "content": "one\n",
		"description": "made by the live driver", "starred": true, "allow_duplicate": true,
	}})
	w.needing(m.text, call{name: "a second shortcut of the same name is allowed when asked for",
		tool: "create_shortcut", args: map[string]any{
			"target": m.text, "parent": w.scratchID, "name": "a second shortcut", "allow_duplicate": true,
		}})
	w.needing(m.text, call{name: "a copy keeps its revision forever", tool: "copy_file", args: map[string]any{
		"file": m.text, "name": "a pinned copy.csv", "to": w.scratchID,
		"keep_revision_forever": true, "allow_duplicate": true,
	}})

	// The upload path, which takes five of these.
	if w.dir != "" {
		path := filepath.Join(w.dir, "described upload.txt")
		if err := os.WriteFile(path, []byte("bytes uploaded by the live driver\n"), 0o600); err != nil {
			w.problem("writing the upload fixture", err)
		} else {
			w.needing(w.scratchID, call{name: "an upload is made with a description and a type",
				tool: "upload_file", args: map[string]any{
					"local_path": "described upload.txt", "parent": w.scratchID,
					"description": "uploaded by the live driver", "mime_type": "text/plain", "allow_duplicate": true,
				}})
			w.needing(w.scratchID, call{name: "an upload is imported as a Google Doc", tool: "upload_file", args: map[string]any{
				"local_path": "described upload.txt", "parent": w.scratchID,
				"name": "imported as a doc", "convert_to": "doc", "allow_duplicate": true,
			}})
		}
		w.indexableText()
	}

	// The two sharing switches on a file, which the card prints back.
	w.needing(m.text, call{name: "a file's two sharing switches are set", tool: "update_file", args: map[string]any{
		"file": m.text, "copy_requires_writer_permission": true, "writers_can_share": false,
	}})
	w.needing(m.text, call{name: "new content is sent with its type", tool: "update_content", args: map[string]any{
		"file": m.text, "content": "col\nrow\n", "mime_type": "text/csv",
	}})

	// A dry run in front of something reversible, and an older revision
	// fetched by id — the one the run has just made a second one over.
	// Drive lists the current revision first, so the older one is asked
	// for by skipping it; the current one is fetched too, since its
	// download is the one the server checks against Drive's checksum.
	w.needing(m.text, call{name: "a dry run of a restore says what would come back",
		tool: "restore_file", args: map[string]any{"file": m.text, "dry_run": true}})
	if rev := w.revisionID(m.text, true); rev != "" {
		w.needing(m.text, call{name: "an older revision is downloaded by id",
			tool: "download_file", args: map[string]any{"file": m.text, "revision": rev}})
	} else {
		w.unverified("download_file.revision was not exercised on an older revision: none is listed",
			errors.New("the revision listing lags the write that made one"))
	}
	if rev := w.firstRevision(m.text); rev != "" {
		w.needing(m.text, call{name: "the current revision is downloaded and checked against its checksum",
			tool: "download_file", args: map[string]any{"file": m.text, "revision": rev}})
	}

	w.spareListings(m)
	w.spareSearches()
}

// spareListings drives the paging and filter options on the listings.
//
// page_size: 1 is how a token is produced without making more items than
// a scratch folder should hold: the first page comes back with one row
// and a token, and the token is what proves paging works rather than
// merely parses.
func (w *writeRun) spareListings(m made) {
	first := w.call(call{name: "the scratch folder is listed one item a page", tool: "list_folder", args: map[string]any{
		"folder": w.scratchID, "page_size": 1,
	}})
	if token := tokenIn(first); token != "" {
		w.needing(w.scratchID, call{name: "the scratch folder's second page is listed",
			tool: "list_folder", args: map[string]any{
				"folder": w.scratchID, "page_size": 1, "page_token": token,
			}})
	} else {
		w.unverified("list_folder.page_token was not exercised: the first page came back without a token",
			errors.New("the scratch folder holds fewer items than the run expected"))
	}
	w.needing(w.scratchID, call{name: "a recursive listing filters by kind and includes the trash",
		tool: "list_folder", args: map[string]any{
			// "file" is not a kind. The kinds are the ones the refusal lists:
			// any, audio, doc, drawing, folder, form, image, office, pdf,
			// sheet, shortcut, slides, video — and the run makes a sheet.
			"folder": w.scratchID, "include_trashed": true, "kind": "sheet", "max_items": 50, "recursive": true,
		}})

	if m.text != "" {
		// A second thread, so that page_size: 1 has a second page to
		// point at: with one thread per file the paged call comes back
		// without a token.
		w.needing(m.text, call{name: "a second thread is added for paging", tool: "add_comment", args: map[string]any{
			"file": m.text, "content": "and a second thread, so paging has somewhere to go",
		}})
		comments := w.call(call{name: "comment threads page one at a time since the run began",
			tool: "list_comments", args: map[string]any{
				"file": m.text, "page_size": 1, "since": w.started,
			}})
		if token := tokenIn(comments); token != "" {
			w.needing(m.text, call{name: "the second page of comment threads is listed",
				tool: "list_comments", args: map[string]any{
					"file": m.text, "page_size": 1, "page_token": token,
				}})
		} else {
			w.unverified("list_comments.page_token was not exercised: one page held every thread",
				errors.New("the run made fewer threads than a second page needs"))
		}
	}
	w.needing(w.scratchID, call{name: "the changes feed is read for My Drive only",
		tool: "list_changes", args: map[string]any{"my_drive_only": true, "limit": 5}})
	if m.doc != "" {
		w.needing(m.doc, call{name: "approvals are listed a page at a time",
			tool: "list_approvals", args: map[string]any{"file": m.doc, "page_size": 5}})
	}
}

// spareSearches drives the search fields nothing sent. Every one is
// scoped to the scratch folder or to this run's own moment, so a search
// that matches something matches something this run made.
func (w *writeRun) spareSearches() {
	w.needing(w.scratchID, call{name: "a search bounds creation and modification from the run's start",
		tool: "search_files", args: map[string]any{
			"in_folder": w.scratchID, "created_after": w.started, "modified_after": w.started,
		}})
	w.needing(w.scratchID, call{name: "a search filters by type and leaves out the trash",
		tool: "search_files", args: map[string]any{
			"in_folder": w.scratchID, "mime_type": "text/csv", "trashed": false,
		}})
	w.needing(w.scratchID, call{name: "a search filters by owner, scope and star",
		tool: "search_files", args: map[string]any{
			"in_folder": w.scratchID, "owner": "me", "scope": "my_drive", "starred": true,
		}})
	// A before that excludes everything this run made, which is the half
	// that proves the bound is applied rather than accepted.
	w.needing(w.scratchID, call{name: "a search bounded before the run finds none of its files",
		tool: "search_files", args: map[string]any{
			"in_folder": w.scratchID, "modified_before": w.started,
		}})
	w.needing(w.scratchID, call{name: "a raw query is passed through", tool: "search_files", args: map[string]any{
		"in_folder": w.scratchID, "raw_query": "trashed = false",
	}})
	// Full text waits on Drive's index the way the property search does,
	// so this proves the argument reaches Drive rather than that the
	// index has caught up.
	w.needing(w.scratchID, call{name: "a full-text search reaches Drive", tool: "search_files", args: map[string]any{
		"in_folder": w.scratchID, "text": "livedrive",
	}})
	page := w.call(call{name: "a search returns one result a page", tool: "search_files", args: map[string]any{
		"in_folder": w.scratchID, "limit": 1,
	}})
	if token := tokenIn(page); token != "" {
		w.needing(w.scratchID, call{name: "a search continues on its second page", tool: "search_files", args: map[string]any{
			"in_folder": w.scratchID, "limit": 1, "page_token": token,
		}})
	} else {
		w.unverified("search_files.page_token was not exercised: the first page came back without a token",
			errors.New("the index has not caught up with the folder this run just filled"))
	}
}

// tokenIn reads the continuation token a listing prints, or "" when the
// page was the last one.
//
// The pattern is read off internal/render, which prints it as part of a
// sentence — `more results: call again with page_token "…"` — and not on
// an `id:` line the way it prints an id.
//
// The value read here is the raw one. The transcript redacts on the way
// to the terminal, so what a person sees is a placeholder and what the
// next call sends is the token Drive gave.
func tokenIn(result string) string {
	m := pageTokenLine.FindStringSubmatch(result)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

var pageTokenLine = regexp.MustCompile(`page_token "([^"]+)"`)
