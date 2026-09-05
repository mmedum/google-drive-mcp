package service_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

func TestCreateFileMakesAnEmptyGoogleDoc(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	got, err := svc.CreateFile(t.Context(), service.CreateFileInput{
		Name: "Plan", Kind: "doc", Parent: "/Projects/2026",
	})
	if err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	if got.JSON.File.Kind != "Google Doc" {
		t.Errorf("created %q", got.JSON.File.Kind)
	}
	if !strings.Contains(got.Text, "created: Plan — Google Doc") {
		t.Errorf("the text does not lead with what happened:\n%s", got.Text)
	}
	if got.JSON.Summary != got.Text {
		t.Error("the structured result does not carry the text a client may not show")
	}
	f := fake.Files[got.JSON.File.ID]
	if f == nil || f.Parent() != "id-2026-fixture" {
		t.Errorf("the file landed at %v", f)
	}
}

func TestCreateFileWritesInlineTextAndConverts(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	got, err := svc.CreateFile(t.Context(), service.CreateFileInput{
		Name: "notes.md", Content: "# Notes\n\nsomething\n", MimeType: "text/markdown",
	})
	if err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	if fake.Content[got.JSON.File.ID] != "# Notes\n\nsomething\n" {
		t.Errorf("stored %q", fake.Content[got.JSON.File.ID])
	}

	converted, err := svc.CreateFile(t.Context(), service.CreateFileInput{
		Name: "Notes as a doc", Content: "# Notes\n", MimeType: "text/markdown", ConvertTo: "doc",
	})
	if err != nil {
		t.Fatalf("CreateFile converting: %v", err)
	}
	if converted.JSON.File.MimeType != gdrive.MimeDocument {
		t.Errorf("converted to %q, want a Google Doc", converted.JSON.File.MimeType)
	}
}

func TestCreateFileRefusesAmbiguousArguments(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	cases := map[string]service.CreateFileInput{
		"neither kind nor content": {Name: "x"},
		"both kind and content":    {Name: "x", Kind: "doc", Content: "hello"},
		"no name":                  {Kind: "doc"},
		"an unknown kind":          {Name: "x", Kind: "novel"},
		"an unknown conversion":    {Name: "x", Content: "hello", ConvertTo: "novel"},
	}
	for name, in := range cases {
		if _, err := svc.CreateFile(t.Context(), in); err == nil {
			t.Errorf("create_file with %s succeeded", name)
		}
	}
}

func TestCreateRefusesADuplicateNameUnlessAsked(t *testing.T) {
	svc, _ := setup(t, service.Options{})

	_, err := svc.CreateFile(t.Context(), service.CreateFileInput{
		Name: "Budget.xlsx", Kind: "sheet", Parent: "/Projects/2026",
	})
	if err == nil {
		t.Fatal("a second Budget.xlsx was created beside the first")
	}
	if !strings.Contains(err.Error(), "[exists]") || !strings.Contains(err.Error(), "id-budget-fixture") {
		t.Errorf("the refusal should name the file that is already there: %v", err)
	}

	if _, err := svc.CreateFile(t.Context(), service.CreateFileInput{
		Name: "Budget.xlsx", Kind: "sheet", Parent: "/Projects/2026", AllowDuplicate: true,
	}); err != nil {
		t.Fatalf("allow_duplicate did not permit it: %v", err)
	}
}

func TestCreateFileCarriesAPreGeneratedIDWhereDriveTakesOne(t *testing.T) {
	// A pre-generated id is what makes a create idempotent. Drive refuses
	// one for the Docs Editors formats — "Generated IDs are not supported
	// for Docs Editors formats", observed live on 2026-09-05 — so a new
	// Doc is created without one, and everything else carries one.
	svc, fake := setup(t, service.Options{})

	if _, err := svc.CreateFile(t.Context(), service.CreateFileInput{
		Name: "rows.csv", Content: "a,b\n", MimeType: "text/csv",
	}); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	if fake.Count("/files/generateIds") == 0 {
		t.Error("a blob create asked for no id, so a retry could make a second file")
	}
	fake.Requested()

	if _, err := svc.CreateFile(t.Context(), service.CreateFileInput{Name: "Plan", Kind: "doc"}); err != nil {
		t.Fatalf("CreateFile of a doc: %v", err)
	}
	if fake.Count("/files/generateIds") != 0 {
		t.Error("a Docs Editors create asked Drive for an id it will refuse to accept")
	}
}

func TestCreateFileOfEveryGoogleKindIsAccepted(t *testing.T) {
	// The whole Docs Editors set, because the refusal is per format and
	// a table that names four of five is a bug nobody sees.
	svc, _ := setup(t, service.Options{})
	for _, kind := range service.NewKinds() {
		if _, err := svc.CreateFile(t.Context(), service.CreateFileInput{
			Name: "New " + kind, Kind: kind,
		}); err != nil {
			t.Errorf("create_file kind=%s: %v", kind, err)
		}
	}
}

func TestUploadFileSendsALocalFile(t *testing.T) {
	svc, fake, dir := withLocalDir(t, service.Options{})
	if err := os.WriteFile(filepath.Join(dir, "rows.csv"), []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := svc.UploadFile(t.Context(), service.UploadFileInput{LocalPath: "rows.csv", Parent: "/Projects"})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if got.JSON.File.Name != "rows.csv" {
		t.Errorf("uploaded as %q", got.JSON.File.Name)
	}
	if got.JSON.File.MimeType != "text/csv" {
		t.Errorf("type = %q, want text/csv from the extension", got.JSON.File.MimeType)
	}
	if fake.Content[got.JSON.File.ID] != "a,b\n1,2\n" {
		t.Errorf("stored %q", fake.Content[got.JSON.File.ID])
	}
}

func TestUploadFileChunksALargeFile(t *testing.T) {
	svc, fake, dir := withLocalDir(t, service.Options{})
	body := strings.Repeat("x", gapi.MaxMultipartUpload+1024)
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := svc.UploadFile(t.Context(), service.UploadFileInput{LocalPath: "big.bin"})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if fake.Content[got.JSON.File.ID] != body {
		t.Error("the uploaded bytes are not the bytes on disk")
	}
	if fake.Count("/sessions/") == 0 {
		t.Error("a file above the multipart limit did not go through a resumable session")
	}
}

func TestUploadFileRefusesAMissingFile(t *testing.T) {
	svc, _, _ := withLocalDir(t, service.Options{})
	_, err := svc.UploadFile(t.Context(), service.UploadFileInput{LocalPath: "nothing.txt"})
	if err == nil || !strings.Contains(err.Error(), "[not_found]") {
		t.Fatalf("err = %v, want not_found", err)
	}
}

func TestUpdateContentReplacesBytesAndKeepsTheOldRevision(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-serverlog-fixture", "server.log", "text/plain", "id-2026-fixture")
	fake.SetContent("id-serverlog-fixture", "old")
	before := fake.Files["id-serverlog-fixture"].HeadRevisionID

	got, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{
		File: "id-serverlog-fixture", Content: "new content", KeepPreviousRevision: true,
	})
	if err != nil {
		t.Fatalf("UpdateContent: %v", err)
	}
	if fake.Content["id-serverlog-fixture"] != "new content" {
		t.Errorf("stored %q", fake.Content["id-serverlog-fixture"])
	}
	if !strings.Contains(got.Text, "replaced the content") {
		t.Errorf("the result does not say what happened:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "pinned") {
		t.Errorf("keep_previous_revision was not reported:\n%s", got.Text)
	}
	if !fake.Revisions["id-serverlog-fixture"][0].KeepForever {
		t.Error("the previous revision was not pinned")
	}
	if fake.Files["id-serverlog-fixture"].HeadRevisionID == before {
		t.Error("the head revision did not move")
	}
}

func TestUpdateContentChecksTheRevisionItWasGiven(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-serverlog-fixture", "server.log", "text/plain", "id-2026-fixture")
	fake.SetContent("id-serverlog-fixture", "old")

	_, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{
		File: "id-serverlog-fixture", Content: "new", ExpectHeadRevision: "id-revision-that-moved-on",
	})
	if err == nil || !strings.Contains(err.Error(), "changed since you read it") {
		t.Fatalf("err = %v, want a refusal that the file moved on", err)
	}
	if fake.Content["id-serverlog-fixture"] != "old" {
		t.Error("the content was replaced anyway")
	}
}

func TestUpdateContentRefusesWhatHasNoBytes(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	cases := map[string]string{
		"a Google Doc": "id-notes-fixture",
		"a folder":     "id-2026-fixture",
		"a shortcut":   "id-shortcut-fixture",
	}
	for what, ref := range cases {
		_, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{File: ref, Content: "x"})
		if err == nil {
			t.Errorf("update_content on %s succeeded", what)
			continue
		}
		if what == "a Google Doc" && !strings.Contains(err.Error(), "Docs, Sheets or Slides API") {
			t.Errorf("the refusal does not say where a Doc is edited: %v", err)
		}
	}
}

func TestWritesAreRefusedInReadOnlyMode(t *testing.T) {
	svc, _ := setup(t, service.Options{ReadOnly: true})
	if _, err := svc.CreateFolder(t.Context(), service.CreateFolderInput{Name: "Reports"}); err == nil ||
		!strings.Contains(err.Error(), "read-only") {
		t.Fatalf("err = %v, want a read-only refusal", err)
	}
}

func TestACreateMakesThePathItAmbiguatesAmbiguous(t *testing.T) {
	// Hard rule 3: a path that matches two items is refused with the
	// candidates. A cached (folder, name) entry from before the second
	// file existed would go on answering with the first one's id, which
	// is the failure the rule exists to prevent.
	svc, _ := setup(t, service.Options{})
	if _, err := svc.GetFile(t.Context(), service.GetFileInput{File: "/Projects/2026/Budget.xlsx"}); err != nil {
		t.Fatalf("GetFile: %v", err)
	}

	if _, err := svc.CreateFile(t.Context(), service.CreateFileInput{
		Name: "Budget.xlsx", Kind: "sheet", Parent: "/Projects/2026", AllowDuplicate: true,
	}); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	_, err := svc.GetFile(t.Context(), service.GetFileInput{File: "/Projects/2026/Budget.xlsx"})
	if err == nil {
		t.Fatal("the path still resolved to one file after a second of that name was created")
	}
	if !strings.Contains(err.Error(), "[ambiguous]") {
		t.Errorf("err = %v, want the ambiguous refusal with both candidates", err)
	}
}

func TestUpdateContentPinsOnlyAfterTheContentIsReplaced(t *testing.T) {
	// Pinning first left a revision kept forever behind a write that
	// never happened, and nothing unpins it.
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-serverlog-fixture", "server.log", "text/plain", "id-2026-fixture")
	fake.SetContent("id-serverlog-fixture", "old")
	fake.Fail = drivetest.FailTimes(9, "/upload/", drivetest.Failure{
		Status: 403, Reason: "insufficientFilePermissions", Message: "no",
	})

	if _, err := svc.UpdateContent(t.Context(), service.UpdateContentInput{
		File: "id-serverlog-fixture", Content: "new", KeepPreviousRevision: true,
	}); err == nil {
		t.Fatal("the upload was refused but update_content reported success")
	}
	if fake.Revisions["id-serverlog-fixture"][0].KeepForever {
		t.Error("a revision was pinned forever by a write that failed")
	}
	if fake.Content["id-serverlog-fixture"] != "old" {
		t.Error("the content changed anyway")
	}
}

func TestGeneratedIDsGoOnlyWhereDriveTakesThem(t *testing.T) {
	// Two refusals, live, with different messages: a Docs Editors format
	// says "Generated IDs are not supported for Docs Editors formats" and
	// a shortcut says "The provided file ID is not usable". Two
	// acceptances: a folder and anything with bytes. Nothing states the
	// rule, so this is the table of what was observed.
	svc, fake := setup(t, service.Options{})

	create := func(t *testing.T, run func() error) int {
		t.Helper()
		fake.Requested()
		if err := run(); err != nil {
			t.Fatalf("create: %v", err)
		}
		return fake.Count("/files/generateIds")
	}

	t.Run("a folder takes one", func(t *testing.T) {
		if n := create(t, func() error {
			_, err := svc.CreateFolder(t.Context(), service.CreateFolderInput{Name: "Reports"})
			return err
		}); n == 0 {
			t.Error("a folder create asked for no id")
		}
	})
	t.Run("a blob takes one", func(t *testing.T) {
		if n := create(t, func() error {
			_, err := svc.CreateFile(t.Context(), service.CreateFileInput{Name: "rows.csv", Content: "a,b\n"})
			return err
		}); n == 0 {
			t.Error("a blob create asked for no id")
		}
	})
	t.Run("a shortcut does not", func(t *testing.T) {
		if n := create(t, func() error {
			_, err := svc.CreateShortcut(t.Context(), service.CreateShortcutInput{
				Target: "id-budget-fixture", Name: "Budget link",
			})
			return err
		}); n != 0 {
			t.Error("a shortcut create asked Drive for an id it will refuse")
		}
	})
	t.Run("a Docs Editors format does not", func(t *testing.T) {
		if n := create(t, func() error {
			_, err := svc.CreateFile(t.Context(), service.CreateFileInput{Name: "Plan", Kind: "doc"})
			return err
		}); n != 0 {
			t.Error("a Docs Editors create asked Drive for an id it will refuse")
		}
	})
	t.Run("a copy of a Google document does not", func(t *testing.T) {
		if n := create(t, func() error {
			_, err := svc.CopyFile(t.Context(), service.CopyFileInput{File: "id-notes-fixture"})
			return err
		}); n != 0 {
			t.Error("copying a Google Doc asked for an id the copy cannot carry")
		}
	})
}

func TestAnUnsupportedConversionSaysWhatTheFileCanBecome(t *testing.T) {
	// Drive answers "The requested conversion is not supported", which
	// leaves a model to guess which half of the pair was wrong. A csv
	// becomes a Sheet and not a Doc, and the account's own importFormats
	// says so.
	svc, _ := setup(t, service.Options{})

	_, err := svc.CreateFile(t.Context(), service.CreateFileInput{
		Name: "rows.csv", Content: "a,b\n", MimeType: "text/csv", ConvertTo: "doc",
	})
	if err == nil {
		t.Fatal("a csv was accepted for conversion to a Google Doc")
	}
	for _, want := range []string{"[invalid]", "CSV file", "Google Doc", "sheet"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}

	// The conversion Drive does offer goes through.
	if _, err := svc.CreateFile(t.Context(), service.CreateFileInput{
		Name: "rows as a sheet", Content: "a,b\n", MimeType: "text/csv", ConvertTo: "sheet",
	}); err != nil {
		t.Errorf("csv to a Sheet was refused: %v", err)
	}
}

func TestAConversionCheckThatCannotRunDoesNotBlockTheCall(t *testing.T) {
	// A check that cannot read its table must not refuse work that would
	// have succeeded: Drive is still the authority.
	svc, fake := setup(t, service.Options{})
	fake.About.ImportFormats = nil

	if _, err := svc.CreateFile(t.Context(), service.CreateFileInput{
		Name: "notes.md", Content: "# Notes\n", MimeType: "text/markdown", ConvertTo: "doc",
	}); err != nil {
		t.Errorf("the call was refused with no table to check against: %v", err)
	}
}

func TestCopyRefusesAConversionDriveWillNotDo(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-rows-fixture", "rows.csv", "text/csv", "id-2026-fixture")
	fake.SetContent("id-rows-fixture", "a,b\n")

	if _, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-rows-fixture", ConvertTo: "doc",
	}); err == nil {
		t.Error("copying a csv as a Google Doc was accepted")
	}
	if _, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-rows-fixture", ConvertTo: "sheet", Name: "rows as a sheet",
	}); err != nil {
		t.Errorf("copying a csv as a Sheet was refused: %v", err)
	}
}
