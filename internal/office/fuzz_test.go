package office_test

import (
	"bytes"
	"testing"

	"github.com/mmedum/google-drive-mcp/v2/internal/office"
	"github.com/mmedum/google-drive-mcp/v2/internal/office/officetest"
)

// fuzzLimits are small, so a fuzzed input reaches them often.
var fuzzLimits = office.Options{MaxText: 4096, MaxRead: 1 << 20}

var allKinds = []office.Kind{office.Docx, office.Xlsx, office.Pptx, office.Odt, office.Ods, office.Odp}

// checkBounded runs one input as every kind and holds what every read
// must keep to: no panic, and never more text than the cap.
func checkBounded(t *testing.T, data []byte) {
	for _, kind := range allKinds {
		res, err := office.Extract(bytes.NewReader(data), int64(len(data)), kind, fuzzLimits)
		if err != nil {
			continue
		}
		if len(res.Text) > fuzzLimits.MaxText {
			t.Fatalf("kind %v: %d bytes of text past a cap of %d", kind, len(res.Text), fuzzLimits.MaxText)
		}
	}
}

// FuzzExtract feeds whole files: damaged zips, truncated ones, and the
// builders' own output for the mutator to start from.
func FuzzExtract(f *testing.F) {
	f.Add(officetest.Docx(officetest.WordParagraph("Heading1", "seed") + officetest.WordTable([]string{"a", "b"})))
	f.Add(officetest.Xlsx(officetest.Workbook{Shared: []string{"s"}, CellXfs: `<xf numFmtId="0"/><xf numFmtId="14"/>`,
		Sheets: []officetest.Sheet{{Name: "S", Data: `<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" s="1"><v>45000</v></c></row>`}}}))
	f.Add(officetest.Pptx([]string{"one"}, []string{"two"}))
	f.Add(officetest.Odt(`<text:h>t</text:h><text:list><text:list-item><text:p>i</text:p></text:list-item></text:list>`))
	f.Add(officetest.Ods(`<table:table table:name="S"><table:table-row table:number-rows-repeated="3">` +
		`<table:table-cell table:number-columns-repeated="2" office:value-type="float" office:value="1"/></table:table-row></table:table>`))
	f.Add(officetest.Odp(`<draw:page><draw:frame><draw:text-box><text:p>p</text:p></draw:text-box></draw:frame></draw:page>`))
	f.Add([]byte("PK\x05\x06" + string(make([]byte, 18))))
	f.Add([]byte{})
	f.Fuzz(checkBounded)
}

// FuzzPartXML feeds well-formed containers with arbitrary XML in the
// part that carries the text, which reaches the parsers far more often
// than a mutated zip does.
func FuzzPartXML(f *testing.F) {
	f.Add(`<w:p><w:r><w:t>x</w:t><w:tab/></w:r></w:p>`, uint8(0))
	f.Add(`<w:tbl><w:tr><w:tc><w:tbl><w:tr><w:tc><w:p/></w:tc></w:tr></w:tbl></w:tc></w:tr></w:tbl>`, uint8(0))
	f.Add(`<row r="9999999"><c r="XFD1" t="b"><v>1</v></c><c r="ZZZZ9" s="999"><v>1e308</v></c></row>`, uint8(1))
	f.Add(`<a:p><a:r><a:t>x</a:t></a:r><a:br/></a:p>`, uint8(2))
	f.Add(`<text:p>a<text:s text:c="999999999"/></text:p><table:table><table:table-row table:number-rows-repeated="99999999">`+
		`<table:table-cell table:number-columns-repeated="99999999"><text:p>x</text:p></table:table-cell></table:table-row></table:table>`, uint8(3))
	f.Add(`<table:table><table:table-row table:number-rows-repeated="1048576"><table:table-cell office:value-type="time" `+
		`office:time-value="PT99999999H"/></table:table-row></table:table>`, uint8(4))
	f.Add(`<draw:page><presentation:notes><text:p>n</text:p></presentation:notes></draw:page>`, uint8(5))
	f.Fuzz(func(t *testing.T, xml string, which uint8) {
		var data []byte
		switch which % 6 {
		case 0:
			data = officetest.Docx(xml)
		case 1:
			data = officetest.Xlsx(officetest.Workbook{CellXfs: `<xf numFmtId="22"/><xf numFmtId="46"/>`,
				NumFmts: `<numFmt numFmtId="164" formatCode="` + xml + `"/>`,
				Sheets:  []officetest.Sheet{{Name: "S", Data: xml}}})
		case 2:
			data = officetest.PptxRaw(officetest.SlideXML(xml), xml)
		case 3:
			data = officetest.Odt(xml)
		case 4:
			data = officetest.Ods(xml)
		case 5:
			data = officetest.Odp(xml)
		}
		checkBounded(t, data)
	})
}
