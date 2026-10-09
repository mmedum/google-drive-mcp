package office_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v2/internal/office"
	"github.com/mmedum/google-drive-mcp/v2/internal/office/officetest"
)

// extract reads a file held in memory.
func extract(t *testing.T, data []byte, kind office.Kind, o office.Options) *office.Result {
	t.Helper()
	res, err := office.Extract(t.Context(), bytes.NewReader(data), int64(len(data)), kind, o)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return res
}

// refusal reads a file that ought to be refused, and returns why.
func refusal(t *testing.T, data []byte, kind office.Kind) *office.Error {
	t.Helper()
	res, err := office.Extract(t.Context(), bytes.NewReader(data), int64(len(data)), kind, office.Options{})
	if err == nil {
		t.Fatalf("Extract succeeded with %q, want a refusal", res.Text)
	}
	var oe *office.Error
	if !errors.As(err, &oe) {
		t.Fatalf("Extract: %v (%T), want an *office.Error", err, err)
	}
	return oe
}

func TestKindOfNamesTheSixFormats(t *testing.T) {
	cases := map[string]office.Kind{
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   office.Docx,
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         office.Xlsx,
		"application/vnd.openxmlformats-officedocument.presentationml.presentation": office.Pptx,
		"application/vnd.oasis.opendocument.text":                                   office.Odt,
		"application/vnd.oasis.opendocument.spreadsheet; charset=binary":            office.Ods,
		"Application/Vnd.Oasis.Opendocument.Presentation":                           office.Odp,
		"application/msword":       office.None,
		"application/vnd.ms-excel": office.None,
		"application/pdf":          office.None,
	}
	for mime, want := range cases {
		if got := office.KindOf(mime); got != want {
			t.Errorf("KindOf(%q) = %v, want %v", mime, got, want)
		}
	}
}

func TestWordReadsHeadingsListsAndTablesInOrder(t *testing.T) {
	body := officetest.WordParagraph("Heading1", "Quarterly plan") +
		officetest.WordParagraph("", "An opening paragraph.") +
		officetest.WordParagraph("", "") +
		officetest.WordParagraph("Heading2", "Goals") +
		officetest.WordListItem(0, "First goal") +
		officetest.WordListItem(1, "A detail of it") +
		officetest.WordTable([]string{"Name", "Amount"}, []string{"Widgets", "12"}) +
		// A run with a tab and a line break in it.
		`<w:p><w:r><w:t>left</w:t><w:tab/><w:t>right</w:t><w:br/><w:t>below</w:t></w:r></w:p>`
	res := extract(t, officetest.Docx(body), office.Docx, office.Options{})
	want := "# Quarterly plan\n" +
		"An opening paragraph.\n" +
		"\n" +
		"## Goals\n" +
		"- First goal\n" +
		"  - A detail of it\n" +
		"| Name | Amount |\n" +
		"| Widgets | 12 |\n" +
		"left\tright\nbelow\n"
	if res.Text != want {
		t.Errorf("text =\n%q\nwant\n%q", res.Text, want)
	}
	if res.Truncated {
		t.Error("a short document was reported as cut short")
	}
}

func TestWordLeavesOutWhatIsNotTheText(t *testing.T) {
	body := `<w:p>` +
		`<w:r><w:t xml:space="preserve">kept </w:t></w:r>` +
		`<w:del w:id="1" w:author="Reviewer"><w:r><w:delText>deleted</w:delText></w:r></w:del>` +
		`<w:ins w:id="2" w:author="Reviewer"><w:r><w:t xml:space="preserve">inserted </w:t></w:r></w:ins>` +
		`<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText>PAGE</w:instrText></w:r>` +
		`<w:r><w:fldChar w:fldCharType="separate"/></w:r><w:r><w:t>7</w:t></w:r>` +
		`<w:r><w:fldChar w:fldCharType="end"/></w:r>` +
		`<w:r><mc:AlternateContent xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006">` +
		`<mc:Choice Requires="wps"><w:t xml:space="preserve"> box</w:t></mc:Choice>` +
		`<mc:Fallback><w:t> box again</w:t></mc:Fallback></mc:AlternateContent></w:r>` +
		`</w:p>`
	res := extract(t, officetest.Docx(body), office.Docx, office.Options{})
	if want := "kept inserted 7 box\n"; res.Text != want {
		t.Errorf("text = %q, want %q", res.Text, want)
	}
}

func TestWordWithoutAStyleSheetStillReads(t *testing.T) {
	data := officetest.Zip(
		officetest.Entry{Name: "word/document.xml", Data: `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
			`<w:body><w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Plain</w:t></w:r></w:p></w:body></w:document>`},
	)
	res := extract(t, data, office.Docx, office.Options{})
	// No style sheet, no relationships: the conventional part is read,
	// and a style nobody defined marks nothing.
	if res.Text != "Plain\n" {
		t.Errorf("text = %q, want %q", res.Text, "Plain\n")
	}
}

func TestPowerPointReadsSlidesInShowOrder(t *testing.T) {
	data := officetest.Pptx([]string{"Title slide", "by the team"}, []string{"Second"})
	res := extract(t, data, office.Pptx, office.Options{})
	want := "--- slide 1 ---\nTitle slide\nby the team\n\n--- slide 2 ---\nSecond\n"
	if res.Text != want {
		t.Errorf("text =\n%q\nwant\n%q", res.Text, want)
	}
	if res.Slides != 2 {
		t.Errorf("Slides = %d, want 2", res.Slides)
	}
}

func TestPowerPointFollowsTheDeckNotThePartNames(t *testing.T) {
	// The deck lists slide2.xml first and marks it hidden; a reader that
	// went by part name would put it second.
	const ns = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
		`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	slide := func(attrs, body string) string {
		return `<p:sld ` + ns + attrs + `><p:cSld><p:spTree><p:sp><p:txBody>` + body + `</p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
	}
	data := officetest.Zip(
		officetest.Entry{Name: "_rels/.rels", Data: `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/></Relationships>`},
		officetest.Entry{Name: "ppt/presentation.xml", Data: `<p:presentation ` + ns + `><p:sldIdLst>` +
			`<p:sldId id="256" r:id="rId2"/><p:sldId id="257" r:id="rId1"/></p:sldIdLst></p:presentation>`},
		officetest.Entry{Name: "ppt/_rels/presentation.xml.rels", Data: `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide1.xml"/>` +
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="/ppt/slides/slide2.xml"/>` +
			`</Relationships>`},
		officetest.Entry{Name: "ppt/slides/slide1.xml", Data: slide("", `<a:p><a:r><a:t>shown</a:t></a:r><a:br/><a:r><a:t>second line</a:t></a:r></a:p>`)},
		officetest.Entry{Name: "ppt/slides/slide2.xml", Data: slide(` show="0"`, `<a:p><a:r><a:t>hidden first</a:t></a:r></a:p>`+
			`<a:tbl><a:tr><a:tc><a:txBody><a:p><a:r><a:t>cell</a:t></a:r></a:p></a:txBody></a:tc>`+
			`<a:tc><a:txBody><a:p><a:r><a:t>other</a:t></a:r></a:p></a:txBody></a:tc></a:tr></a:tbl>`)},
	)
	res := extract(t, data, office.Pptx, office.Options{})
	want := "--- slide 1 (hidden) ---\nhidden first\n| cell | other |\n\n--- slide 2 ---\nshown\nsecond line\n"
	if res.Text != want {
		t.Errorf("text =\n%q\nwant\n%q", res.Text, want)
	}
}

func TestExcelReadsTheFirstVisibleSheetAsCSV(t *testing.T) {
	wb := officetest.Workbook{
		Shared: []string{"name", "amount", "says \"hi\", twice"},
		Sheets: []officetest.Sheet{
			{Name: "Scratch", Hidden: true, Data: officetest.Row(1, "not this one")},
			{Name: "Data", Data: `<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>` +
				`<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>12.5</v></c></row>` +
				// Row 3 is missing; row 4 skips column B.
				`<row r="4"><c r="A4" t="inlineStr"><is><t>two
lines</t></is></c><c r="C4" t="b"><v>1</v></c></row>` +
				`<row r="5"><c r="A5"><f>SUM(B1:B4)</f><v>12.5</v></c><c r="B5" t="e"><v>#DIV/0!</v></c></row>` +
				// Trailing rows with formatting and no values.
				`<row r="6"><c r="A6" s="0"/></row><row r="900"><c r="A900" s="0"/></row>`},
			{Name: "Later", Data: officetest.Row(1, "nor this")},
		},
	}
	res := extract(t, officetest.Xlsx(wb), office.Xlsx, office.Options{})
	want := "name,amount\n" +
		"\"says \"\"hi\"\", twice\",12.5\n" +
		"\n" +
		"\"two\nlines\",,TRUE\n" +
		"12.5,#DIV/0!\n"
	if res.Text != want {
		t.Errorf("text =\n%q\nwant\n%q", res.Text, want)
	}
	if res.Sheet != "Data" || res.Sheets != 3 {
		t.Errorf("sheet = %q of %d, want \"Data\" of 3", res.Sheet, res.Sheets)
	}

	tsv := extract(t, officetest.Xlsx(wb), office.Xlsx, office.Options{Delimiter: '\t'})
	if !strings.HasPrefix(tsv.Text, "name\tamount\n\"says \"\"hi\"\", twice\"\t12.5\n") {
		t.Errorf("tsv text = %q", tsv.Text)
	}
}

func TestExcelShowsDatesAndTimesTheFormatsMakeOfThem(t *testing.T) {
	// One cell per format. Each style index names a number format: the
	// built-ins by id, and custom codes in numFmts.
	xfs := []int{0, 14, 20, 22, 46, 164, 165, 166, 167, 168, 169, 170, 171, 172}
	var cellXfs strings.Builder
	for _, id := range xfs {
		fmt.Fprintf(&cellXfs, `<xf numFmtId="%d"/>`, id)
	}
	numFmts := `<numFmt numFmtId="164" formatCode="yyyy\-mm\-dd hh:mm"/>` +
		`<numFmt numFmtId="165" formatCode="[$-409]mmmm d, yyyy;@"/>` +
		`<numFmt numFmtId="166" formatCode="0.00%"/>` +
		`<numFmt numFmtId="167" formatCode="&quot;due &quot;d/m"/>` +
		`<numFmt numFmtId="168" formatCode="[Red]#,##0.00"/>` +
		`<numFmt numFmtId="169" formatCode="h:mm AM/PM"/>` +
		`<numFmt numFmtId="170" formatCode="mmmm"/>` +
		// A backslash escapes the character after it, which in these
		// takes more than one byte.
		`<numFmt numFmtId="171" formatCode="yyyy\年m\月d\日"/>` +
		`<numFmt numFmtId="172" formatCode="[$-FC19]dd\ mmmm\ yyyy\ \г\.;@"/>`
	cases := []struct {
		style int
		value string
		want  string
	}{
		{0, "45000", "45000"},                  // General: a number
		{1, "45000", "2023-03-15"},             // 14, a date
		{1, "1", "1900-01-01"},                 // the first day of the 1900 base
		{1, "59", "1900-02-28"},                //
		{1, "60", "60"},                        // 29 February 1900, which never was
		{1, "61", "1900-03-01"},                //
		{1, "0", "0"},                          // day zero has no date
		{1, "2958465", "9999-12-31"},           // the last day of the base
		{1, "2958466", "2958466"},              // past it
		{2, "0.5", "12:00:00"},                 // 20, a time
		{3, "45000.75", "2023-03-15 18:00:00"}, // 22, both
		{3, "45000.9999999", "2023-03-16 00:00:00"},
		{4, "1.5", "36:00:00"},                 // 46, hours past a day
		{5, "45000.25", "2023-03-15 06:00:00"}, // custom date and time
		{6, "45000", "2023-03-15"},             // locale code in brackets
		{7, "0.15", "0.15"},                    // a percentage stays a number
		{8, "45000", "2023-03-15"},             // quoted text around a date
		{9, "45000", "45000"},                  // a color code is not a date
		{10, "0.75", "18:00:00"},               // AM/PM makes a time
		{1, "-1", "-1"},                        // a negative serial is no date
		{11, "45000", "2023-03-15"},            // a month name alone is a date
		{12, "45000", "2023-03-15"},            // a Japanese long date
		{13, "45000", "2023-03-15"},            // a Russian long date
	}
	var rows strings.Builder
	for i, c := range cases {
		fmt.Fprintf(&rows, `<row r="%d"><c r="A%d" s="%d"><v>%s</v></c></row>`, i+1, i+1, c.style, c.value)
	}
	wb := officetest.Workbook{NumFmts: numFmts, CellXfs: cellXfs.String(),
		Sheets: []officetest.Sheet{{Name: "Dates", Data: rows.String()}}}
	got := strings.Split(strings.TrimSuffix(extract(t, officetest.Xlsx(wb), office.Xlsx, office.Options{}).Text, "\n"), "\n")
	if len(got) != len(cases) {
		t.Fatalf("got %d rows, want %d: %q", len(got), len(cases), got)
	}
	for i, c := range cases {
		if got[i] != c.want {
			t.Errorf("style %d (format %d), value %s: got %q, want %q", c.style, xfs[c.style], c.value, got[i], c.want)
		}
	}
}

func TestExcelCountsDatesFrom1904WhenTheWorkbookSaysSo(t *testing.T) {
	wb := officetest.Workbook{CellXfs: `<xf numFmtId="0"/><xf numFmtId="14"/>`, Date1904: true,
		Sheets: []officetest.Sheet{{Name: "Mac", Data: `<row r="1"><c r="A1" s="1"><v>0</v></c>` +
			`<c r="B1" s="1"><v>43538</v></c></row>`}}}
	res := extract(t, officetest.Xlsx(wb), office.Xlsx, office.Options{})
	// 1 462 days apart from the 1900 base, as Microsoft documents.
	if want := "1904-01-01,2023-03-15\n"; res.Text != want {
		t.Errorf("text = %q, want %q", res.Text, want)
	}
}

func TestExcelRefusesACellNamingAStringThatIsNotThere(t *testing.T) {
	wb := officetest.Workbook{Shared: []string{"only"},
		Sheets: []officetest.Sheet{{Name: "S", Data: `<row r="1"><c r="A1" t="s"><v>1</v></c></row>`}}}
	if oe := refusal(t, officetest.Xlsx(wb), office.Xlsx); oe.Limit {
		t.Errorf("a damaged file was refused as over a limit: %v", oe)
	}
}

func TestExcelWithNoSheetsIsEmpty(t *testing.T) {
	res := extract(t, officetest.Xlsx(officetest.Workbook{}), office.Xlsx, office.Options{})
	if res.Text != "" || res.Sheets != 0 {
		t.Errorf("result = %+v, want empty", res)
	}
}

func TestOpenDocumentTextReadsLikeAWordDocument(t *testing.T) {
	text := `<text:h text:outline-level="2">Section</text:h>` +
		`<text:p>One<text:s text:c="3"/>two<text:tab/>three<text:line-break/>four</text:p>` +
		`<text:list><text:list-item><text:p>Item</text:p><text:p>more of it</text:p>` +
		`<text:list><text:list-item><text:p>Nested</text:p></text:list-item></text:list>` +
		`</text:list-item></text:list>` +
		`<table:table><table:table-row><table:table-cell><text:p>a</text:p></table:table-cell>` +
		`<table:table-cell table:number-columns-repeated="2"/><table:table-cell><text:p>d</text:p></table:table-cell>` +
		`</table:table-row></table:table>` +
		`<text:tracked-changes><text:changed-region><text:deletion><text:p>gone</text:p></text:deletion></text:changed-region></text:tracked-changes>` +
		`<text:p>kept<office:annotation><text:p>a comment</text:p></office:annotation>` +
		`<text:note><text:note-citation>1</text:note-citation><text:note-body><text:p>a footnote</text:p></text:note-body></text:note></text:p>`
	res := extract(t, officetest.Odt(text), office.Odt, office.Options{})
	want := "## Section\n" +
		"One   two\tthree\nfour\n" +
		"- Item\n" +
		"  more of it\n" +
		"  - Nested\n" +
		"| a |  |  | d |\n" +
		"kept\n"
	if res.Text != want {
		t.Errorf("text =\n%q\nwant\n%q", res.Text, want)
	}
}

func TestOpenDocumentSpreadsheetReadsTheFirstTable(t *testing.T) {
	sheets := `<table:table table:name="Budget">` +
		`<table:table-row><table:table-cell office:value-type="string"><text:p>item</text:p></table:table-cell>` +
		`<table:table-cell office:value-type="string"><text:p>cost</text:p></table:table-cell></table:table-row>` +
		`<table:table-row table:number-rows-repeated="2">` +
		`<table:table-cell office:value-type="float" office:value="3.5"><text:p>3.50</text:p></table:table-cell>` +
		`<table:table-cell table:number-columns-repeated="2" office:value-type="boolean" office:boolean-value="true"><text:p>TRUE</text:p></table:table-cell>` +
		`</table:table-row>` +
		`<table:table-row table:number-rows-repeated="3"><table:table-cell table:number-columns-repeated="1024"/></table:table-row>` +
		`<table:table-row><table:table-cell office:value-type="date" office:date-value="2026-03-04T09:30:00"><text:p>04/03/26</text:p></table:table-cell>` +
		`<table:table-cell office:value-type="time" office:time-value="PT09H30M00S"/>` +
		`<table:table-cell office:value-type="percentage" office:value="0.15"/>` +
		`<table:table-cell><text:p>two</text:p><text:p>lines</text:p></table:table-cell></table:table-row>` +
		`<table:table-row table:number-rows-repeated="1048000"><table:table-cell table:number-columns-repeated="16384"/></table:table-row>` +
		`</table:table>` +
		`<table:table table:name="Second"><table:table-row><table:table-cell><text:p>not read</text:p></table:table-cell></table:table-row></table:table>`
	res := extract(t, officetest.Ods(sheets), office.Ods, office.Options{})
	want := "item,cost\n" +
		"3.5,TRUE,TRUE\n" +
		"3.5,TRUE,TRUE\n" +
		"\n\n\n" +
		"2026-03-04 09:30:00,09:30:00,0.15,\"two\nlines\"\n"
	if res.Text != want {
		t.Errorf("text =\n%q\nwant\n%q", res.Text, want)
	}
	if res.Sheet != "Budget" {
		t.Errorf("sheet = %q, want Budget", res.Sheet)
	}
}

func TestOpenDocumentPresentationReadsPageByPage(t *testing.T) {
	pages := `<draw:page draw:name="one"><draw:frame><draw:text-box><text:p>Hello</text:p></draw:text-box></draw:frame>` +
		`<presentation:notes><draw:frame><draw:text-box><text:p>speaker only</text:p></draw:text-box></draw:frame></presentation:notes>` +
		`</draw:page><draw:page draw:name="two"/>`
	res := extract(t, officetest.Odp(pages), office.Odp, office.Options{})
	if want := "--- slide 1 ---\nHello\n\n--- slide 2 ---\n"; res.Text != want {
		t.Errorf("text = %q, want %q", res.Text, want)
	}
	if res.Slides != 2 {
		t.Errorf("Slides = %d, want 2", res.Slides)
	}
}

func TestTextStopsAtItsCapOnAWholeCharacter(t *testing.T) {
	data := officetest.Docx(officetest.WordParagraph("", "ab→cd"))
	// "ab→" is five bytes; a cap of four lands inside the arrow, and
	// the line ends where the last whole character did.
	res := extract(t, data, office.Docx, office.Options{MaxText: 4})
	if res.Text != "ab\n" || !res.Truncated {
		t.Errorf("cap 4: %q, truncated %v; want \"ab\\n\", truncated", res.Text, res.Truncated)
	}
	// A cap of two leaves no room for the line's end.
	short := extract(t, data, office.Docx, office.Options{MaxText: 2})
	if short.Text != "ab" || !short.Truncated {
		t.Errorf("cap 2: %q, truncated %v; want \"ab\", truncated", short.Text, short.Truncated)
	}
	exact := extract(t, data, office.Docx, office.Options{MaxText: 10})
	if exact.Text != "ab→cd\n" || exact.Truncated {
		t.Errorf("at exactly its length: %q, truncated %v", exact.Text, exact.Truncated)
	}
}

func TestReadingStopsWhenTheXMLBudgetIsSpent(t *testing.T) {
	var body strings.Builder
	for i := range 200 {
		body.WriteString(officetest.WordParagraph("", fmt.Sprintf("paragraph %d", i)))
	}
	res := extract(t, officetest.Docx(body.String()), office.Docx, office.Options{MaxRead: 4096})
	if !res.Truncated || !strings.HasPrefix(res.Text, "paragraph 0\n") || strings.Contains(res.Text, "paragraph 199") {
		t.Errorf("result = %q, truncated %v; want the first paragraphs and a cut", res.Text, res.Truncated)
	}
}

func TestRefusals(t *testing.T) {
	deep := strings.Repeat("<w:sdt><w:sdtContent>", 130) + strings.Repeat("</w:sdtContent></w:sdt>", 130)
	many := make([]officetest.Entry, office.MaxEntries+1)
	for i := range many {
		many[i] = officetest.Entry{Name: fmt.Sprintf("e%d", i)}
	}
	exactly := make([]officetest.Entry, office.MaxEntries)
	for i := range exactly {
		exactly[i] = officetest.Entry{Name: fmt.Sprintf("e%d", i)}
	}
	exactly[0] = officetest.Entry{Name: "content.xml", Data: `<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"/>`}
	cases := []struct {
		name  string
		data  []byte
		kind  office.Kind
		limit bool
	}{
		{"not a zip", []byte("this is plain text and not an archive of any kind"), office.Docx, false},
		{"too short to be a zip", []byte("PK"), office.Docx, false},
		{"encrypted", append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 600)...), office.Xlsx, false},
		{"no main part", officetest.Zip(officetest.Entry{Name: "other.xml", Data: "<x/>"}), office.Docx, false},
		{"no content.xml", officetest.Zip(officetest.Entry{Name: "mimetype", Data: "x", Store: true}), office.Odt, false},
		{"not well-formed", officetest.Docx(`<w:p><w:r><w:t>open`), office.Docx, false},
		{"an entity it does not define", officetest.Docx(`<w:p><w:r><w:t>&lol;</w:t></w:r></w:p>`), office.Docx, false},
		{"nested too deep", officetest.Docx(deep), office.Docx, true},
		{"too many entries", officetest.Zip(many...), office.Odt, true},
		{"expands too far", officetest.Zip(officetest.Entry{Name: "content.xml", Data: strings.Repeat(" ", 2<<20)}), office.Odt, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			oe := refusal(t, c.data, c.kind)
			if oe.Limit != c.limit {
				t.Errorf("Limit = %v, want %v (%v)", oe.Limit, c.limit, oe)
			}
		})
	}
	// The edges: exactly MaxEntries entries, and nesting just inside the
	// limit, both read.
	extract(t, officetest.Zip(exactly...), office.Odt, office.Options{})
	ok := strings.Repeat("<w:sdt><w:sdtContent>", 125) + strings.Repeat("</w:sdtContent></w:sdt>", 125)
	extract(t, officetest.Docx(ok), office.Docx, office.Options{})
}

func TestARefusedEncryptedFileSaysSo(t *testing.T) {
	data := append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 600)...)
	if oe := refusal(t, data, office.Docx); !strings.Contains(oe.Reason, "password-protected") {
		t.Errorf("reason = %q, want it to name password protection", oe.Reason)
	}
}
