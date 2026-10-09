package office_test

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/v2/internal/office"
	"github.com/mmedum/google-drive-mcp/v2/internal/office/officetest"
)

// spent is what one extraction cost.
type spent struct {
	res   *office.Result
	err   error
	alloc uint64
	took  time.Duration
}

// measure runs one extraction and reports what it allocated and how long
// it took. The bytes counted are every allocation made, kept or not,
// which bounds what the read can hold at once.
func measure(t *testing.T, r interface {
	ReadAt([]byte, int64) (int, error)
}, size int64, kind office.Kind, o office.Options) spent {
	t.Helper()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	res, err := office.Extract(t.Context(), r, size, kind, o)
	took := time.Since(start)
	runtime.ReadMemStats(&after)
	return spent{res: res, err: err, alloc: after.TotalAlloc - before.TotalAlloc, took: took}
}

// Bounds for a read under smallCap. A file that costs what its text
// does stays far inside them; each hostile shape below cost hundreds of
// megabytes, or minutes, before it was bounded.
const (
	smallCap  = 64 << 10
	maxAlloc  = 16 << 20
	maxTaking = 3 * time.Second
)

func TestHostileFilesCostWhatTheCapAllows(t *testing.T) {
	long := strings.Repeat("a", smallCap)
	var row strings.Builder
	row.WriteString(`<row r="1">`)
	for range 1000 {
		row.WriteString(`<c t="s"><v>0</v></c>`)
	}
	row.WriteString(`</row>`)
	var far strings.Builder
	for i := range 20000 {
		fmt.Fprintf(&far, `<row r="%d"><c r="XFD%d"/></row>`, i+1, i+1)
	}
	cases := []struct {
		name string
		data []byte
		kind office.Kind
	}{
		{"one long shared string named by a thousand cells of a row",
			officetest.Xlsx(officetest.Workbook{Shared: []string{long}, Sheets: []officetest.Sheet{{Name: "S", Data: row.String()}}}),
			office.Xlsx},
		{"a long sheet cell repeated across a thousand columns",
			officetest.Ods(`<table:table table:name="S"><table:table-row><table:table-cell table:number-columns-repeated="1000">` +
				`<text:p>` + long + `</text:p></table:table-cell></table:table-row></table:table>`),
			office.Ods},
		{"a long table cell repeated across a thousand columns",
			officetest.Odt(`<table:table><table:table-row><table:table-cell table:number-columns-repeated="1000">` +
				`<text:p>` + long + `</text:p></table:table-cell></table:table-row></table:table>`),
			office.Odt},
		{"a row of a table in a cell, sixteen thousand cells wide, repeated twenty thousand times",
			officetest.Odt(`<table:table><table:table-row><table:table-cell><table:table>` +
				`<table:table-row table:number-rows-repeated="20000">` +
				`<table:table-cell table:number-columns-repeated="16384"><text:p>x</text:p></table:table-cell>` +
				`</table:table-row></table:table></table:table-cell></table:table-row></table:table>`),
			office.Odt},
		{"an empty cell in the last column of each of twenty thousand rows",
			officetest.Xlsx(officetest.Workbook{Sheets: []officetest.Sheet{{Name: "S", Data: far.String()}}}),
			office.Xlsx},
		{"ten thousand rows of sixteen thousand empty table cells",
			officetest.Odt(`<table:table>` + strings.Repeat(`<table:table-row>`+
				`<table:table-cell table:number-columns-repeated="16384"/></table:table-row>`, 10000) + `</table:table>`),
			office.Odt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := measure(t, bytes.NewReader(c.data), int64(len(c.data)), c.kind, office.Options{MaxText: smallCap})
			if s.err != nil {
				t.Fatalf("Extract: %v", s.err)
			}
			if len(s.res.Text) > smallCap {
				t.Errorf("%d bytes of text past a cap of %d", len(s.res.Text), smallCap)
			}
			if s.alloc > maxAlloc {
				t.Errorf("a %d-byte file allocated %d MiB under a %d KiB cap", len(c.data), s.alloc>>20, smallCap>>10)
			}
			if s.took > maxTaking {
				t.Errorf("a %d-byte file took %v", len(c.data), s.took)
			}
		})
	}
}

// stored builds a zip whose parts are kept uncompressed, so what a read
// fetches is what it parsed. parts are name and content, in turn.
func stored(parts ...string) []byte {
	entries := make([]officetest.Entry, 0, len(parts)/2)
	for i := 0; i+1 < len(parts); i += 2 {
		entries = append(entries, officetest.Entry{Name: parts[i], Data: parts[i+1], Store: true})
	}
	return officetest.Zip(entries...)
}

func TestARowLongerThanTheCapStopsTheReadThere(t *testing.T) {
	// Two thousand cells of four kilobytes each, every one its own text:
	// eight megabytes in one row. The text cap is reached inside the
	// row, and the read stops there rather than holding the rest of it.
	cell := strings.Repeat("a", 4<<10)
	odfTable := func() string {
		return `<table:table table:name="S"><table:table-row>` +
			strings.Repeat(`<table:table-cell><text:p>`+cell+`</text:p></table:table-cell>`, 2000) +
			`</table:table-row></table:table>`
	}
	odfContent := func(body string) string {
		return `<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" ` +
			`xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" ` +
			`xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0"><office:body>` + body +
			`</office:body></office:document-content>`
	}
	cases := []struct {
		name string
		data []byte
		kind office.Kind
	}{
		{"a workbook", stored(
			"xl/workbook.xml", `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" `+
				`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="S" r:id="r1"/></sheets></workbook>`,
			"xl/_rels/workbook.xml.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
				`<Relationship Id="r1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" `+
				`Target="worksheets/sheet1.xml"/></Relationships>`,
			"xl/worksheets/sheet1.xml", `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1">`+
				strings.Repeat(`<c t="inlineStr"><is><t>`+cell+`</t></is></c>`, 2000)+`</row></sheetData></worksheet>`),
			office.Xlsx},
		{"an OpenDocument spreadsheet", stored("content.xml", odfContent(`<office:spreadsheet>`+odfTable()+`</office:spreadsheet>`)), office.Ods},
		{"an OpenDocument text", stored("content.xml", odfContent(`<office:text>`+odfTable()+`</office:text>`)), office.Odt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &remote{data: c.data}
			ra := office.NewRangeReader(int64(len(c.data)), r.fetch, 0)
			res, err := office.Extract(t.Context(), ra, int64(len(c.data)), c.kind, office.Options{MaxText: smallCap})
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if !res.Truncated || len(res.Text) > smallCap {
				t.Errorf("%d bytes of text, truncated %v; want at most %d, cut short", len(res.Text), res.Truncated, smallCap)
			}
			if r.asked > 1<<20 {
				t.Errorf("fetched %d KiB of a %d KiB file for %d KiB of text", r.asked>>10, len(c.data)>>10, smallCap>>10)
			}
		})
	}
}

// deflated builds a zip of one Word part, compressed, whose header
// declares the given compressed size rather than the real one: zero
// means the real one.
func deflated(t *testing.T, part []byte, declared uint64) []byte {
	t.Helper()
	var comp bytes.Buffer
	fw, err := flate.NewWriter(&comp, flate.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(part); err != nil {
		t.Fatal(err)
	}
	if err := fw.Close(); err != nil {
		t.Fatal(err)
	}
	if declared == 0 {
		declared = uint64(comp.Len())
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{Name: "word/document.xml", Method: zip.Deflate,
		CompressedSize64: declared, UncompressedSize64: uint64(len(part)), CRC32: crc32.ChecksumIEEE(part)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(comp.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const wordOpen = `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`

func TestAPartIsHeldToTheRatioItReallyHas(t *testing.T) {
	// Four megabytes of empty paragraphs deflate to a few kilobytes. A
	// header that declares a hundredth of the real size passes a check
	// of the two declared sizes; the bytes the part pulls from the file
	// do not.
	part := []byte(wordOpen + strings.Repeat("<w:p/>", 4<<20/6) + `</w:body></w:document>`)
	data := deflated(t, part, uint64(len(part))/office.MaxRatio+1)
	oe := refusal(t, data, office.Docx)
	if !oe.Limit || !strings.Contains(oe.Reason, "more than 100 times over") {
		t.Errorf("refusal = %+v, want the ratio named as a limit", oe)
	}
	// The same part, honestly declared, is refused before it is read.
	if oe := refusal(t, deflated(t, part, 0), office.Docx); !oe.Limit {
		t.Errorf("honest declaration: %+v, want a limit", oe)
	}
}

func TestOneLongTokenStopsTheTextRatherThanBeHeldWhole(t *testing.T) {
	// encoding/xml holds a token whole before handing it over. Eight
	// megabytes of text in one run, stored so the ratio does not stop
	// it first, is cut at the token limit.
	body := officetest.WordParagraph("", "before") + `<w:p><w:r><w:t>` + strings.Repeat("a", 8<<20) + `</w:t></w:r></w:p>`
	data := officetest.Zip(officetest.Entry{Name: "word/document.xml", Store: true,
		Data: wordOpen + body + `</w:body></w:document>`})
	s := measure(t, bytes.NewReader(data), int64(len(data)), office.Docx, office.Options{MaxText: smallCap})
	if s.err != nil {
		t.Fatalf("Extract: %v", s.err)
	}
	if s.res.Text != "before\n" || !s.res.Truncated {
		t.Errorf("text = %q, truncated %v; want the paragraph before the token, cut short", s.res.Text, s.res.Truncated)
	}
	if s.alloc > maxAlloc {
		t.Errorf("one %d MiB token allocated %d MiB", 8, s.alloc>>20)
	}
	// Just under the limit, the token is read.
	at := officetest.Zip(officetest.Entry{Name: "word/document.xml", Store: true,
		Data: wordOpen + `<w:p><w:r><w:t>` + strings.Repeat("b", 1000) + `</w:t></w:r></w:p></w:body></w:document>`})
	if res := extract(t, at, office.Docx, office.Options{MaxToken: 1024}); res.Truncated || len(res.Text) != 1001 {
		t.Errorf("a token inside the limit: %d bytes, truncated %v", len(res.Text), res.Truncated)
	}
}

// directoryOnly is n central directory headers with empty names from
// offset zero, then an end record that claims claimed entries in a
// directory of dirSize bytes at offset zero. archive/zip reads headers
// until one is not, and compares only the low 16 bits of the count.
func directoryOnly(n int, claimed uint16, dirSize uint32) []byte {
	b := make([]byte, 0, n*46+22)
	h := make([]byte, 46)
	binary.LittleEndian.PutUint32(h, 0x02014b50)
	for range n {
		b = append(b, h...)
	}
	e := make([]byte, 22)
	binary.LittleEndian.PutUint32(e, 0x06054b50)
	binary.LittleEndian.PutUint16(e[8:], claimed)
	binary.LittleEndian.PutUint16(e[10:], claimed)
	binary.LittleEndian.PutUint32(e[12:], dirSize)
	return append(b, e...)
}

func TestADirectoryLongerThanItsEndRecordSaysIsRefusedAsItIsRead(t *testing.T) {
	// 131 073 headers is two times 65 536 and one: an end record's one
	// entry matches in 16 bits. archive/zip starts from the size the
	// record gives back from its end, or from its offset when a header
	// is there; a file can lie either way.
	const n = 2*65536 + 1
	cases := map[string]struct {
		headers int
		dirSize uint32
	}{
		"its size names one header, and its offset finds them all": {n, 46},
		"its size takes them all in":                               {n, n * 46},
		"one header more than this server reads":                   {office.MaxEntries + 1, (office.MaxEntries + 1) * 46},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			data := directoryOnly(c.headers, 1, c.dirSize)
			r := &remote{data: data}
			ra := office.NewRangeReader(int64(len(data)), r.fetch, 0)
			s := measure(t, ra, int64(len(data)), office.Docx, office.Options{})
			var oe *office.Error
			if !errors.As(s.err, &oe) || !oe.Limit {
				t.Fatalf("err = %v, want a limit", s.err)
			}
			if s.alloc > maxAlloc {
				t.Errorf("refusing it allocated %d MiB", s.alloc>>20)
			}
			if r.asked > 2<<20 {
				t.Errorf("fetched %d KiB of a %d KiB file before refusing it", r.asked>>10, len(data)>>10)
			}
		})
	}
	// Exactly MaxEntries headers are counted and passed to archive/zip.
	data := directoryOnly(office.MaxEntries, office.MaxEntries, office.MaxEntries*46)
	_, err := office.Extract(t.Context(), bytes.NewReader(data), int64(len(data)), office.Docx, office.Options{})
	if oe := (*office.Error)(nil); errors.As(err, &oe) && oe.Limit {
		t.Errorf("exactly %d entries: %v", office.MaxEntries, err)
	}
}

func TestACanceledReadStops(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	data := officetest.Docx(officetest.WordParagraph("", "text"))
	if _, err := office.Extract(ctx, bytes.NewReader(data), int64(len(data)), office.Docx, office.Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestAFileListingMoreThanTheLimitIsRefused(t *testing.T) {
	const ns = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	// many repeats an element n times, each with its own number where
	// the format has one: a map keyed by it grows only when they differ.
	many := func(n int, format string) string {
		var b strings.Builder
		for i := range n {
			b.WriteString(strings.Replace(format, "#", strconv.Itoa(i), 1))
		}
		return b.String()
	}
	const relsNS = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`
	relsOf := func(n int) []byte {
		return stored("word/_rels/document.xml.rels", relsNS+many(n, `<Relationship Id="r#" Type="t" Target="x"/>`)+`</Relationships>`,
			"word/document.xml", wordOpen+`</w:body></w:document>`)
	}
	over := office.MaxListed + 1
	cases := []struct {
		name string
		data []byte
		kind office.Kind
	}{
		{"relationships", relsOf(over), office.Docx},
		{"styles", stored(
			"word/_rels/document.xml.rels", relsNS+`<Relationship Id="s" `+
				`Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`,
			"word/styles.xml", `<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`+
				many(over, `<w:style w:type="paragraph" w:styleId="s#"><w:name w:val="heading 1"/></w:style>`)+`</w:styles>`,
			"word/document.xml", wordOpen+`</w:body></w:document>`), office.Docx},
		{"number formats", officetest.Xlsx(officetest.Workbook{NumFmts: many(over, `<numFmt numFmtId="#" formatCode="0"/>`),
			Sheets: []officetest.Sheet{{Name: "S"}}}), office.Xlsx},
		{"sheets", stored("xl/workbook.xml", `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" `+
			`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`+
			many(over, `<sheet name="s" r:id="r"/>`)+`</sheets></workbook>`), office.Xlsx},
		{"slides", stored("ppt/presentation.xml", `<p:presentation `+ns+`><p:sldIdLst>`+
			many(over, `<p:sldId id="1" r:id="r"/>`)+`</p:sldIdLst></p:presentation>`), office.Pptx},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if oe := refusal(t, c.data, c.kind); !oe.Limit || !strings.Contains(oe.Reason, "lists more than") {
				t.Errorf("refusal = %+v, want the limit on what it lists", oe)
			}
		})
	}
	// Exactly MaxListed is read.
	extract(t, relsOf(office.MaxListed), office.Docx, office.Options{})
}

func TestAFileWhoseContentIsAnotherKindIsRefused(t *testing.T) {
	docx := officetest.Docx(officetest.WordParagraph("", "words"))
	xlsx := officetest.Xlsx(officetest.Workbook{Sheets: []officetest.Sheet{{Name: "S", Data: officetest.Row(1, "cell")}}})
	pptx := officetest.Pptx([]string{"slide"})
	odt := officetest.Odt(`<text:p>words</text:p>`)
	ods := officetest.Ods(`<table:table table:name="S"><table:table-row><table:table-cell><text:p>cell</text:p></table:table-cell></table:table-row></table:table>`)
	odp := officetest.Odp(`<draw:page/>`)
	cases := []struct {
		name string
		data []byte
		kind office.Kind
		want string
	}{
		{"a workbook as a Word document", xlsx, office.Docx, "its content is an Excel workbook, not the Word document its type says"},
		{"a deck as a Word document", pptx, office.Docx, "its content is a PowerPoint presentation, not the Word document its type says"},
		{"a Word document as a workbook", docx, office.Xlsx, "its content is a Word document, not the Excel workbook its type says"},
		{"a Word document as a deck", docx, office.Pptx, "its content is a Word document, not the PowerPoint presentation its type says"},
		{"a spreadsheet as a text", ods, office.Odt, "its content is an OpenDocument spreadsheet, not the OpenDocument text its type says"},
		{"a text as a spreadsheet", odt, office.Ods, "its content is an OpenDocument text, not the OpenDocument spreadsheet its type says"},
		{"a presentation as a text", odp, office.Odt, "its content is an OpenDocument presentation, not the OpenDocument text its type says"},
		{"a text as a presentation", odt, office.Odp, "its content is an OpenDocument text, not the OpenDocument presentation its type says"},
		{"a Word part with another root", officetest.Zip(officetest.Entry{Name: "word/document.xml", Data: `<html/>`}), office.Docx,
			"its content is not the Word document its type says: its part word/document.xml holds html"},
		{"an OpenDocument part with another root", officetest.Zip(officetest.Entry{Name: "content.xml", Data: `<html/>`}), office.Odt,
			"its content is not the OpenDocument text its type says: its part content.xml holds html"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			oe := refusal(t, c.data, c.kind)
			if oe.Limit || oe.Reason != c.want {
				t.Errorf("refusal = %+v, want %q", oe, c.want)
			}
		})
	}
}
