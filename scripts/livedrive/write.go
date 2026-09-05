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
	// failures counts calls that did not behave as expected.
	failures int
}

// scratchPrefix names the folder this driver works in, so a folder left
// behind by an interrupted run is recognisable and safe to remove.
const scratchPrefix = "google-drive-mcp livedrive scratch"

// runWrites drives the whole write surface and reports how many calls
// behaved unexpectedly.
func runWrites(s *session, redact *Redactor, dir, parent string) (int, error) {
	w := &writeRun{session: s, redact: redact, dir: dir}
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
	folderID := w.createAndKeepID("create_folder", map[string]any{
		"name": "Reports", "parent": w.scratchID, "description": "made by the live driver",
	})
	docID := w.createAndKeepID("create_file", map[string]any{
		"name": "Notes", "kind": "doc", "parent": w.scratchID,
	})
	textID := w.createAndKeepID("create_file", map[string]any{
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
		w.createAndKeepID("create_file", map[string]any{
			"name": "New " + kind, "kind": kind, "parent": w.scratchID,
		})
	}

	// A local file large enough to force the resumable path, which is
	// the half of upload_file no fake can prove.
	big := filepath.Join(w.dir, "livedrive-upload.bin")
	if err := writeFiller(big, 6<<20); err != nil {
		fmt.Println("!! could not write the file to upload:", err)
		w.failures++
	} else {
		defer func() { _ = os.Remove(big) }()
		w.createAndKeepID("upload_file", map[string]any{
			"local_path": "livedrive-upload.bin", "parent": w.scratchID, "name": "large upload.bin",
		})
	}

	w.needing("read_file", textID, map[string]any{"file": textID})
	w.needing("read_file", textID, map[string]any{"file": textID, "max_chars": 12})
	w.needing("read_file", docID, map[string]any{"file": docID})
	w.needing("download_file", textID, map[string]any{"file": textID})
	w.needing("download_file", docID, map[string]any{"file": docID, "format": "pdf"})

	w.needing("update_content", textID, map[string]any{
		"file": textID, "content": "name,amount\nfirst,10\n", "keep_previous_revision": true,
	})
	w.expecting("update_content", docID, map[string]any{"file": docID, "content": "no"},
		"a Google Doc's content belongs to the Docs API")

	w.needing("update_file", textID, map[string]any{
		"file": textID, "name": "rows renamed.csv", "starred": true,
		"properties": map[string]any{"livedrive": "yes"},
	})
	w.needing("update_file", folderID, map[string]any{"file": folderID, "color": "#4986e7"})

	if textID != "" && folderID != "" {
		w.call(call{tool: "move_file", args: map[string]any{"file": textID, "to": folderID, "dry_run": true}})
		w.call(call{tool: "move_file", args: map[string]any{"file": textID, "to": folderID}})
	}

	w.needing("copy_file", textID, map[string]any{"file": textID, "name": "rows copy.csv", "to": w.scratchID})
	// Copying a Google Doc is the case where the copy is a Doc too, and
	// so cannot carry a generated id either.
	w.needing("copy_file", docID, map[string]any{"file": docID, "name": "Notes copy", "to": w.scratchID})
	w.needing("create_shortcut", textID, map[string]any{
		"target": textID, "parent": w.scratchID, "name": "rows shortcut",
	})

	w.call(call{tool: "create_folder", args: map[string]any{"name": "Reports", "parent": w.scratchID},
		expectError: true, why: "a second folder of the same name needs allow_duplicate"})
	w.call(call{tool: "upload_file", args: map[string]any{"local_path": "/etc/hostname"},
		expectError: true, why: "a path outside the local directory"})
	w.call(call{tool: "read_file", args: map[string]any{"file": w.scratchID},
		expectError: true, why: "a folder has no text"})

	w.needing("trash_file", textID, map[string]any{"file": textID, "dry_run": true})
	w.needing("trash_file", textID, map[string]any{"file": textID})
	w.needing("restore_file", textID, map[string]any{"file": textID})
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
