package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/service"
	"github.com/mmedum/google-drive-mcp/scripts/internal/mcpstdio"
	"github.com/mmedum/google-drive-mcp/scripts/internal/redact"
)

// writeRun exercises every tool that changes Drive, inside one scratch
// folder it makes and trashes again. Nothing outside that folder is
// touched: the folder is created first, every call names it or something
// inside it, and the last call trashes it whole.
//
// It is opt-in (-write) because it is the only part of this driver that
// changes anything in a real account.
type writeRun struct {
	sess *mcpstdio.Session
	red  *redact.Redactor
	// dir is the local directory the server was told to use, and the one
	// place uploads come from and downloads land in.
	dir string
	// scratchID is the folder everything happens in.
	scratchID string
	// drive is the shared drive to move a file through, or empty.
	drive string
	// share is an address to grant access to, or empty. It is the half
	// of the sharing surface that needs a second person, spike F
	// included.
	share string
	// failures counts calls that did not behave as expected.
	failures int
}

// scratchPrefix names the folder this driver works in, so a folder left
// behind by an interrupted run is recognisable and safe to remove.
const scratchPrefix = "google-drive-mcp livedrive scratch"

// runWrites drives the whole write surface and reports how many calls
// behaved unexpectedly.
func runWrites(s *mcpstdio.Session, red *redact.Redactor, dir, parent, drive, share string) (int, error) {
	w := &writeRun{sess: s, red: red, dir: dir, drive: drive, share: share}
	name := fmt.Sprintf("%s %s", scratchPrefix, time.Now().UTC().Format("2006-01-02 15:04:05"))
	args := map[string]any{"name": name}
	if parent != "" {
		args["parent"] = parent
	}
	w.scratchID = w.createAndKeepID("create_folder", args)
	if w.scratchID == "" {
		return w.failures, fmt.Errorf("the scratch folder could not be created, so nothing else can run safely")
	}
	fmt.Println("\n(everything below happens inside that folder, and it is trashed at the end)")

	w.exercise()
	w.trashScratch()
	return w.failures, nil
}

// exercise runs the whole write surface. A call that fails is counted
// and the run carries on: a live run costs a person's attention and a
// real account, so one of them should surface every problem there is,
// not the first. Only the calls that need an id a failed create never
// produced are skipped, and the skip says so.
func (w *writeRun) exercise() {
	ids := w.create()
	w.readBack(ids)
	w.organise(ids)
	w.access(ids)
	w.history(ids)
	w.collaboration(ids)
	w.resources(ids)
	w.refusals(ids)
	w.sharedDrive(ids)
}

// collaboration exercises what phase 3 added: comments, the access
// requests nobody here can create, and a recursive copy.
//
// The comment surface is the one part of this driver that leaves marks
// other people can see — everybody the file is shared with sees a
// comment — but every file it touches is inside the scratch folder, and
// the folder is trashed at the end.
func (w *writeRun) collaboration(m made) {
	// A thread on a blob and a thread on a Google document: Drive stores
	// them the same way, and that is the claim worth checking live,
	// because every other server puts comments in the Docs API.
	for _, target := range []struct{ id, what string }{{m.text, "a csv"}, {m.doc, "a Google Doc"}} {
		if target.id == "" {
			continue
		}
		fmt.Printf("\n--- comments on %s ---\n", target.what)
		w.needing("list_comments", target.id, map[string]any{"file": target.id})
		id := commentFromResult(w.call(call{tool: "add_comment", args: map[string]any{
			"file": target.id, "content": "is this row still right?",
		}}))
		if id == "" {
			w.problem("no comment id came back from add_comment, so the rest of the thread cannot run",
				errors.New("the result carried no comment id"))
			continue
		}
		w.needing("reply_comment", target.id, map[string]any{
			"file": target.id, "comment": id, "content": "checked, it is",
		})
		w.needing("reply_comment", target.id, map[string]any{
			"file": target.id, "comment": id, "action": "resolve",
		})
		// The same resolve again: it must report that nothing changed
		// rather than adding a second reply everybody can see.
		w.needing("reply_comment", target.id, map[string]any{
			"file": target.id, "comment": id, "action": "resolve",
		})
		w.needing("reply_comment", target.id, map[string]any{
			"file": target.id, "comment": id, "action": "reopen",
		})
		w.needing("reply_comment", target.id, map[string]any{
			"file": target.id, "comment": id, "action": "edit",
			"content": "is this row still right? (edited)",
		})
		w.needing("list_comments", target.id, map[string]any{"file": target.id, "include_deleted": true})
		w.expecting("reply_comment", target.id, map[string]any{
			"file": target.id, "comment": id, "action": "close", "content": "x",
		}, "an action nobody implemented")
		w.expecting("reply_comment", target.id, map[string]any{
			"file": target.id, "comment": "a-comment-that-was-never-made", "content": "x",
		}, "a comment id that names nothing")
	}
	w.expecting("list_comments", w.scratchID, map[string]any{"file": w.scratchID},
		"a folder, which Drive has no comments on")
	w.expecting("add_comment", m.text, map[string]any{"file": m.text},
		"a comment with nothing in it")

	// Access requests: nobody here can make one, so what this checks is
	// that the listing works, and that the account is told when it is
	// not an approver.
	w.needing("list_access_requests", m.text, map[string]any{"file": m.text})
	w.expecting("resolve_access_request", m.text, map[string]any{
		"file": m.text, "request": "a-request-nobody-made", "action": "accept",
	}, "an access request that is not pending")

	// The recursive copy, which is the write with the most steps: a dry
	// run first, then the copy, then a read-back of what it made.
	if m.folder == "" {
		return
	}
	w.needing("copy_file", m.folder, map[string]any{
		"file": m.folder, "to": w.scratchID, "name": "Reports copy", "recursive": true, "dry_run": true,
	})
	w.needing("copy_file", m.folder, map[string]any{
		"file": m.folder, "to": w.scratchID, "name": "Reports copy", "recursive": true,
	})
	w.needing("list_folder", w.scratchID, map[string]any{
		"folder": w.scratchID, "recursive": true, "max_depth": 4,
	})
	w.expecting("copy_file", m.folder, map[string]any{"file": m.folder, "to": w.scratchID},
		"a folder copied without recursive")
	w.expecting("copy_file", m.folder, map[string]any{
		"file": m.folder, "to": m.folder, "recursive": true,
	}, "a folder copied into itself")
	w.expecting("copy_file", m.folder, map[string]any{
		"file": m.folder, "to": w.scratchID, "recursive": true, "max_items": 9000,
	}, "a budget above the ceiling")
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

// access exercises the sharing surface. Everything happens on files
// inside the scratch folder, and every grant made here is removed again
// before the folder is trashed: a share that outlived the run would be
// the one thing trashing the folder does not undo.
//
// -share NAME@EXAMPLE.COM adds the half that needs a second person: a
// real grant to a real address, and the ownership transfer of spike F.
// Without it the run still covers the refusals, the link grant and the
// domain grant, which is most of the policy.
func (w *writeRun) access(m made) {
	w.needing("list_permissions", m.text, map[string]any{"file": m.text})

	// The acknowledgements. Both must be refused without their flag, and
	// a refusal here proves as much as a success.
	w.expecting("share_file", m.text, map[string]any{
		"file": m.text, "principal": "anyone", "role": "reader",
	}, "a public link without allow_anyone")
	w.expecting("share_file", m.text, map[string]any{
		"file": m.text, "principal": "someone@example.com", "role": "owner",
	}, "an ownership transfer without transfer_ownership")
	w.expecting("share_file", m.text, map[string]any{
		"file": m.text, "principal": "not-an-address", "role": "reader",
	}, "a principal that is not one")
	w.expecting("share_file", m.text, map[string]any{
		"file": m.text, "principal": "someone@example.com", "role": "organizer",
	}, "a shared-drive role on a file in My Drive")
	w.expecting("share_file", m.text, map[string]any{
		"file": m.text, "principal": "anyone", "role": "reader",
		"allow_anyone": true, "expires": "30d",
	}, "an expiry on a link grant, which Drive allows only for people and groups")

	// The link grant, which is the exposure worth seeing before and
	// after, then removed again.
	w.needing("share_file", m.text, map[string]any{
		"file": m.text, "principal": "anyone", "role": "reader",
		"allow_anyone": true, "dry_run": true,
	})
	w.needing("share_file", m.text, map[string]any{
		"file": m.text, "principal": "anyone", "role": "reader", "allow_anyone": true,
	})
	w.needing("list_permissions", m.text, map[string]any{"file": m.text})
	w.needing("unshare_file", m.text, map[string]any{"file": m.text, "remove_link": true})

	if w.share == "" {
		fmt.Println("\n(pass -share SOMEONE@EXAMPLE.COM to also exercise a real grant, an expiry " +
			"and the ownership transfer of spike F)")
		return
	}
	w.needing("share_file", m.text, map[string]any{
		"file": m.text, "principal": w.share, "role": "reader", "expires": "7d",
	})
	// Granting again to the same principal changes the role rather than
	// adding a second grant, which is Drive's one-per-principal rule.
	w.needing("share_file", m.text, map[string]any{
		"file": m.text, "principal": w.share, "role": "writer",
	})
	w.needing("list_permissions", m.text, map[string]any{"file": m.text})
	w.needing("unshare_file", m.text, map[string]any{"file": m.text, "principal": w.share})

	w.spikeF()
}

// spikeF is the ownership transfer, and the only call in this run whose
// effect cannot be undone from this account.
//
// It runs on a file created for it, never on one the rest of the run
// needs. Afterwards this account is a writer rather than the owner, and
// Drive moves the file to the new owner's root — so it leaves the
// scratch folder, and trashing that folder will not take it.
//
// The transfer is READ BACK. Two earlier runs of this driver reported
// "all calls behaved as expected" while three results were wrong,
// because a call succeeding and a call telling the truth are different
// questions. For a transfer the second question is the only one that
// matters, and the answer is in the owner line, not the status.
func (w *writeRun) spikeF() {
	transfer := w.createAndKeepID("create_file", map[string]any{
		"name": "ownership transfer probe", "parent": w.scratchID,
		"content": "spike F: this file is about to change hands\n", "mime_type": "text/plain",
	})
	if transfer == "" {
		fmt.Println("\n=== spike F: skipped, the file it needs was never created ===")
		return
	}
	before := ownerFromResult(w.call(call{tool: "get_file", args: map[string]any{"file": transfer}}))

	fmt.Println("\n!! the next call hands this file to " + w.share + " for good.")
	w.needing("share_file", transfer, map[string]any{
		"file": transfer, "principal": w.share, "role": "owner", "transfer_ownership": true,
	})

	// The proof. A transfer that answers 200 and leaves the owner where
	// it was would look identical to one that worked, from the result
	// alone.
	after := ownerFromResult(w.call(call{tool: "get_file", args: map[string]any{"file": transfer}}))
	w.call(call{tool: "list_permissions", args: map[string]any{"file": transfer}})

	switch after {
	case "":
		w.problem("the file could not be read back after the transfer, so whether ownership actually "+
			"moved is unknown. Open it in Drive and look", errors.New("no owner line in the result"))
	case before:
		w.problem("share_file reported an ownership transfer and the owner DID NOT CHANGE. That is the "+
			"failure reading it back exists to catch", errors.New("the owner is the same as before"))
	default:
		fmt.Println("\n(spike F: the owner changed, so the transfer really happened. " +
			"This account is a writer on it now.)")
	}
	fmt.Println("!! that file now belongs to " + w.share + " and has moved to their Drive. Trashing the " +
		"scratch folder does not take it back: only its new owner can remove it.")
}

// ownerFromResult reads the "owner:" line of a file card, before
// redaction, so a transfer can be compared with what came before it. The
// value is never printed: only whether it changed.
func ownerFromResult(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "owner: "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// history exercises the version history and the changes feed. The feed
// is the one part that reports on the whole account rather than the
// scratch folder, so it is read but never acted on.
func (w *writeRun) history(m made) {
	w.needing("list_revisions", m.text, map[string]any{"file": m.text})
	w.needing("list_revisions", m.doc, map[string]any{"file": m.doc})
	w.expecting("list_revisions", w.scratchID, map[string]any{"file": w.scratchID},
		"a folder has no content and so no versions")

	// A pin, then the same call again, which must report that nothing
	// changed rather than a write that did not happen.
	if rev := w.firstRevision(m.text); rev != "" {
		w.needing("manage_revision", m.text, map[string]any{
			"file": m.text, "revision": rev, "action": "keep",
		})
		w.needing("manage_revision", m.text, map[string]any{
			"file": m.text, "revision": rev, "action": "keep",
		})
		w.needing("manage_revision", m.text, map[string]any{
			"file": m.text, "revision": rev, "action": "unkeep",
		})
	}
	w.expecting("manage_revision", m.text, map[string]any{
		"file": m.text, "revision": "a-revision-that-was-never-current", "action": "keep",
	}, "a revision id that names nothing")

	// The feed: a starting point, then the changes this run has made
	// since it. Everything above happened before the token, so the
	// second call should be quiet — which is itself worth seeing.
	token := tokenFromResult(w.call(call{tool: "list_changes", args: map[string]any{}}))
	w.needing("update_file", m.text, map[string]any{"file": m.text, "starred": false})
	if token != "" {
		w.pollChanges(token)
	}
	w.call(call{tool: "list_changes", args: map[string]any{"page_token": "not-a-real-token"},
		expectError: true, why: "a page token from no feed at all"})
}

// pollChanges reads the feed until it reports the change this run just
// made, or gives up and says which happened.
//
// Drive's changes feed is eventually consistent: two live runs asked for
// it about a second after a write and were told "0 changes", which reads
// as a working feed reporting nothing and is indistinguishable from a
// broken one. Waiting is what tells those apart, and a run that gives up
// says so rather than printing an empty answer and moving on.
func (w *writeRun) pollChanges(token string) {
	const attempts = 6
	for i := range attempts {
		if i > 0 {
			time.Sleep(2 * time.Second)
		}
		out := w.call(call{tool: "list_changes", args: map[string]any{"page_token": token, "limit": 20}})
		if !strings.Contains(out, "0 changes") {
			return
		}
		fmt.Printf("(the feed reports nothing yet; it is eventually consistent, waiting — attempt %d of %d)\n",
			i+1, attempts)
	}
	w.problem("the changes feed never reported the write this run made. That is either Drive's own lag "+
		"or a real fault, and this run cannot tell which: check it by hand before trusting list_changes",
		errors.New("the feed stayed empty for "+fmt.Sprint(attempts*2)+" seconds"))
}

// firstRevision reads a revision id out of a list_revisions result, so
// the pin has something real to act on.
//
// A row of that listing is "<id>  <date> ...", and the date is what
// identifies it: the lines around it are a subject line and prose, and
// neither has a second field beginning with a four-digit year.
func (w *writeRun) firstRevision(id string) string {
	if id == "" {
		return ""
	}
	out, isError, err := w.sess.CallTool("list_revisions", map[string]any{"file": id})
	if err != nil || isError {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && looksLikeDate(fields[1]) {
			return fields[0]
		}
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

// made are the ids the create phase produced. An empty one means that
// create failed, and the calls needing it say they were skipped.
type made struct {
	folder, doc, sheet, text, blob, small string
}

// create makes one of everything, because several of Drive's rules turn
// out to be per format rather than per kind of thing.
func (w *writeRun) create() made {
	var m made
	m.folder = w.createAndKeepID("create_folder", map[string]any{
		"name": "Reports", "parent": w.scratchID, "description": "made by the live driver",
	})
	m.doc = w.createAndKeepID("create_file", map[string]any{
		"name": "Notes", "kind": "doc", "parent": w.scratchID,
	})
	m.text = w.createAndKeepID("create_file", map[string]any{
		"name": "rows.csv", "parent": w.scratchID,
		"content": "name,amount\nfirst,1\nsecond,2\n", "mime_type": "text/csv",
	})
	// The import path: markdown becomes a formatted Google Doc.
	w.createAndKeepID("create_file", map[string]any{
		"name": "Imported notes", "parent": w.scratchID,
		"content": "# Heading\n\nA paragraph.\n", "mime_type": "text/markdown", "convert_to": "doc",
	})
	// One of every Google kind, because the id rule is per format and a
	// run that tries one of five proves one of five.
	for _, kind := range []string{"sheet", "slides", "drawing", "form"} {
		id := w.createAndKeepID("create_file", map[string]any{
			"name": "New " + kind, "kind": kind, "parent": w.scratchID,
		})
		if kind == "sheet" {
			m.sheet = id
		}
	}
	// A duplicate name, asked for explicitly: the guard has to be a
	// guard and not a wall.
	w.createAndKeepID("create_folder", map[string]any{
		"name": "Reports", "parent": w.scratchID, "allow_duplicate": true,
	})

	// Both upload paths. The small one goes in a single request; the
	// large one is chunked and is the half no fake can prove.
	small := filepath.Join(w.dir, "livedrive-small.csv")
	if err := os.WriteFile(small, []byte("name,amount\nfirst,1\n"), 0o600); err != nil {
		w.problem("could not write the small file to upload", err)
	} else {
		defer func() { _ = os.Remove(small) }()
		m.small = w.createAndKeepID("upload_file", map[string]any{
			"local_path": "livedrive-small.csv", "parent": w.scratchID, "name": "small upload.csv",
		})
	}
	big := filepath.Join(w.dir, "livedrive-upload.bin")
	if err := writeFiller(big, 6<<20); err != nil {
		w.problem("could not write the file to upload", err)
	} else {
		defer func() { _ = os.Remove(big) }()
		m.blob = w.createAndKeepID("upload_file", map[string]any{
			"local_path": "livedrive-upload.bin", "parent": w.scratchID, "name": "large upload.bin",
		})
	}
	return m
}

// readBack asks for everything that was just written, which is the only
// way to see that a write did what its result claimed.
func (w *writeRun) readBack(m made) {
	w.needing("get_file", m.text, map[string]any{"file": m.text})
	w.needing("get_file", m.doc, map[string]any{"file": m.doc})
	w.call(call{tool: "list_folder", args: map[string]any{"folder": w.scratchID}})
	w.call(call{tool: "list_folder", args: map[string]any{
		"folder": w.scratchID, "recursive": true, "max_depth": 3,
	}})
	w.call(call{tool: "search_files", args: map[string]any{"in_folder": w.scratchID, "kind": "any"}})
	w.call(call{tool: "search_files", args: map[string]any{"in_folder": w.scratchID, "name": "rows"}})

	w.needing("read_file", m.text, map[string]any{"file": m.text})
	// A window, then the continuation it offered: paging is the part a
	// model actually has to use, and an offset that does not line up is
	// invisible until someone follows it.
	w.needing("read_file", m.text, map[string]any{"file": m.text, "max_chars": 12})
	w.needing("read_file", m.text, map[string]any{"file": m.text, "offset": 12, "max_chars": 12})
	w.needing("read_file", m.text, map[string]any{"file": m.text, "offset": 9000})
	w.needing("read_file", m.doc, map[string]any{"file": m.doc})
	w.needing("read_file", m.sheet, map[string]any{"file": m.sheet})
	w.needing("read_file", m.sheet, map[string]any{"file": m.sheet, "format": "tsv"})

	w.needing("download_file", m.text, map[string]any{"file": m.text})
	w.needing("download_file", m.doc, map[string]any{"file": m.doc, "format": "pdf"})
	w.needing("download_file", m.doc, map[string]any{"file": m.doc})
	w.needing("download_file", m.sheet, map[string]any{"file": m.sheet})
	w.needing("download_file", m.blob, map[string]any{"file": m.blob})
}

// organise runs everything that changes a file without replacing it.
func (w *writeRun) organise(m made) {
	w.needing("update_content", m.text, map[string]any{
		"file": m.text, "content": "name,amount\nfirst,10\n", "keep_previous_revision": true,
	})
	// The other half of update_content: new bytes from a local file.
	replacement := filepath.Join(w.dir, "livedrive-replacement.csv")
	if err := os.WriteFile(replacement, []byte("name,amount\nfirst,100\nsecond,200\n"), 0o600); err != nil {
		w.problem("could not write the replacement file", err)
	} else {
		defer func() { _ = os.Remove(replacement) }()
		w.needing("update_content", m.small, map[string]any{
			"file": m.small, "local_path": "livedrive-replacement.csv",
		})
	}

	w.needing("update_file", m.text, map[string]any{
		"file": m.text, "name": "rows renamed.csv", "starred": true,
		"properties": map[string]any{"livedrive": "yes"},
	})
	// An empty value deletes a property, and an empty description clears
	// it: the two places where "" means something.
	w.needing("update_file", m.text, map[string]any{
		"file": m.text, "description": "written by the live driver",
	})
	w.needing("update_file", m.text, map[string]any{
		"file": m.text, "description": "", "properties": map[string]any{"livedrive": ""},
	})
	// A patch that changes nothing has to say so rather than reporting a
	// write that did not happen.
	w.needing("update_file", m.text, map[string]any{"file": m.text, "name": "rows renamed.csv"})
	w.needing("update_file", m.folder, map[string]any{"file": m.folder, "color": "#4986e7"})

	if m.text != "" && m.folder != "" {
		w.call(call{tool: "move_file", args: map[string]any{"file": m.text, "to": m.folder, "dry_run": true}})
		w.call(call{tool: "move_file", args: map[string]any{"file": m.text, "to": m.folder}})
		// Moving it where it already is changes nothing, and says so.
		w.call(call{tool: "move_file", args: map[string]any{"file": m.text, "to": m.folder}})
	}

	w.needing("copy_file", m.text, map[string]any{"file": m.text, "name": "rows copy.csv", "to": w.scratchID})
	// Copying a Google Doc is the case where the copy is a Doc too, and
	// so cannot carry a generated id either.
	w.needing("copy_file", m.doc, map[string]any{"file": m.doc, "name": "Notes copy", "to": w.scratchID})
	// The import route: a csv becomes a Sheet, which is the conversion
	// Drive offers for it. Asking for a Doc is refused before the call,
	// with what it can become instead.
	w.needing("copy_file", m.text, map[string]any{
		"file": m.text, "name": "rows as a sheet", "to": w.scratchID, "convert_to": "sheet",
	})
	w.expecting("copy_file", m.text, map[string]any{
		"file": m.text, "name": "rows as a doc", "to": w.scratchID, "convert_to": "doc",
	}, "Google imports a csv as a Sheet, not as a Doc")
	w.needing("create_shortcut", m.text, map[string]any{
		"target": m.text, "parent": w.scratchID, "name": "rows shortcut",
	})

	w.needing("trash_file", m.text, map[string]any{"file": m.text, "dry_run": true})
	w.needing("trash_file", m.text, map[string]any{"file": m.text})
	w.needing("restore_file", m.text, map[string]any{"file": m.text})
	// Restoring what is not in the trash changes nothing.
	w.needing("restore_file", m.text, map[string]any{"file": m.text})
}

// refusals are the calls that must not work. A refusal that is expected
// proves as much as a success: it is how the server says no to something
// it should not do.
func (w *writeRun) refusals(m made) {
	w.call(call{tool: "create_folder", args: map[string]any{"name": "Reports", "parent": w.scratchID},
		expectError: true, why: "two folders of that name are already there, so the guard cannot pick one"})
	w.call(call{tool: "upload_file", args: map[string]any{"local_path": "/etc/hostname"},
		expectError: true, why: "a path outside the local directory"})
	w.call(call{tool: "upload_file", args: map[string]any{"local_path": "../../etc/hostname"},
		expectError: true, why: "a relative path that climbs out of it"})
	w.call(call{tool: "read_file", args: map[string]any{"file": w.scratchID},
		expectError: true, why: "a folder has no text"})
	w.expecting("read_file", m.blob, map[string]any{"file": m.blob},
		"a binary file is not text, and the refusal names the two ways forward")
	w.expecting("update_content", m.doc, map[string]any{"file": m.doc, "content": "no"},
		"a Google Doc's content belongs to the Docs API")
	w.expecting("update_content", m.text, map[string]any{
		"file": m.text, "content": "no", "expect_head_revision": "a-revision-that-was-never-current",
	}, "the file is not at the revision the caller expected")
	w.expecting("copy_file", m.folder, map[string]any{"file": m.folder},
		"Drive has no copy for a folder")
	w.expecting("update_file", m.text, map[string]any{"file": m.text, "color": "#4986e7"},
		"a colour belongs to a folder")
	w.expecting("download_file", m.text, map[string]any{"file": m.text, "format": "docx"},
		"format applies to a Google document, not to a file with bytes of its own")
}

// sharedDrive is the half of the shared-drive work that writes: moving a
// file in and out, and the refusal that a My Drive folder cannot follow
// it. It is opt-in (-drive) because it is the only part of this run that
// touches anything outside the scratch folder.
func (w *writeRun) sharedDrive(m made) {
	if w.drive == "" {
		fmt.Println("\n(pass -drive NAME_OR_ID to also exercise moving a file into a shared drive and back)")
		return
	}
	if m.small == "" || m.folder == "" {
		fmt.Println("\n=== shared drive: skipped, the files it needs were never created ===")
		return
	}
	target := "drive:" + w.drive
	w.call(call{tool: "move_file", args: map[string]any{"file": m.small, "to": target, "dry_run": true}})
	if out := w.call(call{tool: "move_file", args: map[string]any{"file": m.small, "to": target}}); out != "" {
		// The file is now outside the scratch folder, so trashing the
		// scratch folder will not take it. Bringing it back is the only
		// cleanup there is, and a failure here is the one thing this run
		// can leave behind in somebody's shared drive: say so loudly,
		// with the id, rather than counting it and moving on.
		w.recover(m.small)
	}
	w.call(call{tool: "move_file", args: map[string]any{"file": m.folder, "to": target},
		expectError: true, why: "a My Drive folder cannot move into a shared drive"})
}

// recover brings a file back out of the shared drive. It is the only
// cleanup in this run that is not covered by trashing the scratch
// folder, so it says exactly what to do if it fails.
func (w *writeRun) recover(id string) {
	back := w.call(call{tool: "move_file", args: map[string]any{"file": id, "to": w.scratchID}})
	if strings.Contains(back, "moved:") {
		return
	}
	// A second attempt: the usual reason is a rate limit, and this is
	// worth one retry before asking a person to do it by hand.
	if again := w.call(call{tool: "move_file", args: map[string]any{"file": id, "to": w.scratchID}}); strings.Contains(again, "moved:") {
		return
	}
	w.problem("A FILE IS STILL IN THE SHARED DRIVE "+w.drive+
		" and this run cannot get it back. Its id is in the result above; move or trash it by hand",
		errors.New("the move back out failed twice"))
}

// problem records something that went wrong outside a tool call.
func (w *writeRun) problem(what string, err error) {
	fmt.Printf("!! %s: %v\n", what, err)
	w.failures++
}

// needing runs a call that depends on an id an earlier create produced,
// and says so when it cannot.
func (w *writeRun) needing(tool, id string, args map[string]any) {
	if id == "" {
		fmt.Printf("\n=== %s: skipped, the file it needs was never created ===\n", tool)
		return
	}
	w.call(call{tool: tool, args: args})
}

// expecting is needing for a call that ought to be refused.
func (w *writeRun) expecting(tool, id string, args map[string]any, why string) {
	if id == "" {
		fmt.Printf("\n=== %s: skipped, the file it needs was never created ===\n", tool)
		return
	}
	w.call(call{tool: tool, args: args, expectError: true, why: why})
}

// trashScratch removes the folder and everything the run put in it.
func (w *writeRun) trashScratch() {
	if w.scratchID == "" {
		return
	}
	w.call(call{tool: "trash_file", args: map[string]any{"file": w.scratchID}})
	fmt.Println("\n(the scratch folder is in the trash; empty it yourself if you want it gone for good)")
}

// call runs one tool and prints the redacted result.
func (w *writeRun) call(c call) string {
	fmt.Printf("\n=== %s %s ===\n", c.tool, mcpstdio.Encode(c.args))
	if c.why != "" {
		fmt.Printf("(expecting a refusal: %s)\n", c.why)
	}
	out, isError, err := w.sess.CallTool(c.tool, c.args)
	if err != nil {
		fmt.Println("!! transport failure:", err)
		w.failures++
		return ""
	}
	fmt.Println(strings.TrimRight(w.red.Do(out), "\n"))
	if isError != c.expectError {
		w.failures++
		if c.expectError {
			fmt.Println("!! expected a refusal and did not get one")
		} else {
			fmt.Println("!! unexpected tool error")
		}
	}
	return out
}

// createAndKeepID runs a create and pulls the new id out of the result,
// because the calls after it need one. A create that failed returns "",
// and the calls that needed it say they were skipped rather than the run
// stopping there. The id is read before redaction and never printed.
func (w *writeRun) createAndKeepID(tool string, args map[string]any) string {
	return mcpstdio.IDIn(w.call(call{tool: tool, args: args}))
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

// resources reads the three gdrive:// templates, which is the half of
// phase 3's surface no tool call reaches. A resource read is a different
// method with a different failure shape, so a driver that only calls
// tools would never have found out whether they work at all.
func (w *writeRun) resources(m made) {
	for _, r := range []struct{ id, suffix, why string }{
		{m.text, "", ""},
		{m.text, "meta", ""},
		{w.scratchID, "children", ""},
		{w.scratchID, "", "a folder has no text of its own"},
		{m.text, "children", "a file is not a folder"},
		{"1SyntheticFixtureFileIdAAAAAAAAAAAA", "", "an id that names nothing"},
	} {
		// The same guard every other call in this file gets from
		// needing: an id an earlier create never produced is a skip that
		// says so, not a silent one.
		if r.id == "" {
			fmt.Println("\n=== resource: skipped, the file it needs was never created ===")
			continue
		}
		uri := service.ResourceURI(r.id, r.suffix)
		fmt.Printf("\n=== resource %s ===\n", w.red.Do(uri))
		if r.why != "" {
			fmt.Printf("(expecting a refusal: %s)\n", r.why)
		}
		text, mime, err := w.sess.ReadResource(uri)
		var refused *mcpstdio.RPCError
		switch {
		case errors.As(err, &refused):
			fmt.Println(w.red.Do(refused.Detail))
			if r.why == "" {
				fmt.Println("!! unexpected refusal")
				w.failures++
			}
			continue
		case err != nil:
			fmt.Println("!! transport failure:", err)
			w.failures++
			continue
		}
		if r.why != "" {
			fmt.Println("!! expected a refusal and did not get one")
			w.failures++
		}
		fmt.Printf("mime: %s\n%s\n", mime, strings.TrimRight(w.red.Do(head(text, 12)), "\n"))
	}
}

// head keeps the first n lines, so a transcript shows the shape of a
// resource without printing a whole file.
func head(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= n {
		return text
	}
	return strings.Join(lines[:n], "\n") + "\n… (" + fmt.Sprint(len(lines)-n) + " more lines)"
}
