// Package officetest builds small Word, Excel and PowerPoint files and
// their OpenDocument counterparts, for tests and for the live driver to
// upload. Everything in them is whatever the caller passes; nothing is
// taken from a real file.
//
// The files carry the parts a reader needs and the ones Office needs to
// open them: the content types, the relationships, the main part, and
// for OpenDocument the mimetype entry stored first and uncompressed.
package officetest

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
)

// Entry is one file inside a zip.
type Entry struct {
	Name string
	Data string
	// Store keeps it uncompressed, as the mimetype entry of an
	// OpenDocument file has to be.
	Store bool
}

// Zip builds an archive of the entries, in order.
func Zip(entries ...Entry) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		method := zip.Deflate
		if e.Store {
			method = zip.Store
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.Name, Method: method})
		if err != nil {
			panic(err) // a fixed name and method; this cannot fail
		}
		if _, err := w.Write([]byte(e.Data)); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// Namespaces the builders write.
const (
	nsW   = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	nsS   = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
	nsP   = "http://schemas.openxmlformats.org/presentationml/2006/main"
	nsA   = "http://schemas.openxmlformats.org/drawingml/2006/main"
	nsR   = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsRel = "http://schemas.openxmlformats.org/package/2006/relationships"
	nsCT  = "http://schemas.openxmlformats.org/package/2006/content-types"
	relOD = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"
)

const xmlHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

// rels writes a relationships part from id, type and target triples.
func rels(triples ...string) string {
	var b strings.Builder
	b.WriteString(xmlHead + `<Relationships xmlns="` + nsRel + `">`)
	for i := 0; i+2 < len(triples); i += 3 {
		fmt.Fprintf(&b, `<Relationship Id="%s" Type="%s" Target="%s"/>`, triples[i], relOD+triples[i+1], triples[i+2])
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

func contentTypes(overrides ...string) string {
	var b strings.Builder
	b.WriteString(xmlHead + `<Types xmlns="` + nsCT + `">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	for i := 0; i+1 < len(overrides); i += 2 {
		fmt.Fprintf(&b, `<Override PartName="%s" ContentType="%s"/>`, overrides[i], overrides[i+1])
	}
	b.WriteString(`</Types>`)
	return b.String()
}

// Docx builds a Word document whose body is the given WordprocessingML,
// with a style sheet naming "Heading1" and "Heading2" as headings.
func Docx(body string) []byte {
	return Zip(
		Entry{Name: "[Content_Types].xml", Data: contentTypes(
			"/word/document.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml",
			"/word/styles.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml")},
		Entry{Name: "_rels/.rels", Data: rels("rId1", "officeDocument", "word/document.xml")},
		Entry{Name: "word/_rels/document.xml.rels", Data: rels("rId1", "styles", "styles.xml")},
		Entry{Name: "word/document.xml", Data: xmlHead + `<w:document xmlns:w="` + nsW + `" xmlns:r="` + nsR +
			`"><w:body>` + body + `</w:body></w:document>`},
		Entry{Name: "word/styles.xml", Data: xmlHead + `<w:styles xmlns:w="` + nsW + `">` +
			`<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/></w:style>` +
			`<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/></w:style>` +
			`<w:style w:type="paragraph" w:styleId="Normal"><w:name w:val="Normal"/></w:style>` +
			`</w:styles>`},
	)
}

// WordParagraph is one paragraph of plain text, with a style id when
// style is not empty.
func WordParagraph(style, text string) string {
	props := ""
	if style != "" {
		props = `<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
	}
	return `<w:p>` + props + `<w:r><w:t xml:space="preserve">` + escape(text) + `</w:t></w:r></w:p>`
}

// WordListItem is one paragraph of a list, at a 0-based level.
func WordListItem(level int, text string) string {
	return fmt.Sprintf(`<w:p><w:pPr><w:numPr><w:ilvl w:val="%d"/><w:numId w:val="1"/></w:numPr></w:pPr>`+
		`<w:r><w:t>%s</w:t></w:r></w:p>`, level, escape(text))
}

// WordTable is a table of plain text cells.
func WordTable(rows ...[]string) string {
	var b strings.Builder
	b.WriteString(`<w:tbl>`)
	for _, row := range rows {
		b.WriteString(`<w:tr>`)
		for _, c := range row {
			b.WriteString(`<w:tc>` + WordParagraph("", c) + `</w:tc>`)
		}
		b.WriteString(`</w:tr>`)
	}
	b.WriteString(`</w:tbl>`)
	return b.String()
}

// Sheet is one worksheet: its name, and its sheetData as XML.
type Sheet struct {
	Name   string
	Hidden bool
	Data   string
}

// Workbook is an Excel workbook to build.
type Workbook struct {
	Sheets []Sheet
	// Shared is the shared string table.
	Shared []string
	// NumFmts and CellXfs are the styles part's contents, as XML.
	NumFmts string
	CellXfs string
	// Date1904 switches the workbook to the 1904 date system.
	Date1904 bool
}

// Xlsx builds the workbook.
func Xlsx(wb Workbook) []byte {
	var sheets, wbRels strings.Builder
	entries := []Entry{
		{Name: "_rels/.rels", Data: rels("rId1", "officeDocument", "xl/workbook.xml")},
	}
	overrides := make([]string, 0, 6+2*len(wb.Sheets))
	overrides = append(overrides, "/xl/workbook.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml")
	triples := make([]string, 0, 6+3*len(wb.Sheets))
	for i, s := range wb.Sheets {
		id := fmt.Sprintf("rId%d", i+1)
		state := ""
		if s.Hidden {
			state = ` state="hidden"`
		}
		fmt.Fprintf(&sheets, `<sheet name="%s" sheetId="%d"%s r:id="%s"/>`, escape(s.Name), i+1, state, id)
		part := fmt.Sprintf("worksheets/sheet%d.xml", i+1)
		triples = append(triples, id, "worksheet", part)
		entries = append(entries, Entry{Name: "xl/" + part, Data: xmlHead + `<worksheet xmlns="` + nsS +
			`" xmlns:r="` + nsR + `"><sheetData>` + s.Data + `</sheetData></worksheet>`})
		overrides = append(overrides, "/xl/"+part, "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml")
	}
	triples = append(triples, "rIdS", "sharedStrings", "sharedStrings.xml", "rIdT", "styles", "styles.xml")
	wbRels.WriteString(rels(triples...))
	pr := ""
	if wb.Date1904 {
		pr = `<workbookPr date1904="1"/>`
	}
	var sst strings.Builder
	for _, s := range wb.Shared {
		sst.WriteString(`<si><t xml:space="preserve">` + escape(s) + `</t></si>`)
	}
	entries = append(entries,
		Entry{Name: "xl/workbook.xml", Data: xmlHead + `<workbook xmlns="` + nsS + `" xmlns:r="` + nsR + `">` + pr +
			`<sheets>` + sheets.String() + `</sheets></workbook>`},
		Entry{Name: "xl/_rels/workbook.xml.rels", Data: wbRels.String()},
		Entry{Name: "xl/sharedStrings.xml", Data: xmlHead + `<sst xmlns="` + nsS + `">` + sst.String() + `</sst>`},
		Entry{Name: "xl/styles.xml", Data: xmlHead + `<styleSheet xmlns="` + nsS + `"><numFmts>` + wb.NumFmts +
			`</numFmts><cellXfs>` + orDefault(wb.CellXfs, `<xf numFmtId="0"/>`) + `</cellXfs></styleSheet>`},
	)
	overrides = append(overrides,
		"/xl/sharedStrings.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml",
		"/xl/styles.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml")
	entries = append([]Entry{{Name: "[Content_Types].xml", Data: contentTypes(overrides...)}}, entries...)
	return Zip(entries...)
}

// Row is one row of a sheet, each cell an inline string at its column,
// starting at A.
func Row(n int, cells ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<row r="%d">`, n)
	for i, c := range cells {
		if c == "" {
			continue
		}
		fmt.Fprintf(&b, `<c r="%s%d" t="inlineStr"><is><t>%s</t></is></c>`, Column(i), n, escape(c))
	}
	b.WriteString(`</row>`)
	return b.String()
}

// Column is the letters of a 0-based column: A, B, ... Z, AA.
func Column(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}

// Pptx builds a deck with one slide per entry, each slide's text one
// paragraph per line in a single text box.
func Pptx(slides ...[]string) []byte {
	parts := make([]string, len(slides))
	for i, lines := range slides {
		var paras strings.Builder
		for _, l := range lines {
			paras.WriteString(`<a:p><a:r><a:rPr lang="en-US"/><a:t>` + escape(l) + `</a:t></a:r></a:p>`)
		}
		parts[i] = SlideXML(paras.String())
	}
	return PptxRaw(parts...)
}

// SlideXML is a whole slide part whose one text box holds the given
// DrawingML paragraphs.
func SlideXML(paragraphs string) string {
	return xmlHead + `<p:sld xmlns:p="` + nsP + `" xmlns:a="` + nsA + `" xmlns:r="` + nsR +
		`"><p:cSld><p:spTree><p:sp><p:txBody><a:bodyPr/>` + paragraphs + `</p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
}

// PptxRaw builds a deck from whole slide parts, in order.
func PptxRaw(slides ...string) []byte {
	var ids strings.Builder
	triples := make([]string, 0, 3*len(slides))
	entries := []Entry{
		{Name: "_rels/.rels", Data: rels("rId1", "officeDocument", "ppt/presentation.xml")},
	}
	overrides := make([]string, 0, 2+2*len(slides))
	overrides = append(overrides, "/ppt/presentation.xml", "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml")
	for i, slide := range slides {
		id := fmt.Sprintf("rId%d", i+1)
		fmt.Fprintf(&ids, `<p:sldId id="%d" r:id="%s"/>`, 256+i, id)
		part := fmt.Sprintf("slides/slide%d.xml", i+1)
		triples = append(triples, id, "slide", part)
		entries = append(entries, Entry{Name: "ppt/" + part, Data: slide})
		overrides = append(overrides, "/ppt/"+part, "application/vnd.openxmlformats-officedocument.presentationml.slide+xml")
	}
	entries = append(entries,
		Entry{Name: "ppt/presentation.xml", Data: xmlHead + `<p:presentation xmlns:p="` + nsP + `" xmlns:r="` + nsR +
			`"><p:sldIdLst>` + ids.String() + `</p:sldIdLst></p:presentation>`},
		Entry{Name: "ppt/_rels/presentation.xml.rels", Data: rels(triples...)},
	)
	entries = append([]Entry{{Name: "[Content_Types].xml", Data: contentTypes(overrides...)}}, entries...)
	return Zip(entries...)
}

// ODF namespaces.
const odfNamespaces = `xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" ` +
	`xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" ` +
	`xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0" ` +
	`xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" ` +
	`xmlns:presentation="urn:oasis:names:tc:opendocument:xmlns:presentation:1.0" office:version="1.3"`

func odf(mime, body string) []byte {
	return Zip(
		Entry{Name: "mimetype", Data: mime, Store: true},
		Entry{Name: "META-INF/manifest.xml", Data: xmlHead +
			`<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0" manifest:version="1.3">` +
			`<manifest:file-entry manifest:full-path="/" manifest:media-type="` + mime + `"/>` +
			`<manifest:file-entry manifest:full-path="content.xml" manifest:media-type="text/xml"/>` +
			`</manifest:manifest>`},
		Entry{Name: "content.xml", Data: xmlHead + `<office:document-content ` + odfNamespaces + `><office:body>` +
			body + `</office:body></office:document-content>`},
	)
}

// Odt builds an OpenDocument text whose office:text is the given XML.
func Odt(text string) []byte {
	return odf("application/vnd.oasis.opendocument.text", `<office:text>`+text+`</office:text>`)
}

// Ods builds an OpenDocument spreadsheet whose office:spreadsheet is
// the given XML.
func Ods(sheets string) []byte {
	return odf("application/vnd.oasis.opendocument.spreadsheet", `<office:spreadsheet>`+sheets+`</office:spreadsheet>`)
}

// Odp builds an OpenDocument presentation whose office:presentation is
// the given XML.
func Odp(pages string) []byte {
	return odf("application/vnd.oasis.opendocument.presentation", `<office:presentation>`+pages+`</office:presentation>`)
}

func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
