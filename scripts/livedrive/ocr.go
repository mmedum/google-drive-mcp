package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ocrPDF is the scan this driver uploads: a one-page PDF written by hand
// below, with invented text set large, since Google asks for text at
// least ten pixels high.
const ocrPDF = "livedrive-receipt.pdf"

// ocrWords are words the OCR has to find in it.
var ocrWords = []string{"RECEIPT", "42"}

// keptCopyID reads the kept copy's id out of extract_text's note, which
// names it as `"<name>", id <id>, in the root of My Drive`.
var keptCopyID = regexp.MustCompile(`, id ([A-Za-z0-9_\-]+), in the root of My Drive`)

// ocr runs Google's OCR live, which nothing here had ever done: through
// extract_text, which makes a temporary Google Doc and deletes it, and
// through the two imports that take an ocr_language. Every belief §18
// records as unverified about it is checked here or nowhere.
func (w *writeRun) ocr() {
	w.out.Say("\n--- Google's OCR: extract_text, and the imports that take ocr_language ---")
	path := filepath.Join(w.dir, ocrPDF)
	if err := os.WriteFile(path, handWrittenPDF("DRIVER RECEIPT", "TOTAL 42"), 0o600); err != nil {
		w.problem("could not write the PDF to upload", err)
		return
	}
	defer func() { _ = os.Remove(path) }()
	scan := w.createAndKeepID("upload_file", map[string]any{
		"local_path": ocrPDF, "parent": w.scratchID, "name": "Driver receipt.pdf",
	})
	if scan == "" {
		w.out.Say("\n=== extract_text: skipped, the PDF was never uploaded ===")
		return
	}
	w.expecting("read_file", scan, map[string]any{"file": scan},
		"a PDF is not text, and the refusal names extract_text")

	whole := w.call(call{tool: "extract_text", args: map[string]any{"file": scan}})
	w.ocrFound("extract_text", whole)
	if !strings.Contains(whole, "deleted for good") {
		w.failures++
		w.out.Say("!! the result does not say the temporary copy was deleted")
	}
	// A language hint, and a window of the text, then the next window,
	// which reuses the text rather than making a second copy.
	w.call(call{tool: "extract_text", args: map[string]any{
		"file": scan, "ocr_language": "en", "max_chars": 8,
	}})
	next := w.call(call{tool: "extract_text", args: map[string]any{
		"file": scan, "ocr_language": "en", "offset": 8, "max_chars": 200,
	}})
	if !strings.Contains(next, "no copy was made this time") {
		w.failures++
		w.out.Say("!! the second window made a second copy")
	}
	// Whether a temporary copy is left anywhere: the search index lags,
	// so an empty answer is the expected one and a hit is a finding.
	w.call(call{tool: "search_files", args: map[string]any{
		"name": "Temporary copy of Driver receipt", "in_folder": "root",
	}, tolerant: true})

	kept := w.call(call{tool: "extract_text", args: map[string]any{"file": scan, "keep_copy": true}})
	if m := keptCopyID.FindStringSubmatch(kept); len(m) == 2 {
		// The kept copy is in the root of My Drive, outside the scratch
		// folder: it is moved in, so trashing the folder takes it too.
		w.call(call{tool: "move_file", args: map[string]any{"file": m[1], "to": w.scratchID}})
		w.call(call{tool: "read_file", args: map[string]any{"file": m[1]}})
	} else {
		w.failures++
		w.out.Say("!! the kept copy's id is not in the result, so it stays in the root of My Drive; remove it by hand")
	}

	// The imports that take a language hint, which closes the two
	// options that were undrivable while every fixture was typed text.
	copied := w.createAndKeepID("copy_file", map[string]any{
		"file": scan, "convert_to": "doc", "ocr_language": "en", "to": w.scratchID, "name": "Driver receipt, imported",
	})
	if copied != "" {
		w.ocrFound("read_file of the copy_file import", w.call(call{tool: "read_file", args: map[string]any{"file": copied}}))
	}
	uploaded := w.createAndKeepID("upload_file", map[string]any{
		"local_path": ocrPDF, "parent": w.scratchID, "name": "Driver receipt, uploaded as a Doc",
		"convert_to": "doc", "ocr_language": "en",
	})
	if uploaded != "" {
		w.ocrFound("read_file of the upload_file import", w.call(call{tool: "read_file", args: map[string]any{"file": uploaded}}))
	}
}

// ocrFound checks that the OCR read the words the PDF carries.
func (w *writeRun) ocrFound(what, out string) {
	upper := strings.ToUpper(out)
	for _, word := range ocrWords {
		if !strings.Contains(upper, word) {
			w.failures++
			w.out.Sayf("!! %s does not carry %q, which the PDF says in 36-point Helvetica", what, word)
		}
	}
}

// handWrittenPDF is a one-page PDF of the given lines, in Helvetica at
// 36 points, with its cross-reference table worked out byte by byte.
func handWrittenPDF(lines ...string) []byte {
	var content strings.Builder
	content.WriteString("BT /F1 36 Tf 72 700 Td 48 TL\n")
	for _, l := range lines {
		l = strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(l)
		fmt.Fprintf(&content, "(%s) Tj T*\n", l)
	}
	content.WriteString("ET")
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
			"/Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", content.Len(), content.String()),
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, o := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	// Every cross-reference entry is exactly twenty bytes, its line end
	// included, which is why the free entry ends in a space.
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return b.Bytes()
}
