package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// writeRun exercises every tool that changes Drive, inside one scratch
// folder it makes and trashes again. Nothing outside that folder is
// touched: the folder is created first, every call names it or something
// inside it, and the last call trashes it whole.
//
// It is opt-in (-write) because it is the only part of this driver that
// changes anything in a real account.
type writeRun struct {
	session *session
	redact  *Redactor
	// dir is the local directory the server was told to use, and the one
	// place uploads come from and downloads land in.
	dir string
	// scratchID is the folder everything happens in.
	scratchID string
	// drive is the shared drive to move a file through, or empty.
	drive string
	// failures counts calls that did not behave as expected.
	failures int
}

// scratchPrefix names the folder this driver works in, so a folder left
// behind by an interrupted run is recognisable and safe to remove.
const scratchPrefix = "google-drive-mcp livedrive scratch"

// runWrites drives the whole write surface and reports how many calls
// behaved unexpectedly.
func runWrites(s *session, redact *Redactor, dir, parent, drive string) (int, error) {
	w := &writeRun{session: s, redact: redact, dir: dir, drive: drive}
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
	w.refusals(ids)
	w.sharedDrive(ids)
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
		fmt.Println("\n(pass -drive NAME to also exercise moving a file into a shared drive and back)")
		return
	}
	if m.small == "" || m.folder == "" {
		fmt.Println("\n=== shared drive: skipped, the files it needs were never created ===")
		return
	}
	target := "drive:" + w.drive
	w.call(call{tool: "move_file", args: map[string]any{"file": m.small, "to": target, "dry_run": true}})
	w.call(call{tool: "move_file", args: map[string]any{"file": m.small, "to": target}})
	// And back, so the run leaves nothing behind in the drive.
	w.call(call{tool: "move_file", args: map[string]any{"file": m.small, "to": w.scratchID}})
	w.call(call{tool: "move_file", args: map[string]any{"file": m.folder, "to": target},
		expectError: true, why: "a My Drive folder cannot move into a shared drive"})
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
	fmt.Printf("\n=== %s %s ===\n", c.tool, encode(c.args))
	if c.why != "" {
		fmt.Printf("(expecting a refusal: %s)\n", c.why)
	}
	out, isError, err := w.session.callTool(c.tool, c.args)
	if err != nil {
		fmt.Println("!! transport failure:", err)
		w.failures++
		return ""
	}
	fmt.Println(strings.TrimRight(w.redact.Do(out), "\n"))
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
	return idFromResult(w.call(call{tool: tool, args: args}))
}

// idFromResult reads the "id:" line of a file card.
func idFromResult(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "id: "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
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
