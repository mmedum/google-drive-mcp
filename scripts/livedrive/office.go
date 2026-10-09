package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mmedum/google-drive-mcp/v2/internal/office/officetest"
)

// padTo is how far padded moves the parts after the padding: past the
// first of the 64 KiB blocks an Office read fetches, so a read of the
// file has to ask Drive for byte ranges well past its start.
const padTo = 96 << 10

// officeFiles uploads a Word document, an Excel workbook and a
// PowerPoint deck this driver builds itself, with invented text, and
// reads each back. What it settles is the part no fake can: that Drive
// answers the byte ranges an Office read makes, many of them in quick
// succession, with exactly those bytes. Each read is checked for the
// text that went in, because a read that succeeds with the wrong text
// is the failure that matters here.
//
// The workbook and the deck are padded past 96 KiB, with an unused
// entry in the middle of the archive, so their reads fetch ranges past
// the first block; the document stays small, the one-block case. Each
// file is also read in two windows, which must join to the whole text.
func (w *writeRun) officeFiles() {
	w.out.Say("\n--- Office files read here, through byte ranges ---")
	files := []struct {
		local, name string
		data        []byte
		reads       []map[string]any
		want        []string
	}{
		{
			local: "livedrive-notes.docx", name: "Driver notes.docx",
			data: officetest.Docx(officetest.WordParagraph("Heading1", "Driver heading") +
				officetest.WordParagraph("", "A sentence the driver wrote.") +
				officetest.WordListItem(0, "first point") +
				officetest.WordTable([]string{"left", "right"})),
			want: []string{"# Driver heading", "- first point", "| left | right |"},
		},
		{
			local: "livedrive-rows.xlsx", name: "Driver rows.xlsx",
			data: padded(officetest.Xlsx(officetest.Workbook{CellXfs: `<xf numFmtId="0"/><xf numFmtId="14"/>`,
				Sheets: []officetest.Sheet{
					{Name: "Totals", Data: officetest.Row(1, "item", "count", "when") +
						`<row r="2"><c r="A2" t="inlineStr"><is><t>bolts, small</t></is></c><c r="B2"><v>12</v></c>` +
						`<c r="C2" s="1"><v>45000</v></c></row>`},
					{Name: "Other", Data: officetest.Row(1, "not read")},
				}}), "xl/worksheets/sheet1.xml"),
			reads: []map[string]any{{"format": "tsv"}},
			want:  []string{"item,count,when", `"bolts, small",12,2023-03-15`},
		},
		{
			local: "livedrive-deck.pptx", name: "Driver deck.pptx",
			data: padded(officetest.Pptx([]string{"Opening slide"}, []string{"Closing slide", "with two lines"}),
				"ppt/slides/slide1.xml"),
			want: []string{"--- slide 1 ---", "Opening slide", "--- slide 2 ---", "with two lines"},
		},
	}
	for _, f := range files {
		path := filepath.Join(w.dir, f.local)
		if err := os.WriteFile(path, f.data, 0o600); err != nil {
			w.problem("could not write "+f.local+" to upload", err)
			continue
		}
		id := w.createAndKeepID("upload_file", map[string]any{
			"local_path": f.local, "parent": w.scratchID, "name": f.name,
		})
		_ = os.Remove(path)
		if id == "" {
			w.out.Sayf("\n=== read_file: skipped, %s was never uploaded ===", f.name)
			continue
		}
		whole := w.call(call{tool: "read_file", args: map[string]any{"file": id}})
		for _, want := range f.want {
			if !strings.Contains(whole, want) {
				w.failures++
				w.out.Sayf("!! the text read back from %s does not contain %q", f.name, want)
			}
		}
		w.windows(id, f.name, textOf(whole))
		for _, extra := range f.reads {
			args := map[string]any{"file": id}
			for k, v := range extra {
				args[k] = v
			}
			w.call(call{tool: "read_file", args: args})
		}
	}
}

// windows reads a file in two windows, cut 20 bytes in, and checks they
// join to the whole text. The first window is cut to its 20 bytes: the
// read ends a window that does not end a line with a newline of its own.
func (w *writeRun) windows(id, name, whole string) {
	const cut = 20
	if len(whole) <= cut {
		return
	}
	first := w.call(call{tool: "read_file", args: map[string]any{"file": id, "max_chars": cut}})
	rest := w.call(call{tool: "read_file", args: map[string]any{"file": id, "offset": cut, "max_chars": 100000}})
	head := textOf(first)
	if len(head) > cut {
		head = head[:cut]
	}
	if joined := head + textOf(rest); joined != whole {
		w.failures++
		w.out.Sayf("!! the two windows of %s do not join to the whole text: %d bytes against %d",
			name, len(joined), len(whole))
	}
}

// textOf is the text a read returns, below the line that ends its
// header.
func textOf(out string) string {
	_, text, _ := strings.Cut(out, "\n---\n")
	return text
}

// padded is an Office file with an unused entry of padTo bytes, stored
// uncompressed, after the entry named after. Nothing refers to it, so a
// reader skips it; what it changes is where the entries after it sit.
func padded(file []byte, after string) []byte {
	zr, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		panic(err) // the file was built here a moment ago
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		if err := zw.Copy(f); err != nil {
			panic(err)
		}
		if f.Name != after {
			continue
		}
		pad, err := zw.CreateHeader(&zip.FileHeader{Name: "livedrive/padding.bin", Method: zip.Store})
		if err != nil {
			panic(err)
		}
		if _, err := io.CopyN(pad, filler{}, padTo); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// filler reads as an endless run of one invented line.
type filler struct{}

func (filler) Read(p []byte) (int, error) {
	const line = "livedrive padding, not part of the document\n"
	for i := range p {
		p[i] = line[i%len(line)]
	}
	return len(p), nil
}
