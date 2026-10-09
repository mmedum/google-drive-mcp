package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/v2/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v2/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v2/internal/service"
)

// addScan puts a PDF in the 2026 folder whose bytes the fake's import
// hands back as the text it read, standing in for Google's OCR.
func addScan(fake *drivetest.Server, id, text string, opts ...drivetest.FileOpt) {
	f := fake.AddFile(id, "receipt.pdf", "application/pdf", "id-2026-fixture")
	fake.SetContent(id, text)
	// After the content, which sets the size: an option may stand for a
	// file larger than the text it carries.
	for _, o := range opts {
		o(f)
	}
}

// copies are the Google Docs extract_text made and left in the fake.
func copies(fake *drivetest.Server) []*gdrive.File {
	var out []*gdrive.File
	for _, f := range fake.Files {
		if f.AppProperties[service.ExtractMarkerKey] != "" {
			out = append(out, f)
		}
	}
	return out
}

// requests counts what the fake served by method and path suffix, and
// clears its log.
func requests(fake *drivetest.Server) (copied, deleted int, copyQuery []string) {
	for _, r := range fake.Requested() {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/copy"):
			copied++
			copyQuery = append(copyQuery, r.Query.Encode())
		case r.Method == http.MethodDelete:
			deleted++
		}
	}
	return copied, deleted, copyQuery
}

func TestExtractTextReadsAScanAndDeletesItsCopy(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addScan(fake, "id-scan-fixture", "Invoice 7\nTotal: 42\n")
	_ = fake.Requested()

	out, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture", OCRLanguage: "de"})
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if got := body(out); got != "Invoice 7\nTotal: 42\n" {
		t.Errorf("text = %q", got)
	}
	// The copy is named by the id it was deleted under, so get_file on
	// it can show it is gone.
	named := regexp.MustCompile(`copy in the root of My Drive, id (\S+), which was deleted for good\.`).FindStringSubmatch(out)
	if len(named) != 2 || named[1] == "id-scan-fixture" || fake.Count("/files/"+named[1]) == 0 || fake.Files[named[1]] != nil {
		t.Errorf("the result does not name the copy it deleted:\n%s", out)
	}
	copied, deleted, query := requests(fake)
	if copied != 1 || deleted != 1 {
		t.Errorf("%d copies and %d deletes, want one of each", copied, deleted)
	}
	if len(query) == 1 && (!strings.Contains(query[0], "ignoreDefaultVisibility=true") || !strings.Contains(query[0], "ocrLanguage=de")) {
		t.Errorf("the copy was asked for with %s; want ignoreDefaultVisibility and the language hint", query[0])
	}
	if left := copies(fake); len(left) != 0 {
		t.Errorf("a copy was left behind: %s", left[0].Name)
	}
}

func TestExtractTextPagesWithoutASecondCopy(t *testing.T) {
	now := testNow
	svc, fake := setup(t, service.Options{Now: func() time.Time { return now }})
	addScan(fake, "id-scan-fixture", "abcdefghijklmnopqrst")
	_ = fake.Requested()

	first, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture", MaxChars: 10})
	if err != nil {
		t.Fatalf("first window: %v", err)
	}
	if !strings.Contains(first, "call extract_text again with offset: 10") {
		t.Errorf("the continuation does not name this tool:\n%s", first)
	}
	now = now.Add(2 * time.Minute)
	second, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture", Offset: 10, MaxChars: 10})
	if err != nil {
		t.Fatalf("second window: %v", err)
	}
	if got := body(second); got != "klmnopqrst\n" {
		t.Errorf("second window = %q", got)
	}
	if !strings.Contains(second, "This is the text read 2 minutes ago, kept for paging; no copy was made this time.") {
		t.Errorf("the second window does not say when the text was read and that it made no copy:\n%s", second)
	}
	if copied, _, _ := requests(fake); copied != 1 {
		t.Errorf("paging made %d copies, want 1", copied)
	}
}

func TestExtractTextKeepsTheCopyWhenAsked(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addScan(fake, "id-scan-fixture", "kept text")
	_ = fake.Requested()

	out, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture", KeepCopy: true})
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	left := copies(fake)
	if len(left) != 1 {
		t.Fatalf("%d copies left, want the one kept", len(left))
	}
	kept := left[0]
	if kept.MimeType != gdrive.MimeDocument || kept.Parent() != drivetest.RootFolderID || kept.Name != "Copy of receipt.pdf" {
		t.Errorf("kept copy = %q, %s, in %s; want a Google Doc named \"Copy of receipt.pdf\" in My Drive's root",
			kept.Name, kept.MimeType, kept.Parent())
	}
	if !strings.Contains(out, kept.ID) {
		t.Errorf("the result does not name the kept copy's id %s:\n%s", kept.ID, out)
	}
	if _, deleted, _ := requests(fake); deleted != 0 {
		t.Errorf("a kept copy was deleted (%d deletes)", deleted)
	}

	// The marker the copy carries is the one an ambiguous outcome tells
	// the caller to search for, and search_files takes that query.
	query := "appProperties has { key='" + service.ExtractMarkerKey + "' and value='" +
		kept.AppProperties[service.ExtractMarkerKey] + "' }"
	found, err := svc.Search(t.Context(), service.SearchInput{RawQuery: query})
	if err != nil {
		t.Fatalf("Search by marker: %v", err)
	}
	if !strings.Contains(found, kept.ID) {
		t.Errorf("a search for the marker did not find the copy:\n%s", found)
	}
}

func TestExtractTextCallsALostCopyAmbiguousAndDoesNotRepeatIt(t *testing.T) {
	cases := map[string]drivetest.Failure{
		"a server error":   {Status: 500, Reason: "backendError", Message: "Backend Error"},
		"a cut connection": {Hijack: true},
	}
	for name, failure := range cases {
		t.Run(name, func(t *testing.T) {
			svc, fake := setup(t, service.Options{})
			addScan(fake, "id-scan-fixture", "text")
			fake.Fail = func(r *http.Request) *drivetest.Failure {
				if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/copy") {
					f := failure
					return &f
				}
				return nil
			}
			_ = fake.Requested()
			_, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture"})
			if err == nil || !strings.HasPrefix(err.Error(), "[ambiguous_outcome]") {
				t.Fatalf("err = %v, want [ambiguous_outcome]", err)
			}
			for _, want := range []string{"appProperties has { key='" + service.ExtractMarkerKey + "'", "search_files", "trash_file"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error does not say %q: %v", want, err)
				}
			}
			if copied, _, _ := requests(fake); copied != 1 {
				t.Errorf("the copy was sent %d times, want once: a copy without its own id is never repeated", copied)
			}
		})
	}
}

func TestExtractTextReportsACopyItCouldNotDelete(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addScan(fake, "id-scan-fixture", "the text")
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if r.Method == http.MethodDelete {
			return &drivetest.Failure{Status: 403, Reason: "insufficientFilePermissions", Message: "The user does not have sufficient permissions for this file."}
		}
		return nil
	}
	out, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture"})
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if got := body(out); got != "the text\n" {
		t.Errorf("text = %q; the text is still the result when only the cleanup failed", got)
	}
	left := copies(fake)
	if len(left) != 1 {
		t.Fatalf("%d copies left, want the one that could not be deleted", len(left))
	}
	if !strings.Contains(out, "could not be deleted") || !strings.Contains(out, left[0].ID) {
		t.Errorf("the result does not name the leftover copy %s:\n%s", left[0].ID, out)
	}
}

func TestExtractTextDeletesItsCopyWhenTheExportFails(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addScan(fake, "id-scan-fixture", "text")
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if strings.HasSuffix(r.URL.Path, "/export") {
			return &drivetest.Failure{Status: 403, Reason: "exportSizeLimitExceeded", Message: "This file is too large to be exported."}
		}
		return nil
	}
	_, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture"})
	if err == nil || !strings.Contains(err.Error(), "The temporary copy was deleted.") {
		t.Errorf("err = %v, want the export's failure and that the copy was deleted", err)
	}
	if left := copies(fake); len(left) != 0 {
		t.Errorf("a copy was left behind after a failed export: %s", left[0].Name)
	}
}

func TestExtractTextDeletesItsCopyEvenWhenTheCallIsCanceled(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addScan(fake, "id-scan-fixture", "text")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// The call is canceled as the export arrives, after the copy exists.
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if strings.HasSuffix(r.URL.Path, "/export") {
			cancel()
			return &drivetest.Failure{Status: 503, Reason: "backendError", Message: "Backend Error"}
		}
		return nil
	}
	if _, err := svc.ExtractText(ctx, service.ExtractTextInput{File: "id-scan-fixture"}); err == nil {
		t.Fatal("a canceled call succeeded")
	}
	if left := copies(fake); len(left) != 0 {
		t.Errorf("a canceled call left its copy behind: %s", left[0].Name)
	}
}

func TestExtractTextRefusesBeforeWriting(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-report-fixture", "Report.docx", mimeDocx, "id-2026-fixture")
	fake.AddFile("id-photo-fixture", "photo.webp", "image/webp", "id-2026-fixture")
	fake.AddFile("id-archive-zip-fixture", "bundle.zip", "application/zip", "id-2026-fixture")
	addScan(fake, "id-huge-scan-fixture", "x", drivetest.Size(50<<20+1, ""))
	addScan(fake, "id-locked-scan-fixture", "x", drivetest.WithCapabilities(gdrive.Capabilities{CanCopy: false}))
	cases := []struct {
		file, class string
	}{
		{"id-2026-fixture", "[invalid]"},            // a folder
		{"id-notes-fixture", "[invalid]"},           // a Google Doc: read_file
		{"id-report-fixture", "[invalid]"},          // an Office file: read_file
		{"id-archive-zip-fixture", "[unsupported]"}, // not a PDF or an image
		{"id-photo-fixture", "[unsupported]"},       // an image Google does not import
		{"id-huge-scan-fixture", "[unsupported]"},   // over 50 MB
		{"id-locked-scan-fixture", "[forbidden]"},   // copying is off
	}
	for _, c := range cases {
		_ = fake.Requested()
		_, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: c.file})
		if err == nil || !strings.HasPrefix(err.Error(), c.class) {
			t.Errorf("%s: err = %v, want %s", c.file, err, c.class)
		}
		if copied, _, _ := requests(fake); copied != 0 {
			t.Errorf("%s: a copy was made before the refusal", c.file)
		}
	}

	// Exactly 50 MB is still read.
	addScan(fake, "id-edge-scan-fixture", "edge", drivetest.Size(50<<20, ""))
	if _, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-edge-scan-fixture"}); err != nil {
		t.Errorf("a 50 MB scan: %v", err)
	}
}

func TestExtractTextWarnsAboveGooglesAdvisedSize(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addScan(fake, "id-at-limit-fixture", "a", drivetest.Size(2<<20, ""))
	addScan(fake, "id-over-limit-fixture", "b", drivetest.Size(2<<20+1, ""))
	at, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-at-limit-fixture"})
	if err != nil {
		t.Fatalf("at 2 MB: %v", err)
	}
	over, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-over-limit-fixture"})
	if err != nil {
		t.Fatalf("over 2 MB: %v", err)
	}
	if strings.Contains(at, "2 MB or less") || !strings.Contains(over, "2 MB or less") {
		t.Errorf("the warning belongs past 2 MB only:\nat:\n%s\nover:\n%s", at, over)
	}
}

func TestExtractTextIsRefusedOnAReadOnlyServer(t *testing.T) {
	svc, fake := setup(t, service.Options{ReadOnly: true})
	addScan(fake, "id-scan-fixture", "text")
	_, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture"})
	if err == nil || !strings.HasPrefix(err.Error(), "[forbidden]") {
		t.Errorf("err = %v, want [forbidden]", err)
	}
	// And read_file, in that mode, does not point at it.
	_, err = svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-scan-fixture"})
	if err == nil || strings.Contains(err.Error(), "extract_text") {
		t.Errorf("a read-only read_file refusal names extract_text: %v", err)
	}
}

func TestReadFilePointsAPDFAtExtractText(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addScan(fake, "id-scan-fixture", "text")
	_, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-scan-fixture"})
	if err == nil || !strings.HasPrefix(err.Error(), "[unsupported]") || !strings.Contains(err.Error(), "extract_text") {
		t.Errorf("err = %v, want [unsupported] naming extract_text", err)
	}
}

func TestExtractTextSaysWhenItFoundNoText(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addScan(fake, "id-blank-scan-fixture", "")
	out, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-blank-scan-fixture"})
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if !strings.Contains(out, "found no text") {
		t.Errorf("an empty result does not say so:\n%s", out)
	}
}

func TestExtractTextFallsBackToGooglesListWhenImportFormatsCannotBeRead(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if strings.HasSuffix(r.URL.Path, "/about") {
			return &drivetest.Failure{Status: 403, Reason: "insufficientPermissions", Message: "no"}
		}
		return nil
	}
	fake.AddFile("id-photo-fixture", "photo.webp", "image/webp", "id-2026-fixture")
	fake.AddFile("id-chart-gif-fixture", "chart.gif", "image/gif", "id-2026-fixture")
	fake.SetContent("id-chart-gif-fixture", "chart text")
	if _, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-photo-fixture"}); err == nil ||
		!strings.HasPrefix(err.Error(), "[unsupported]") {
		t.Errorf("webp: err = %v, want [unsupported]: it is not on the import guide's list", err)
	}
	if _, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-chart-gif-fixture"}); err != nil {
		t.Errorf("gif: %v; it is on the import guide's list", err)
	}
}

func TestExtractTextPagesAfterAnotherReadWithoutASecondCopy(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addScan(fake, "id-scan-fixture", "abcdefghijklmnopqrst")
	_ = fake.Requested()
	if _, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture", OCRLanguage: "de", MaxChars: 10}); err != nil {
		t.Fatalf("first window: %v", err)
	}
	// A read in between, which keeps its own text.
	if _, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-notes-fixture"}); err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	second, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture", OCRLanguage: "de", Offset: 10, MaxChars: 10})
	if err != nil {
		t.Fatalf("second window: %v", err)
	}
	if got := body(second); got != "klmnopqrst\n" {
		t.Errorf("second window = %q", got)
	}
	if copied, _, _ := requests(fake); copied != 1 {
		t.Errorf("paging around another read made %d copies, want 1", copied)
	}
}

func TestExtractTextNamesTheLanguageInItsContinuation(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addScan(fake, "id-scan-fixture", "abcdefghijklmnopqrst")
	out, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture", OCRLanguage: "de", MaxChars: 10})
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if !strings.Contains(out, "call extract_text again with offset: 10 and ocr_language: de\n") {
		t.Errorf("the continuation does not carry the language, which is part of what was read:\n%s", out)
	}
}

func TestExtractTextSaysWhenAWindowCameFromASecondReading(t *testing.T) {
	now := testNow
	svc, fake := setup(t, service.Options{Now: func() time.Time { return now }})
	addScan(fake, "id-scan-fixture", "abcdefghijklmnopqrst")
	first, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture", MaxChars: 10})
	if err != nil {
		t.Fatalf("first window: %v", err)
	}
	const shift = "may not start exactly where the last one ended"
	if strings.Contains(first, shift) {
		t.Errorf("a first window warns of a shift:\n%s", first)
	}
	now = now.Add(time.Hour)
	second, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture", Offset: 10, MaxChars: 10})
	if err != nil {
		t.Fatalf("second window: %v", err)
	}
	if !strings.Contains(second, shift) {
		t.Errorf("a window read from a second copy does not say it may have shifted:\n%s", second)
	}
}

func TestExtractTextKeepsEachFilesAndLanguagesTextApart(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	// Two files alike in all but their id, so only the id tells their
	// text apart.
	same := func(f *gdrive.File) {
		f.HeadRevisionID, f.ModifiedTime = "id-revision-fixture", "2026-01-02T03:04:05.000Z"
	}
	addScan(fake, "id-scan-one-fixture", "first file", same)
	addScan(fake, "id-scan-two-fixture", "second file", same)
	_ = fake.Requested()
	for id, want := range map[string]string{"id-scan-one-fixture": "first file\n", "id-scan-two-fixture": "second file\n"} {
		out, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: id})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got := body(out); got != want {
			t.Errorf("%s: text = %q, want %q", id, got, want)
		}
	}
	// Another language is another reading.
	for _, lang := range []string{"de", "fr"} {
		if _, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-one-fixture", OCRLanguage: lang}); err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
	}
	if copied, _, query := requests(fake); copied != 4 || !strings.Contains(query[3], "ocrLanguage=fr") {
		t.Errorf("%d copies, the last asked with %v; want four, the last in French", copied, query)
	}
}

func TestExtractTextKeepsACopyEvenWhenTheTextIsKept(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addScan(fake, "id-scan-fixture", "text")
	if _, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture"}); err != nil {
		t.Fatalf("first read: %v", err)
	}
	out, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture", KeepCopy: true})
	if err != nil {
		t.Fatalf("keep_copy: %v", err)
	}
	left := copies(fake)
	if len(left) != 1 || !strings.Contains(out, left[0].ID) {
		t.Errorf("keep_copy after a kept read left %d copies; want one, named in the result:\n%s", len(left), out)
	}
}

func TestExtractTextDeletesACopyMadeAfterTheCallWasCanceled(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addScan(fake, "id-scan-fixture", "text")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// The call is canceled while the copy is on its way to Google, which
	// makes it anyway.
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/copy") {
			cancel()
		}
		return nil
	}
	_ = fake.Requested()
	_, err := svc.ExtractText(ctx, service.ExtractTextInput{File: "id-scan-fixture", KeepCopy: true})
	if err == nil || !strings.Contains(err.Error(), "canceled") || !strings.Contains(err.Error(), "The copy was deleted") {
		t.Errorf("err = %v, want the cancel, and that the copy was deleted", err)
	}
	if left := copies(fake); len(left) != 0 {
		t.Errorf("a canceled call left an unreported copy: %s", left[0].ID)
	}
	if copied, deleted, _ := requests(fake); copied != 1 || deleted != 1 {
		t.Errorf("%d copies and %d deletes, want one of each", copied, deleted)
	}
}

func TestExtractTextDeletesOnlyTheGoogleDocItsCopyMade(t *testing.T) {
	// Drive's answer to the copy is all that names what to delete for
	// good, so an answer that does not describe a new Google Doc deletes
	// nothing.
	cases := map[string]func(f map[string]any){
		"an answer that is not a Google Doc": func(f map[string]any) { f["mimeType"] = "application/pdf" },
		"an answer naming the source":        func(f map[string]any) { f["id"] = "id-scan-fixture" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			svc, fake := setup(t, service.Options{})
			addScan(fake, "id-scan-fixture", "text")
			fake.Rewrite = func(r *http.Request, status int, body []byte) (int, []byte) {
				if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/copy") {
					return status, body
				}
				var f map[string]any
				if err := json.Unmarshal(body, &f); err != nil {
					t.Fatalf("copy answer: %v", err)
				}
				change(f)
				out, _ := json.Marshal(f)
				return status, out
			}
			_ = fake.Requested()
			out, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture"})
			if _, deleted, _ := requests(fake); deleted != 0 {
				t.Errorf("%d deletes, want none", deleted)
			}
			if said := out + errString(err); !strings.Contains(said, "could not be deleted") {
				t.Errorf("the result does not say the copy is still there: %s", said)
			}
			if fake.Files["id-scan-fixture"] == nil {
				t.Error("the source was deleted")
			}
		})
	}
}

func TestExtractTextNamesTheSearchThatFindsALostCopy(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	addScan(fake, "id-scan-fixture", "text")
	// Google makes the copy, and the answer is lost as a server error.
	fake.Rewrite = func(r *http.Request, status int, body []byte) (int, []byte) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/copy") {
			return http.StatusInternalServerError, []byte(`{"error":{"code":500,"message":"Backend Error","errors":[{"reason":"backendError"}]}}`)
		}
		return status, body
	}
	_, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture"})
	if err == nil || !strings.HasPrefix(err.Error(), "[ambiguous_outcome]") {
		t.Fatalf("err = %v, want [ambiguous_outcome]", err)
	}
	query := regexp.MustCompile(`appProperties has \{ key='[^']+' and value='[^']+' \}`).FindString(err.Error())
	if query == "" {
		t.Fatalf("the error names no marker search: %v", err)
	}
	fake.Rewrite = nil
	found, err := svc.Search(t.Context(), service.SearchInput{RawQuery: query})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	left := copies(fake)
	if len(left) != 1 || !strings.Contains(found, left[0].ID) {
		t.Errorf("the search the error names does not find the copy Google made:\n%s", found)
	}
}

// errString is an error's text, or nothing.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
