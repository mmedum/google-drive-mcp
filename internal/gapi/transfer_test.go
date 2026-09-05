package gapi_test

import (
	"bytes"
	"crypto/md5" //nolint:gosec // Drive's own checksum
	"encoding/hex"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

func TestDownloadReturnsBytes(t *testing.T) {
	t.Parallel()
	s := drivetest.New()
	defer s.Close()
	s.AddFile("id-log-fixture", "server.log", "text/plain", s.RootID)
	s.SetContent("id-log-fixture", "line one\nline two\nline three\n")
	c := drivetest.Client(t, s)

	got, err := c.Download(t.Context(), "id-log-fixture", gapi.DownloadOptions{})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer func() { _ = got.Body.Close() }()
	data, err := io.ReadAll(got.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "line one\nline two\nline three\n" {
		t.Errorf("content = %q", data)
	}
	if got.MimeType != "text/plain" {
		t.Errorf("mime type = %q, want text/plain", got.MimeType)
	}
	if got.Partial {
		t.Error("a whole-file download reported itself as partial")
	}
}

func TestDownloadHonoursARange(t *testing.T) {
	t.Parallel()
	s := drivetest.New()
	defer s.Close()
	s.AddFile("id-log-fixture", "server.log", "text/plain", s.RootID)
	s.SetContent("id-log-fixture", "0123456789abcdef")
	c := drivetest.Client(t, s)

	got, err := c.Download(t.Context(), "id-log-fixture", gapi.DownloadOptions{Offset: 4, Length: 6})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer func() { _ = got.Body.Close() }()
	data, _ := io.ReadAll(got.Body)
	if string(data) != "456789" {
		t.Errorf("window = %q, want 456789", data)
	}
	if !got.Partial {
		t.Error("a ranged download did not report itself as partial")
	}
	if got.TotalLength != 16 {
		t.Errorf("total = %d, want 16", got.TotalLength)
	}
}

func TestDownloadRefusesAWorkspaceDocument(t *testing.T) {
	t.Parallel()
	s := drivetest.New()
	defer s.Close()
	s.AddFile("id-notes-fixture", "Notes", gdrive.MimeDocument, s.RootID)
	c := drivetest.Client(t, s)

	if _, err := c.Download(t.Context(), "id-notes-fixture", gapi.DownloadOptions{}); err == nil {
		t.Fatal("downloading a Google Doc's bytes succeeded; it has none")
	}
}

func TestExportConvertsAWorkspaceDocument(t *testing.T) {
	t.Parallel()
	s := drivetest.New()
	defer s.Close()
	s.AddFile("id-notes-fixture", "Notes", gdrive.MimeDocument, s.RootID)
	s.SetContent("id-notes-fixture", "# Heading\n\nA paragraph.\n")
	c := drivetest.Client(t, s)

	got, err := c.Export(t.Context(), "id-notes-fixture", "text/markdown")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	defer func() { _ = got.Body.Close() }()
	data, _ := io.ReadAll(got.Body)
	if !strings.Contains(string(data), "A paragraph.") {
		t.Errorf("export = %q", data)
	}

	if _, err := c.Export(t.Context(), "id-notes-fixture", "image/gif"); err == nil {
		t.Error("exporting to a format Drive does not offer succeeded")
	}
}

func TestUploadMultipartCreatesAFileWithItsContent(t *testing.T) {
	t.Parallel()
	s := drivetest.New()
	defer s.Close()
	c := drivetest.Client(t, s)

	body := []byte("name,amount\nfirst,1\n")
	f, err := c.UploadMultipart(t.Context(), gapi.UploadRequest{
		Meta:        &gdrive.FileMeta{ID: "id-new-fixture", Name: "rows.csv", Parents: []string{s.RootID}},
		ContentType: "text/csv",
	}, body)
	if err != nil {
		t.Fatalf("UploadMultipart: %v", err)
	}
	if f.Name != "rows.csv" || f.MimeType != "text/csv" {
		t.Errorf("created %q of type %q", f.Name, f.MimeType)
	}
	if f.MD5Checksum != md5Hex(body) {
		t.Errorf("md5 = %q, want %q", f.MD5Checksum, md5Hex(body))
	}
	if s.Content["id-new-fixture"] != string(body) {
		t.Errorf("stored content = %q", s.Content["id-new-fixture"])
	}
}

func TestUploadMultipartRefusesMoreThanFiveMegabytes(t *testing.T) {
	t.Parallel()
	s := drivetest.New()
	defer s.Close()
	c := drivetest.Client(t, s)

	_, err := c.UploadMultipart(t.Context(), gapi.UploadRequest{
		Meta: &gdrive.FileMeta{Name: "big.bin", Parents: []string{s.RootID}},
	}, make([]byte, gapi.MaxMultipartUpload+1))
	if !errors.Is(err, gapi.ErrInvalid) {
		t.Fatalf("err = %v, want an invalid-request error", err)
	}
	if s.Count("/upload/") != 0 {
		t.Error("an oversized multipart upload still went to the network")
	}
}

func TestUploadResumableSendsEveryChunk(t *testing.T) {
	t.Parallel()
	s := drivetest.New()
	defer s.Close()
	c := drivetest.Client(t, s)

	body := randomBytes(3*gapi.ChunkAlignment + 17)
	var progress []int64
	f, err := c.UploadResumable(t.Context(), gapi.UploadRequest{
		Meta:        &gdrive.FileMeta{ID: "id-big-fixture", Name: "archive.bin", Parents: []string{s.RootID}},
		ContentType: "application/octet-stream",
	}, bytes.NewReader(body), int64(len(body)), gapi.ResumableOptions{
		ChunkSize: gapi.ChunkAlignment,
		Progress:  func(sent, _ int64) { progress = append(progress, sent) },
	})
	if err != nil {
		t.Fatalf("UploadResumable: %v", err)
	}
	if f.MD5Checksum != md5Hex(body) {
		t.Errorf("md5 = %q, want %q", f.MD5Checksum, md5Hex(body))
	}
	if s.Content["id-big-fixture"] != string(body) {
		t.Error("the stored bytes are not the bytes that were sent")
	}
	if len(progress) < 4 {
		t.Errorf("progress reported %d times for a four-chunk upload: %v", len(progress), progress)
	}
}

func TestUploadResumableContinuesAfterAnInterruption(t *testing.T) {
	t.Parallel()
	s := drivetest.New()
	defer s.Close()
	// The connection dies once, in the middle of the transfer, which is
	// the failure the whole protocol exists for.
	s.Fail = drivetest.FailTimes(1, "/sessions/", drivetest.Failure{Hijack: true})
	c := drivetest.Client(t, s)

	body := randomBytes(2*gapi.ChunkAlignment + 5)
	f, err := c.UploadResumable(t.Context(), gapi.UploadRequest{
		Meta:        &gdrive.FileMeta{ID: "id-big-fixture", Name: "archive.bin", Parents: []string{s.RootID}},
		ContentType: "application/octet-stream",
	}, bytes.NewReader(body), int64(len(body)), gapi.ResumableOptions{ChunkSize: gapi.ChunkAlignment})
	if err != nil {
		t.Fatalf("UploadResumable after an interruption: %v", err)
	}
	if f.MD5Checksum != md5Hex(body) {
		t.Errorf("md5 = %q, want %q", f.MD5Checksum, md5Hex(body))
	}
	if s.Content["id-big-fixture"] != string(body) {
		t.Error("the recovered upload stored different bytes")
	}
}

func TestUploadResumableContinuesFromMidChunk(t *testing.T) {
	t.Parallel()
	s := drivetest.New()
	defer s.Close()
	// Drive may store less than a chunk and say so in the Range header;
	// the client has to resend from where the session actually got to.
	s.ChunkLimit = 100_000
	c := drivetest.Client(t, s)

	body := randomBytes(gapi.ChunkAlignment + 1000)
	f, err := c.UploadResumable(t.Context(), gapi.UploadRequest{
		Meta:        &gdrive.FileMeta{ID: "id-big-fixture", Name: "archive.bin", Parents: []string{s.RootID}},
		ContentType: "application/octet-stream",
	}, bytes.NewReader(body), int64(len(body)), gapi.ResumableOptions{ChunkSize: gapi.ChunkAlignment})
	if err != nil {
		t.Fatalf("UploadResumable: %v", err)
	}
	if f.MD5Checksum != md5Hex(body) {
		t.Errorf("md5 = %q, want %q", f.MD5Checksum, md5Hex(body))
	}
}

func TestUploadResumableReplacesContent(t *testing.T) {
	t.Parallel()
	s := drivetest.New()
	defer s.Close()
	s.AddFile("id-log-fixture", "server.log", "text/plain", s.RootID)
	s.SetContent("id-log-fixture", "old")
	c := drivetest.Client(t, s)

	body := []byte(strings.Repeat("new\n", 100))
	f, err := c.UploadResumable(t.Context(), gapi.UploadRequest{
		FileID: "id-log-fixture", ContentType: "text/plain",
	}, bytes.NewReader(body), int64(len(body)), gapi.ResumableOptions{})
	if err != nil {
		t.Fatalf("UploadResumable: %v", err)
	}
	if f.MD5Checksum != md5Hex(body) {
		t.Errorf("md5 = %q, want %q", f.MD5Checksum, md5Hex(body))
	}
	if len(s.Revisions["id-log-fixture"]) != 2 {
		t.Errorf("revisions = %d, want 2", len(s.Revisions["id-log-fixture"]))
	}
}

func TestCreateUpdateAndCopy(t *testing.T) {
	t.Parallel()
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	c := drivetest.Client(t, s)
	ctx := t.Context()

	folder, err := c.CreateFile(ctx, &gdrive.FileMeta{
		ID: "id-folder-fixture", Name: "Reports", MimeType: gdrive.MimeFolder, Parents: []string{s.RootID},
	}, gapi.WriteOptions{})
	if err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	if !folder.IsFolder() || folder.Name != "Reports" {
		t.Fatalf("created %q of type %q", folder.Name, folder.MimeType)
	}

	renamed, err := c.UpdateFile(ctx, folder.ID, &gdrive.FileMeta{
		Name: "Reports 2026", Description: gdrive.String("quarterly"),
	}, gapi.UpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	if renamed.Name != "Reports 2026" || renamed.Description != "quarterly" {
		t.Errorf("patched to %q / %q", renamed.Name, renamed.Description)
	}

	moved, err := c.UpdateFile(ctx, "id-budget-fixture", &gdrive.FileMeta{}, gapi.UpdateOptions{
		AddParents: folder.ID, RemoveParents: "id-2026-fixture",
	})
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if moved.Parent() != folder.ID {
		t.Errorf("parent = %q, want %q", moved.Parent(), folder.ID)
	}

	copied, err := c.CopyFile(ctx, "id-budget-fixture", &gdrive.FileMeta{
		ID: "id-copy-fixture", Name: "Budget copy",
	}, gapi.WriteOptions{})
	if err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if copied.Name != "Budget copy" || copied.ID != "id-copy-fixture" {
		t.Errorf("copied to %q (%s)", copied.Name, copied.ID)
	}
}

func TestUpdateRefusesAFolderIntoASharedDrive(t *testing.T) {
	t.Parallel()
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	c := drivetest.Client(t, s)

	_, err := c.UpdateFile(t.Context(), "id-projects-fixture", &gdrive.FileMeta{}, gapi.UpdateOptions{
		AddParents: "id-campaigns-fixture", RemoveParents: s.RootID,
	})
	if !errors.Is(err, gapi.ErrUnsupported) {
		t.Fatalf("err = %v, want unsupported", err)
	}
}

func TestRevisionsAreReadAndPinned(t *testing.T) {
	t.Parallel()
	s := drivetest.New()
	defer s.Close()
	s.AddFile("id-log-fixture", "server.log", "text/plain", s.RootID)
	s.SetContent("id-log-fixture", "first")
	s.SetContent("id-log-fixture", "second")
	c := drivetest.Client(t, s)
	first := s.Revisions["id-log-fixture"][0].ID

	rev, err := c.GetRevision(t.Context(), "id-log-fixture", first)
	if err != nil {
		t.Fatalf("GetRevision: %v", err)
	}
	if rev.KeepForever {
		t.Error("a fresh revision was already pinned")
	}
	if _, err := c.UpdateRevision(t.Context(), "id-log-fixture", first, true); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	if !s.Revisions["id-log-fixture"][0].KeepForever {
		t.Error("keepForever did not stick")
	}

	got, err := c.Download(t.Context(), "id-log-fixture", gapi.DownloadOptions{RevisionID: first})
	if err != nil {
		t.Fatalf("Download of a revision: %v", err)
	}
	defer func() { _ = got.Body.Close() }()
	data, _ := io.ReadAll(got.Body)
	if string(data) != "first" {
		t.Errorf("old revision = %q, want first", data)
	}
}

func TestTransfersRetryATransientFailure(t *testing.T) {
	t.Parallel()
	s := drivetest.New()
	defer s.Close()
	s.AddFile("id-log-fixture", "server.log", "text/plain", s.RootID)
	s.SetContent("id-log-fixture", "content")
	s.Fail = drivetest.FailTimes(2, "/files/", drivetest.Failure{
		Status: http.StatusServiceUnavailable, Reason: "backendError", Message: "try again",
	})
	c := drivetest.Client(t, s)

	got, err := c.Download(t.Context(), "id-log-fixture", gapi.DownloadOptions{})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer func() { _ = got.Body.Close() }()
	data, _ := io.ReadAll(got.Body)
	if string(data) != "content" {
		t.Errorf("content = %q", data)
	}
}

func md5Hex(b []byte) string {
	sum := md5.Sum(b) //nolint:gosec // matching Drive's md5Checksum
	return hex.EncodeToString(sum[:])
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	r := rand.NewChaCha8([32]byte{7})
	_, _ = r.Read(b)
	return b
}

func TestUploadResumableGivesUpWhenNothingIsStored(t *testing.T) {
	t.Parallel()
	s := drivetest.New()
	defer s.Close()
	// A session that answers 308 and stores nothing is what a proxy
	// stripping the Range header looks like. Nothing in the protocol
	// forbids it, and a client that assumes progress sends the same
	// chunk until the context gives up.
	s.StallUploads = true
	c := drivetest.Client(t, s)

	body := randomBytes(2 * gapi.ChunkAlignment)
	_, err := c.UploadResumable(t.Context(), gapi.UploadRequest{
		Meta: &gdrive.FileMeta{ID: "id-stalled-fixture", Name: "archive.bin", Parents: []string{s.RootID}},
	}, bytes.NewReader(body), int64(len(body)), gapi.ResumableOptions{ChunkSize: gapi.ChunkAlignment})
	if err == nil {
		t.Fatal("an upload that stored nothing reported success")
	}
	if !strings.Contains(err.Error(), "without storing a byte") {
		t.Errorf("err = %v, want it to say the session made no progress", err)
	}
}

func TestACreateThatCannotCarryAnIDIsNeverRepeated(t *testing.T) {
	t.Parallel()
	// A 500 proves Google answered, not that it did nothing: it can
	// arrive after the file was made. A create is safe to repeat only
	// because of the pre-generated id that collapses the second attempt
	// into the first, and Drive refuses that id for the Docs Editors
	// formats. Repeating one of those makes two documents.
	for _, c := range []struct {
		name    string
		meta    *gdrive.FileMeta
		wantMax int
	}{
		{"without an id", &gdrive.FileMeta{Name: "Plan", MimeType: gdrive.MimeDocument}, 1},
		{"with one", &gdrive.FileMeta{ID: "id-planned-fixture", Name: "rows.csv", MimeType: "text/csv"}, 2},
	} {
		s := drivetest.New()
		s.Fail = drivetest.FailTimes(1, "/files", drivetest.Failure{
			Status: http.StatusInternalServerError, Reason: "internalError", Message: "try again",
		})
		c2 := drivetest.Client(t, s)
		_, _ = c2.CreateFile(t.Context(), c.meta, gapi.WriteOptions{})
		if got := s.Count("POST"); got > c.wantMax {
			t.Errorf("a create %s was attempted %d times, want at most %d", c.name, got, c.wantMax)
		}
		s.Close()
	}
}

func TestRepeatabilityDefaultsToTheSafeAnswer(t *testing.T) {
	t.Parallel()
	// The rule has to be safe when nobody thought about it. A POST added
	// in a later phase and given no flag must not inherit permission to
	// retry, which is what the previous version of this rule did: its
	// flag's zero value meant "safe to repeat".
	cases := []struct {
		method string
		want   bool
	}{
		{http.MethodGet, true},
		{http.MethodPatch, true},
		{http.MethodPut, true},
		{http.MethodDelete, true},
		{http.MethodPost, false},
	}
	for _, c := range cases {
		if got := gapi.RepeatableForTest(c.method, false); got != c.want {
			t.Errorf("a bare %s is repeatable=%v, want %v", c.method, got, c.want)
		}
	}
	if !gapi.RepeatableForTest(http.MethodPost, true) {
		t.Error("a POST that says it is idempotent should be repeatable")
	}
}
