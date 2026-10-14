package service_test

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v2/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v2/internal/mediatype"
	"github.com/mmedum/google-drive-mcp/v2/internal/service"
)

// commented is a synthetic Markdown file in the shape the Google Docs
// editor writes one holding a comment: markers around the passage and
// the gdoc_comments block ending the file.
const commented = "# Notes\n\nWe <!-- gdoc_comment_begin: c1 -->ship in May<!-- gdoc_comment_end: c1 -->.\n\n" +
	commentBlock

// commentBlock is the block that ends commented.
const commentBlock = "---\ngdoc_comments:\n  c1:\n    status: ACTIVE\n    head:\n      author: Ada Example\n" +
	"      body: is May real?\n      quote: |-\n        ship in May\n---\n"

const mdID = "id-commented-md-fixture"

func markdownSetup(t *testing.T, content string) (*service.Service, *drivetest.Server, string) {
	t.Helper()
	svc, fake, dir := withLocalDir(t, service.Options{})
	fake.AddFile(mdID, "notes.md", mediatype.Markdown, "id-2026-fixture")
	fake.SetContent(mdID, content)
	return svc, fake, dir
}

func TestUpdateContentFromALocalFileKeepsTheFilesType(t *testing.T) {
	// Drive makes the uploaded bytes' type the file's type. The local
	// file's name said text/plain, which turned a Markdown file into one
	// Google Docs no longer opens as Markdown.
	svc, fake, dir := markdownSetup(t, "# Notes\n")
	if err := os.WriteFile(filepath.Join(dir, "draft.txt"), []byte("# Notes, revised\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{File: mdID, LocalPath: "draft.txt"}); err != nil {
		t.Fatalf("UpdateContent: %v", err)
	}
	if got := fake.Files[mdID].MimeType; got != "text/markdown" {
		t.Errorf("the file became %q, want text/markdown", got)
	}
	if got := fake.Content[mdID]; got != "# Notes, revised\n" {
		t.Errorf("stored %q", got)
	}

	if _, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{
		File: mdID, LocalPath: "draft.txt", MimeType: "text/plain",
	}); err != nil {
		t.Fatalf("UpdateContent with mime_type: %v", err)
	}
	if got := fake.Files[mdID].MimeType; got != "text/plain" {
		t.Errorf("an explicit mime_type gave %q, want text/plain", got)
	}
}

func TestUpdateContentRefusesToLoseAMarkdownFilesComments(t *testing.T) {
	svc, fake, _ := markdownSetup(t, commented)

	_, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{
		File: mdID, Content: "# Notes\n\nWe ship in May.\n\n" + commentBlock,
	})
	if err == nil {
		t.Fatal("a replacement without the comment's markers was accepted")
	}
	for _, want := range []string{"[invalid]", "notes.md holds 1 comment made in Google Docs",
		"comment c1 lost a marker", "allow_comment_changes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
	if fake.Content[mdID] != commented {
		t.Error("the content was replaced anyway")
	}

	// The same text sent as another type: Drive makes that the file's
	// type, and Google Docs no longer reads it as Markdown.
	_, err = svc.UpdateContent(t.Context(), service.UpdateContentInput{File: mdID, Content: commented, MimeType: "text/plain"})
	if err == nil || !strings.Contains(err.Error(), "sending it as text/plain makes it a text file") {
		t.Fatalf("err = %v, want a refusal to retype a file holding comments", err)
	}
	if got := fake.Files[mdID].MimeType; got != "text/markdown" {
		t.Errorf("the file became %q anyway", got)
	}
}

func TestUpdateContentChecksAMarkdownFileUnderTheOlderType(t *testing.T) {
	svc, fake, _ := markdownSetup(t, commented)
	fake.Files[mdID].MimeType = "text/x-markdown"

	if _, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{File: mdID, Content: "# Notes\n"}); err == nil {
		t.Fatal("a text/x-markdown file holding a comment was replaced without the check")
	}
	if fake.Content[mdID] != commented {
		t.Error("the content was replaced anyway")
	}
}

func TestUpdateContentKeepsAMarkdownFilesComments(t *testing.T) {
	svc, fake, dir := markdownSetup(t, commented)
	revised := strings.Replace(commented, "# Notes", "# Release notes", 1)

	got, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{File: mdID, Content: revised})
	if err != nil {
		t.Fatalf("UpdateContent: %v", err)
	}
	if !strings.Contains(got.Text, "Its text still holds the 1 comment made on it in Google Docs") {
		t.Errorf("the result does not say the comment survived:\n%s", got.Text)
	}
	if strings.Contains(got.Text, "landed") || strings.Contains(got.Text, "could not be read") {
		t.Errorf("an update nothing raced reports a save it replaced, or a history it could not read:\n%s", got.Text)
	}
	if fake.Content[mdID] != revised {
		t.Errorf("stored %q", fake.Content[mdID])
	}

	// From a local file, which is read once to check it and once to send
	// it: what lands has to be the whole file, not what the check left.
	again := strings.Replace(revised, "ship in May", "ship in June", 1)
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte(again), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{File: mdID, LocalPath: "notes.md"}); err != nil {
		t.Fatalf("UpdateContent from a local file: %v", err)
	}
	if fake.Content[mdID] != again {
		t.Errorf("stored %q, want the whole local file", fake.Content[mdID])
	}

	// Sent as plain text when the caller says so, it is no longer a
	// Markdown file, and the result says so.
	plain, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{
		File: mdID, Content: again, MimeType: "text/plain", AllowCommentChanges: true,
	})
	if err != nil {
		t.Fatalf("UpdateContent as text/plain: %v", err)
	}
	if !strings.Contains(plain.Text, "It is now a text file: Drive takes the type an upload is sent as") {
		t.Errorf("the result does not say the file stopped being Markdown:\n%s", plain.Text)
	}
}

func TestAllowCommentChangesReplacesAMarkdownFileWithoutReadingIt(t *testing.T) {
	svc, fake, _ := markdownSetup(t, commented)

	if _, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{
		File: mdID, Content: "# Notes\n", AllowCommentChanges: true,
	}); err != nil {
		t.Fatalf("UpdateContent: %v", err)
	}
	if fake.Content[mdID] != "# Notes\n" {
		t.Errorf("stored %q", fake.Content[mdID])
	}
	if n := downloads(fake); n != 0 {
		t.Errorf("the file was downloaded %d time(s) for a check the caller waived", n)
	}
}

func TestUpdateContentReadsOnlyAMarkdownFileForComments(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-serverlog-fixture", "server.log", "text/plain", "id-2026-fixture")
	fake.SetContent("id-serverlog-fixture", commented)

	if _, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{
		File: "id-serverlog-fixture", Content: "replaced\n",
	}); err != nil {
		t.Fatalf("UpdateContent: %v", err)
	}
	if n := downloads(fake); n != 0 {
		t.Errorf("a plain text file was downloaded %d time(s) to look for comments Google Docs keeps only in Markdown", n)
	}
}

func TestUpdateContentSaysWhenItCannotCheckTheComments(t *testing.T) {
	svc, fake, _ := markdownSetup(t, commented)
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if r.Method == http.MethodGet && r.URL.Query().Get("alt") == "media" {
			return &drivetest.Failure{Status: 403, Reason: "cannotDownloadFile", Message: "download refused"}
		}
		return nil
	}

	_, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{File: mdID, Content: "# Notes\n"})
	if err == nil || !strings.Contains(err.Error(), "reading notes.md to check the comments Google Docs keeps in it") ||
		!strings.Contains(err.Error(), "allow_comment_changes") {
		t.Fatalf("err = %v, want a refusal naming the check and the way past it", err)
	}
	if fake.Content[mdID] != commented {
		t.Error("the content was replaced without the check")
	}
}

// withPlatformType makes the platform's own table say ext is typ for
// one test, and puts back what it said. The mime package cannot forget
// an extension, so one it did not know stays known.
func withPlatformType(t *testing.T, ext, typ string) {
	t.Helper()
	prev := mime.TypeByExtension(ext)
	if err := mime.AddExtensionType(ext, typ); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if prev != "" {
			_ = mime.AddExtensionType(ext, prev)
		}
	})
}

func TestCreateFileTypesInlineTextByItsName(t *testing.T) {
	// The machine says something about .log; the registry does not, so
	// what the machine says must not decide.
	withPlatformType(t, ".log", "text/x-log")
	cases := map[string]string{
		"notes.md":       "text/markdown",
		"notes.MARKDOWN": "text/markdown",
		"rows.csv":       "text/csv",
		"notes":          "text/plain",
		"server.log":     "text/plain",
		// Text named as something that is not text stays plain text.
		"picture.png": "text/plain",
	}
	svc, fake := setup(t, service.Options{})
	for name, want := range cases {
		got, err := svc.CreateFile(t.Context(), service.CreateFileInput{Name: name, Content: "# Notes\n"})
		if err != nil {
			t.Fatalf("CreateFile %s: %v", name, err)
		}
		if mime := fake.Files[got.JSON.File.ID].MimeType; mime != want {
			t.Errorf("%s was created as %q, want %q", name, mime, want)
		}
	}
}

func TestCreateFileConvertsInlineTextItsNameWouldNotImport(t *testing.T) {
	// Google imports csv only as a Sheet. Asked for a Doc, the text goes
	// as plain text, which is what it always went as.
	svc, fake := setup(t, service.Options{})
	for convert, want := range map[string]string{"doc": "application/vnd.google-apps.document",
		"sheet": "application/vnd.google-apps.spreadsheet"} {
		got, err := svc.CreateFile(t.Context(), service.CreateFileInput{
			Name: "rows " + convert + ".csv", Content: "a,b\n1,2\n", ConvertTo: convert,
		})
		if err != nil {
			t.Fatalf("CreateFile rows.csv as a %s: %v", convert, err)
		}
		if mime := fake.Files[got.JSON.File.ID].MimeType; mime != want {
			t.Errorf("rows.csv with convert_to %s became %q, want %q", convert, mime, want)
		}
	}
}

func TestUploadFileTypesAMarkdownFileTheSameOnEveryPlatform(t *testing.T) {
	// Go's own table has no .md, so mime.TypeByExtension answers with
	// whatever the machine's tables say, or nothing. This one says
	// something else.
	withPlatformType(t, ".md", "text/x-markdown")
	svc, fake, dir := withLocalDir(t, service.Options{})
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# Notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := svc.UploadFile(t.Context(), service.UploadFileInput{LocalPath: "notes.md"})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if mime := fake.Files[got.JSON.File.ID].MimeType; mime != "text/markdown" {
		t.Errorf("notes.md was uploaded as %q, want text/markdown", mime)
	}
}

func TestAMarkdownFileSaysWhereItsGoogleDocsCommentsAre(t *testing.T) {
	for _, mime := range []string{"text/markdown", "text/x-markdown"} {
		t.Run(mime, func(t *testing.T) { saysWhereItsCommentsAre(t, mime) })
	}
}

func saysWhereItsCommentsAre(t *testing.T, mime string) {
	svc, fake, _ := markdownSetup(t, commented)
	fake.Files[mdID].MimeType = mime

	card, err := svc.GetFile(t.Context(), service.GetFileInput{File: mdID})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	read, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: mdID})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	listing, err := svc.ListComments(t.Context(), service.ListCommentsInput{File: mdID})
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	for what, c := range map[string]struct{ out, want string }{
		"the file card": {card, "a comment made on it in Google Docs is kept in the file's own text"},
		"a read":        {read, "an entry in the gdoc_comments block that ends the file"},
		"the listing":   {listing, "list_comments does not show it"},
	} {
		if !strings.Contains(c.out, c.want) {
			t.Errorf("%s does not say %q:\n%s", what, c.want, c.out)
		}
	}
	if !strings.Contains(read, "ship in May") {
		t.Errorf("the read does not carry the file's text:\n%s", read)
	}
}

func TestUpdateContentRefusesAMarkdownFileSavedWhileItWasChecked(t *testing.T) {
	// A person saves in Google Docs while the comment check reads the
	// file: the look just before the upload sees the head move.
	svc, fake, _ := markdownSetup(t, commented)
	saved := strings.Replace(commented, "# Notes", "# Notes, as the person saved them", 1)
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if r.Method == http.MethodGet && r.URL.Query().Get("alt") == "media" {
			fake.SetContent(mdID, saved)
		}
		return nil
	}

	_, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{
		File: mdID, Content: strings.Replace(commented, "# Notes", "# Release notes", 1),
	})
	if err == nil || !strings.Contains(err.Error(), "[exists]") ||
		!strings.Contains(err.Error(), "changed while this was being checked") {
		t.Fatalf("err = %v, want a refusal that the file moved during the check", err)
	}
	if fake.Content[mdID] != saved {
		t.Errorf("the person's save was replaced: stored %q", fake.Content[mdID])
	}
}

func TestUpdateContentNamesASaveItReplaced(t *testing.T) {
	// A save that lands after the last look and before the upload cannot
	// be stopped; it can be found in the history afterwards and named.
	svc, fake, _ := markdownSetup(t, "# Notes\n")
	raced := false
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if !raced && strings.Contains(r.URL.Path, "/upload/") {
			raced = true
			fake.SetContent(mdID, "# Notes, as the person saved them\n")
		}
		return nil
	}

	got, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{
		File: mdID, Content: "# Release notes\n", KeepPreviousRevision: true,
	})
	if err != nil {
		t.Fatalf("UpdateContent: %v", err)
	}
	revs := fake.Revisions[mdID]
	if len(revs) != 3 {
		t.Fatalf("the fake holds %d revisions, want the checked one, the save and the upload", len(revs))
	}
	checked, save := revs[0], revs[1]
	want := "A save landed after the check and before this upload, which replaced it: revision " + save.ID +
		". Drive keeps it 30 days unless it is pinned"
	if !strings.Contains(got.Text, want) {
		t.Errorf("the result does not name the save it replaced:\n%s\nwant %q", got.Text, want)
	}
	// keep_previous_revision pins what the upload replaced, which is the
	// person's save, not the revision that was checked.
	if !save.KeepForever || checked.KeepForever {
		t.Errorf("pinned: checked %v, save %v; want the save only", checked.KeepForever, save.KeepForever)
	}
	if !strings.Contains(got.Text, "The previous revision, "+save.ID+", is pinned") {
		t.Errorf("the result does not name the revision it pinned:\n%s", got.Text)
	}
	// Only the order of the history matters here, so only ids are asked.
	for _, r := range fake.Requests {
		if strings.HasSuffix(r.Path, "/revisions") && r.Query.Get("fields") != "nextPageToken,revisions(id)" {
			t.Errorf("the history was read with fields %q, want the ids only", r.Query.Get("fields"))
		}
	}
}
