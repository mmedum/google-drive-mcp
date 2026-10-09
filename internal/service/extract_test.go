package service_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

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
	if !strings.Contains(out, "deleted for good") {
		t.Errorf("the result does not say the copy was deleted:\n%s", out)
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
	svc, fake := setup(t, service.Options{})
	addScan(fake, "id-scan-fixture", "abcdefghijklmnopqrst")
	_ = fake.Requested()

	first, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture", MaxChars: 10})
	if err != nil {
		t.Fatalf("first window: %v", err)
	}
	if !strings.Contains(first, "call extract_text again with offset: 10") {
		t.Errorf("the continuation does not name this tool:\n%s", first)
	}
	second, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-scan-fixture", Offset: 10, MaxChars: 10})
	if err != nil {
		t.Fatalf("second window: %v", err)
	}
	if got := body(second); got != "klmnopqrst\n" {
		t.Errorf("second window = %q", got)
	}
	if !strings.Contains(second, "no copy was made this time") {
		t.Errorf("the second window does not say it made no copy:\n%s", second)
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
