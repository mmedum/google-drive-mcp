package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mmedum/google-drive-mcp/v2/internal/office/officetest"
)

// officeFiles uploads a Word document, an Excel workbook and a
// PowerPoint deck this driver builds itself, with invented text, and
// reads each back. What it settles is the part no fake can: that Drive
// answers the byte ranges an Office read makes, many of them in quick
// succession, with exactly those bytes. Each read is checked for the
// text that went in, because a read that succeeds with the wrong text
// is the failure that matters here.
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
			reads: []map[string]any{{}, {"max_chars": 20}, {"offset": 20, "max_chars": 200}},
			want:  []string{"# Driver heading", "- first point", "| left | right |"},
		},
		{
			local: "livedrive-rows.xlsx", name: "Driver rows.xlsx",
			data: officetest.Xlsx(officetest.Workbook{CellXfs: `<xf numFmtId="0"/><xf numFmtId="14"/>`,
				Sheets: []officetest.Sheet{
					{Name: "Totals", Data: officetest.Row(1, "item", "count", "when") +
						`<row r="2"><c r="A2" t="inlineStr"><is><t>bolts, small</t></is></c><c r="B2"><v>12</v></c>` +
						`<c r="C2" s="1"><v>45000</v></c></row>`},
					{Name: "Other", Data: officetest.Row(1, "not read")},
				}}),
			reads: []map[string]any{{}, {"format": "tsv"}},
			want:  []string{"item,count,when", `"bolts, small",12,2023-03-15`},
		},
		{
			local: "livedrive-deck.pptx", name: "Driver deck.pptx",
			data:  officetest.Pptx([]string{"Opening slide"}, []string{"Closing slide", "with two lines"}),
			reads: []map[string]any{{}},
			want:  []string{"--- slide 1 ---", "Opening slide", "--- slide 2 ---", "with two lines"},
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
		var whole string
		for _, extra := range f.reads {
			args := map[string]any{"file": id}
			for k, v := range extra {
				args[k] = v
			}
			out := w.call(call{tool: "read_file", args: args})
			if len(extra) == 0 {
				whole = out
			}
		}
		for _, want := range f.want {
			if !strings.Contains(whole, want) {
				w.failures++
				w.out.Sayf("!! the text read back from %s does not contain %q", f.name, want)
			}
		}
	}
}
