package service_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v2/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v2/internal/office/officetest"
	"github.com/mmedum/google-drive-mcp/v2/internal/service"
)

const (
	mimeDocx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	mimeXlsx = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	mimePptx = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	mimeOds  = "application/vnd.oasis.opendocument.spreadsheet"
)

// addOffice puts an Office file with the given bytes in the 2026 folder.
func addOffice(fake *drivetest.Server, id, name, mime string, data []byte) {
	fake.AddFile(id, name, mime, "id-2026-fixture")
	fake.SetContent(id, string(data))
}

// body is the text under a read's header.
func body(out string) string {
	i := strings.Index(out, "---\n")
	if i < 0 {
		return ""
	}
	return out[i+4:]
}

// downloads counts the media requests the fake served, and clears its
// log.
func downloads(fake *drivetest.Server) int {
	n := 0
	for _, r := range fake.Requested() {
		if r.Query.Get("alt") == "media" {
			n++
		}
	}
	return n
}

func TestReadFileReadsAWordDocument(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addOffice(fake, "id-report-fixture", "Report.docx", mimeDocx, officetest.Docx(
		officetest.WordParagraph("Heading1", "Findings")+officetest.WordParagraph("", "All good.")))

	out, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-report-fixture"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got, want := body(out), "# Findings\nAll good.\n"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	if !strings.Contains(out, "showing: text, the whole file") {
		t.Errorf("the header does not say what the text is:\n%s", out)
	}
}

func TestReadFileReadsAnExcelWorkbookAsCSVOrTSV(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addOffice(fake, "id-sales-fixture", "Sales.xlsx", mimeXlsx, officetest.Xlsx(officetest.Workbook{
		Sheets: []officetest.Sheet{
			{Name: "Totals", Data: officetest.Row(1, "region", "total") + officetest.Row(2, "North, East", "40")},
			{Name: "Raw", Data: officetest.Row(1, "not shown")},
		},
	}))

	csv, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-sales-fixture"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got, want := body(csv), "region,total\n\"North, East\",40\n"; got != want {
		t.Errorf("csv = %q, want %q", got, want)
	}
	for _, want := range []string{"FIRST SHEET", `"Totals", one of 2`} {
		if !strings.Contains(csv, want) {
			t.Errorf("the header does not say %q:\n%s", want, csv)
		}
	}

	tsv, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-sales-fixture", Format: "tsv"})
	if err != nil {
		t.Fatalf("ReadFile as tsv: %v", err)
	}
	if got, want := body(tsv), "region\ttotal\nNorth, East\t40\n"; got != want {
		t.Errorf("tsv = %q, want %q", got, want)
	}

	_, err = svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-sales-fixture", Format: "xlsx"})
	if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Errorf("format xlsx: err = %v, want [invalid]", err)
	}
}

func TestReadFileNamesTheFormatInItsContinuation(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addOffice(fake, "id-sales-fixture", "Sales.xlsx", mimeXlsx, officetest.Xlsx(officetest.Workbook{
		Sheets: []officetest.Sheet{{Name: "Totals", Data: officetest.Row(1, "region", "total") + officetest.Row(2, "North", "40")}},
	}))
	out, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-sales-fixture", Format: "TSV", MaxChars: 5})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// Without the format the next call would read the csv, whose
	// offsets are another text's.
	if !strings.Contains(out, "call read_file again with offset: 5 and format: tsv\n") {
		t.Errorf("the continuation does not carry the format:\n%s", out)
	}
}

func TestReadFileRefusesAFormatForADocumentThatIsNotASheet(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addOffice(fake, "id-report-fixture", "Report.docx", mimeDocx, officetest.Docx(officetest.WordParagraph("", "x")))
	_, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-report-fixture", Format: "csv"})
	if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Errorf("err = %v, want [invalid]", err)
	}
}

func TestReadFileReadsADeckAndSaysHowManySlides(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addOffice(fake, "id-deck-fixture", "Deck.pptx", mimePptx, officetest.Pptx([]string{"Welcome"}, []string{"Plan"}))

	out, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-deck-fixture"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got, want := body(out), "--- slide 1 ---\nWelcome\n\n--- slide 2 ---\nPlan\n"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	if !strings.Contains(out, "The deck has 2 slides.") {
		t.Errorf("the header does not count the slides:\n%s", out)
	}
}

func TestReadFilePagesAnOfficeFileWithoutReadingItAgain(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addOffice(fake, "id-report-fixture", "Report.docx", mimeDocx, officetest.Docx(
		officetest.WordParagraph("", "abcdefghij")+officetest.WordParagraph("", "klmnopqrst")))
	_ = fake.Requested()

	first, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-report-fixture", MaxChars: 11})
	if err != nil {
		t.Fatalf("first window: %v", err)
	}
	if got := body(first); got != "abcdefghij\n" {
		t.Errorf("first window = %q", got)
	}
	if !strings.Contains(first, "offset: 11") {
		t.Errorf("no continuation offered:\n%s", first)
	}
	fetched := downloads(fake)
	if fetched == 0 {
		t.Fatal("the first window fetched nothing")
	}

	second, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-report-fixture", Offset: 11, MaxChars: 11})
	if err != nil {
		t.Fatalf("second window: %v", err)
	}
	if got := body(second); got != "klmnopqrst\n" {
		t.Errorf("second window = %q", got)
	}
	if n := downloads(fake); n != 0 {
		t.Errorf("the second window fetched the file again (%d requests)", n)
	}
}

func TestReadFileReadsOfficeFilesInReadOnlyMode(t *testing.T) {
	svc, fake := setup(t, service.Options{ReadOnly: true})
	addOffice(fake, "id-budget-ods-fixture", "Budget.ods", mimeOds, officetest.Ods(
		`<table:table table:name="Plan"><table:table-row><table:table-cell office:value-type="float" office:value="7"/>`+
			`</table:table-row></table:table>`))
	out, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-budget-ods-fixture"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := body(out); got != "7\n" {
		t.Errorf("text = %q, want %q", got, "7\n")
	}
}

func TestReadFileRefusesTheOldBinaryOfficeFormats(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	cases := map[string]string{
		"application/msword":            "convert_to: doc",
		"application/vnd.ms-excel":      "convert_to: sheet",
		"application/vnd.ms-powerpoint": "convert_to: slides",
	}
	for mime, want := range cases {
		id := "id-legacy-" + want[len(want)-3:] + "-fixture"
		fake.AddFile(id, "old file", mime, "id-2026-fixture")
		_, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: id})
		if err == nil || !strings.HasPrefix(err.Error(), "[unsupported]") || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want [unsupported] naming %q", mime, err, want)
		}
	}
}

func TestReadFileSaysAnOfficeFileIsDamagedOrTooLarge(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addOffice(fake, "id-broken-fixture", "Broken.docx", mimeDocx, []byte("this was never a zip archive at all"))
	_, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-broken-fixture"})
	if err == nil || !strings.HasPrefix(err.Error(), "[unsupported]") || !strings.Contains(err.Error(), "could not be read as a Word document") {
		t.Errorf("damaged: err = %v, want [unsupported] saying it could not be read as a Word document", err)
	}

	bomb := officetest.Zip(officetest.Entry{Name: "word/document.xml", Data: strings.Repeat(" ", 4<<20)})
	addOffice(fake, "id-bomb-fixture", "Bomb.docx", mimeDocx, bomb)
	_, err = svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-bomb-fixture"})
	if err == nil || !strings.HasPrefix(err.Error(), "[unsupported]") || !strings.Contains(err.Error(), "larger than this server reads") {
		t.Errorf("bomb: err = %v, want [unsupported] saying it is larger than this server reads", err)
	}
}

func TestReadFileReportsAFailedFetchAsOneAndNotAsDamage(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addOffice(fake, "id-report-fixture", "Report.docx", mimeDocx, officetest.Docx(officetest.WordParagraph("", "x")))
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if r.URL.Query().Get("alt") != "media" {
			return nil
		}
		return &drivetest.Failure{Status: 404, Reason: "notFound", Message: "File not found."}
	}
	_, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-report-fixture"})
	if err == nil || !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Errorf("err = %v, want the fetch's own failure, [not_found]", err)
	}
}

func TestReadFileRefusesARangeAnsweredWithTheWholeFile(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	// Big enough that the end of the file, where a zip keeps its
	// directory, is not at offset zero.
	addOffice(fake, "id-report-fixture", "Report.docx", mimeDocx, officetest.Zip(
		officetest.Entry{Name: "word/media/image1.png", Data: noise(200 << 10), Store: true},
		officetest.Entry{Name: "word/document.xml", Data: "<x/>"}))
	fake.IgnoreRange = true
	_, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-report-fixture"})
	if err == nil || !strings.HasPrefix(err.Error(), "[unexpected]") {
		t.Errorf("err = %v, want [unexpected]", err)
	}
}

func TestOfficeResourceIsTheTextAlone(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addOffice(fake, "id-sales-fixture", "Sales.xlsx", mimeXlsx, officetest.Xlsx(officetest.Workbook{
		Sheets: []officetest.Sheet{{Name: "S", Data: officetest.Row(1, "a", "b")}}}))
	res, err := svc.ResourceText(t.Context(), "id-sales-fixture")
	if err != nil {
		t.Fatalf("ResourceText: %v", err)
	}
	if res.Text != "a,b\n" || res.MimeType != "text/csv" {
		t.Errorf("resource = %q as %s, want \"a,b\\n\" as text/csv", res.Text, res.MimeType)
	}
}

// noise is bytes that do not compress.
func noise(n int) string {
	b := make([]byte, n)
	x := uint32(2463534242)
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x)
	}
	return string(b)
}
