package service_test

import (
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// A Google Vid has no bytes on the file endpoint and Drive refuses to
// export one, so the long-running download is the whole of it. It is
// also a Workspace document by media type, which is the trap: the export
// branch would have caught it first and failed with a message about
// formats.
func TestAVidComesThroughTheLongRunningDownload(t *testing.T) {
	dir := t.TempDir()
	svc, fake := setup(t, service.Options{LocalDir: dir})
	fake.AddFile("id-launch-vid-fixture", "Launch video", gdrive.MimeVid, "id-2026-fixture")
	fake.Content["id-launch-vid-fixture"] = "pretend mp4 bytes"

	out, err := svc.DownloadFile(t.Context(), service.DownloadFileInput{File: "id-launch-vid-fixture"})
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if !strings.Contains(out, "mp4") {
		t.Errorf("the result does not say what the video came out as:\n%s", out)
	}
	// It must have gone through the operation, not through an export.
	var started, polled bool
	for _, r := range fake.Requests {
		if strings.HasSuffix(r.Path, "/download") && r.Method == "POST" {
			started = true
		}
		if strings.Contains(r.Path, "/export") {
			polled = true
		}
	}
	if !started {
		t.Error("files.download was never called, so the Vid did not go through the operation")
	}
	if polled {
		t.Error("an export was attempted on a Vid, which Drive answers fileNotExportable")
	}
}

// A new operation is usually pending, "especially for Vids files", with
// done absent rather than false. Polling has to keep going until it is
// finished.
func TestAPendingDownloadIsPolledUntilItFinishes(t *testing.T) {
	dir := t.TempDir()
	svc, fake := setup(t, service.Options{LocalDir: dir})
	fake.AddFile("id-launch-vid-fixture", "Launch video", gdrive.MimeVid, "id-2026-fixture")
	fake.Content["id-launch-vid-fixture"] = "pretend mp4 bytes"
	fake.PendingDownloads = 2

	if _, err := svc.DownloadFile(t.Context(), service.DownloadFileInput{File: "id-launch-vid-fixture"}); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	polls := 0
	for _, r := range fake.Requests {
		if strings.HasPrefix(r.Path, "/drive/v3/operations/") {
			polls++
		}
	}
	if polls < 2 {
		t.Errorf("the operation was polled %d times; it answered pending twice", polls)
	}
}

func TestAVidTakesNoFormat(t *testing.T) {
	dir := t.TempDir()
	svc, fake := setup(t, service.Options{LocalDir: dir})
	fake.AddFile("id-launch-vid-fixture", "Launch video", gdrive.MimeVid, "id-2026-fixture")

	_, err := svc.DownloadFile(t.Context(), service.DownloadFileInput{
		File: "id-launch-vid-fixture", Format: "pdf",
	})
	if err == nil {
		t.Fatal("a format was accepted for a Vid, which Drive renders as MP4 and nothing else")
	}
	if !strings.Contains(err.Error(), "MP4") {
		t.Errorf("the refusal does not say what a Vid does come out as: %v", err)
	}
}
