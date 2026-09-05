package service_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// withLocalDir builds a service whose transfers land in a temporary
// directory, and returns the directory.
func withLocalDir(t *testing.T, o service.Options) (*service.Service, *drivetest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	o.LocalDir = dir
	svc, fake := setup(t, o)
	return svc, fake, dir
}

func TestReadFileReturnsAWholeTextFile(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-serverlog-fixture", "server.log", "text/plain", "id-2026-fixture")
	fake.SetContent("id-serverlog-fixture", "first line\nsecond line\n")

	out, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-serverlog-fixture"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(out, "first line\nsecond line") {
		t.Errorf("content missing from:\n%s", out)
	}
	if !strings.Contains(out, "the whole file") {
		t.Errorf("a complete read did not say so:\n%s", out)
	}
	if strings.Contains(out, "offset:") {
		t.Errorf("a complete read offered a continuation:\n%s", out)
	}
}

func TestReadFileWindowsALargeFileAndSaysHowToContinue(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-serverlog-fixture", "server.log", "text/plain", "id-2026-fixture")
	fake.SetContent("id-serverlog-fixture", strings.Repeat("abcde", 100))

	out, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-serverlog-fixture", MaxChars: 20})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(out, "offset: 20") {
		t.Errorf("no continuation offered:\n%s", out)
	}
	body := out[strings.Index(out, "---\n")+4:]
	if got := strings.TrimSuffix(body, "\n"); got != "abcdeabcdeabcdeabcde" {
		t.Errorf("window = %q", got)
	}

	next, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-serverlog-fixture", Offset: 20, MaxChars: 20})
	if err != nil {
		t.Fatalf("ReadFile at an offset: %v", err)
	}
	if !strings.Contains(next, "bytes 20 to 39") {
		t.Errorf("the second window did not name its range:\n%s", next)
	}
}

func TestReadFileNeverSplitsACharacter(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-notes-fixture", "notes.txt", "text/plain", "id-2026-fixture")
	// Three-byte characters against a budget that lands mid-character.
	fake.SetContent("id-notes-fixture", strings.Repeat("→", 10))

	out, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-notes-fixture", MaxChars: 8})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	body := strings.TrimSuffix(out[strings.Index(out, "---\n")+4:], "\n")
	if body != "→→" {
		t.Errorf("window = %q, want two whole characters", body)
	}
	if !strings.Contains(out, "offset: 6") {
		t.Errorf("continuation should resume at the character boundary:\n%s", out)
	}
}

func TestReadFileExportsAGoogleDocAsMarkdown(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.SetContent("id-notes-fixture", "# Meeting\n\nWe agreed.\n")

	out, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-notes-fixture"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(out, "We agreed.") {
		t.Errorf("the exported text is missing:\n%s", out)
	}
	if !strings.Contains(out, "markdown") {
		t.Errorf("the header does not say what form the text is in:\n%s", out)
	}
	if !strings.Contains(out, "Docs API") {
		t.Errorf("a Doc's read does not say where its content is edited:\n%s", out)
	}
}

func TestReadFileStripsInlinedImages(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.SetContent("id-notes-fixture", "Before ![a chart](data:image/png;base64,AAAABBBBCCCC) after")

	out, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-notes-fixture"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(out, "base64") {
		t.Errorf("an inlined image survived into the text:\n%s", out)
	}
	if !strings.Contains(out, "[image removed]") {
		t.Errorf("the image was dropped without saying so:\n%s", out)
	}
}

func TestReadFileSaysASheetIsOnlyItsFirstSheet(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-sheet-fixture", "Numbers", gdrive.MimeSheet, "id-2026-fixture")
	fake.SetContent("id-sheet-fixture", "name,amount\nfirst,1\n")

	out, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-sheet-fixture"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(out, "FIRST SHEET") {
		t.Errorf("a spreadsheet read did not say which sheet it was:\n%s", out)
	}

	tsv, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-sheet-fixture", Format: "tsv"})
	if err != nil {
		t.Fatalf("ReadFile as tsv: %v", err)
	}
	if !strings.Contains(tsv, "tsv") {
		t.Errorf("the tsv form was not announced:\n%s", tsv)
	}
	if _, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-sheet-fixture", Format: "xlsx"}); err == nil {
		t.Error("a spreadsheet read as xlsx succeeded; that is a download")
	}
}

func TestReadFileRefusesWhatIsNotTextAndSaysWhatToDo(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-scan-fixture", "scan.pdf", "application/pdf", "id-2026-fixture")

	_, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-scan-fixture"})
	if err == nil {
		t.Fatal("reading a PDF as text succeeded")
	}
	msg := err.Error()
	for _, want := range []string{"[unsupported]", "download_file", "convert_to: doc"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q: %s", want, msg)
		}
	}
}

func TestReadFileRefusesAFolder(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-2026-fixture"})
	if err == nil || !strings.Contains(err.Error(), "list_folder") {
		t.Fatalf("err = %v, want a pointer to list_folder", err)
	}
}

func TestDownloadFileNeedsTheLocalDirectory(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.DownloadFile(t.Context(), service.DownloadFileInput{File: "id-budget-fixture"})
	if err == nil || !strings.Contains(err.Error(), "GDRIVE_LOCAL_DIR") {
		t.Fatalf("err = %v, want a refusal naming GDRIVE_LOCAL_DIR", err)
	}
}

func TestDownloadFileWritesAndVerifies(t *testing.T) {
	svc, fake, dir := withLocalDir(t, service.Options{})
	fake.SetContent("id-budget-fixture", "name,amount\nfirst,1\n")

	out, err := svc.DownloadFile(t.Context(), service.DownloadFileInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if !strings.Contains(out, "verified against Drive's md5") {
		t.Errorf("the checksum was not confirmed:\n%s", out)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("directory holds %v (%v)", entries, err)
	}
	name := entries[0].Name()
	if !strings.HasPrefix(name, "Budget-") || !strings.HasSuffix(name, ".xlsx") {
		t.Errorf("wrote %q, want the name, the short id and the extension", name)
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil || string(data) != "name,amount\nfirst,1\n" {
		t.Errorf("file holds %q (%v)", data, err)
	}
}

func TestDownloadFileNeverOverwrites(t *testing.T) {
	svc, fake, dir := withLocalDir(t, service.Options{})
	fake.SetContent("id-budget-fixture", "one")
	if _, err := svc.DownloadFile(t.Context(), service.DownloadFileInput{File: "id-budget-fixture"}); err != nil {
		t.Fatalf("first download: %v", err)
	}
	if _, err := svc.DownloadFile(t.Context(), service.DownloadFileInput{File: "id-budget-fixture"}); err != nil {
		t.Fatalf("second download: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("a second download produced %d files, want 2", len(entries))
	}
}

func TestDownloadFileExportsAGoogleDoc(t *testing.T) {
	svc, fake, dir := withLocalDir(t, service.Options{})
	fake.SetContent("id-notes-fixture", "the text")

	out, err := svc.DownloadFile(t.Context(), service.DownloadFileInput{File: "id-notes-fixture"})
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if !strings.Contains(out, "no checksum") {
		t.Errorf("an export claimed a checksum it cannot have:\n%s", out)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".docx") {
		t.Fatalf("wrote %v, want a docx", entries)
	}

	if _, err := svc.DownloadFile(t.Context(), service.DownloadFileInput{
		File: "id-notes-fixture", Format: "txt",
	}); err != nil {
		t.Fatalf("DownloadFile as txt: %v", err)
	}
	if _, err := svc.DownloadFile(t.Context(), service.DownloadFileInput{
		File: "id-notes-fixture", Format: "xlsx",
	}); err == nil {
		t.Error("exporting a document as a spreadsheet succeeded")
	}
}

func TestDownloadFileRefusesWhatIsTooLarge(t *testing.T) {
	svc, fake, _ := withLocalDir(t, service.Options{MaxDownload: 10})
	fake.AddFile("id-bigfile-fixture", "big.bin", "application/octet-stream", "id-2026-fixture",
		drivetest.Size(1<<20, ""))

	_, err := svc.DownloadFile(t.Context(), service.DownloadFileInput{File: "id-bigfile-fixture"})
	if err == nil || !strings.Contains(err.Error(), "GDRIVE_MAX_DOWNLOAD") {
		t.Fatalf("err = %v, want a refusal naming the limit", err)
	}
}

func TestDownloadFileRefusesAFolder(t *testing.T) {
	svc, _, _ := withLocalDir(t, service.Options{})
	_, err := svc.DownloadFile(t.Context(), service.DownloadFileInput{File: "id-2026-fixture"})
	if err == nil || !strings.Contains(err.Error(), "list_folder") {
		t.Fatalf("err = %v, want a folder refusal", err)
	}
}

func TestDownloadFileFetchesAnOldRevision(t *testing.T) {
	svc, fake, dir := withLocalDir(t, service.Options{})
	fake.AddFile("id-serverlog-fixture", "server.log", "text/plain", "id-2026-fixture")
	fake.SetContent("id-serverlog-fixture", "old content")
	fake.SetContent("id-serverlog-fixture", "new content")
	first := fake.Revisions["id-serverlog-fixture"][0].ID

	out, err := svc.DownloadFile(t.Context(), service.DownloadFileInput{File: "id-serverlog-fixture", Revision: first})
	if err != nil {
		t.Fatalf("DownloadFile of a revision: %v", err)
	}
	if !strings.Contains(out, "not compared") {
		t.Errorf("a revision download compared against the current checksum:\n%s", out)
	}
	entries, _ := os.ReadDir(dir)
	data, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if string(data) != "old content" {
		t.Errorf("downloaded %q, want the old revision", data)
	}
}

func TestUploadsRefuseAPathOutsideTheLocalDirectory(t *testing.T) {
	svc, _, dir := withLocalDir(t, service.Options{})
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{outside, "../secret.txt", filepath.Join(dir, "..", "secret.txt")} {
		_, err := svc.UploadFile(t.Context(), service.UploadFileInput{LocalPath: path})
		if err == nil {
			t.Fatalf("uploading %q succeeded; it is outside the local directory", path)
		}
	}

	// A symlink inside the directory pointing out of it is the same
	// escape wearing a different name.
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := svc.UploadFile(t.Context(), service.UploadFileInput{LocalPath: "link.txt"}); err == nil {
		t.Error("a symlink out of the local directory was followed")
	}
}

func TestReadFilePagesAnExportWithoutReExportingIt(t *testing.T) {
	// Drive takes no byte range on an export, so a naive continuation
	// re-exports the whole document for every window. Paging a 1 MB Doc
	// at the default window would be 53 exports to deliver 1 MB once.
	svc, fake := setup(t, service.Options{})
	fake.SetContent("id-notes-fixture", strings.Repeat("0123456789", 50))

	first, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-notes-fixture", MaxChars: 100})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(first, "offset: 100") {
		t.Fatalf("no continuation offered:\n%s", first)
	}
	fake.Requested()

	for offset := 100; offset < 500; offset += 100 {
		out, err := svc.ReadFile(t.Context(), service.ReadFileInput{
			File: "id-notes-fixture", Offset: int64(offset), MaxChars: 100,
		})
		if err != nil {
			t.Fatalf("ReadFile at %d: %v", offset, err)
		}
		if !strings.Contains(out, "0123456789") {
			t.Errorf("window at %d is empty:\n%s", offset, out)
		}
	}
	if n := fake.Count("/export"); n != 0 {
		t.Errorf("paging cost %d further exports; the first one should have covered them", n)
	}
}

func TestReadFileStopsAtTheEndOfAnExport(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.SetContent("id-notes-fixture", "short")

	out, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-notes-fixture", Offset: 500})
	if err != nil {
		t.Fatalf("ReadFile past the end: %v", err)
	}
	if !strings.Contains(out, "the file ends before it") {
		t.Errorf("a read past the end did not say so:\n%s", out)
	}
}

func TestReadFileOfADocSaysWhereItsContentIsEditedOnce(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-sheetnote-fixture", "Numbers", gdrive.MimeSheet, "id-2026-fixture")
	fake.SetContent("id-sheetnote-fixture", "a,b\n")

	out, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-sheetnote-fixture"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if n := strings.Count(out, "Sheets API"); n != 1 {
		t.Errorf("the Sheets API is mentioned %d times, want once:\n%s", n, out)
	}
}

func TestReadFileWindowsALargeBlobRatherThanRefusingIt(t *testing.T) {
	// The whole point of the byte range is that a file's size does not
	// decide whether it can be read. A guard here once refused the file
	// and told the caller to retry with max_chars — which changed
	// nothing, so the advice was a loop.
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-hugelog-fixture", "server.log", "text/plain", "id-2026-fixture",
		drivetest.Size(200<<20, ""))
	fake.SetContent("id-hugelog-fixture", strings.Repeat("line\n", 1000))

	out, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-hugelog-fixture", MaxChars: 50})
	if err != nil {
		t.Fatalf("ReadFile on a large log: %v", err)
	}
	if !strings.Contains(out, "line") {
		t.Errorf("no content returned:\n%s", out)
	}
	if !strings.Contains(out, "offset: 50") {
		t.Errorf("no continuation offered:\n%s", out)
	}
}

func TestReadFilePastTheEndOfABlobSaysSo(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-shortlog-fixture", "server.log", "text/plain", "id-2026-fixture")
	fake.SetContent("id-shortlog-fixture", "five!")

	out, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: "id-shortlog-fixture", Offset: 500})
	if err != nil {
		t.Fatalf("ReadFile past the end: %v", err)
	}
	if !strings.Contains(out, "the file ends before it") {
		t.Errorf("a read past the end of a blob did not say so:\n%s", out)
	}
}
