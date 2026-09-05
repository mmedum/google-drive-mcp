package service_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// A person's Drive is personal data: the names of their files, who they
// share with, and what they search for are all disclosing on their own.
// The logs therefore carry the shape of a call — method, path, outcome,
// duration — and never its subject. That is what lets the issue template
// ask a reporter for a debug log without also asking them to audit it.
//
// This test is what makes the promise true rather than intended. It runs
// the whole surface at debug level against fixtures whose every value is
// unmistakable, and fails if any of them reaches the log.

// Values chosen so that an incidental match is impossible: none of them
// occurs in any log message this server can emit.
const (
	secretFileID   = "1ZzyzxSyntheticFixtureFileIdAAAAAAA"
	secretFolderID = "1ZzyzxSyntheticFixtureFolderIdAAAAA"
	secretDriveID  = "0AZzyzxFixtureDriveIdAAAAA"
	secretFileName = "Kwyjibo quarterly forecast.xlsx"
	secretFolder   = "Grimsby restructuring"
	secretDrive    = "Sprawlmart acquisition"
	secretEmail    = "hallucinated.person@example.com"
	secretPerson   = "Bartholomew Quimby"
	secretQuery    = "Kwyjibo"
	secretContent  = "the numbers nobody outside this room has seen"
	secretLocalOne = "Kwyjibo-restructuring-notes.txt"
)

func TestLogsCarryNoTraceOfWhatWasTouched(t *testing.T) {
	fake := drivetest.New()
	t.Cleanup(fake.Close)
	fake.SetNow(func() time.Time { return time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC) })

	fake.AddFolder(secretFolderID, secretFolder, fake.RootID)
	fake.AddFile(secretFileID, secretFileName,
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", secretFolderID,
		drivetest.Owner(secretPerson, secretEmail))
	fake.SetContent(secretFileID, secretContent)
	fake.AddDrive(secretDriveID, secretDrive)
	fake.Grant(secretFileID, &gdrive.Permission{
		Type: "user", Role: "writer", EmailAddress: secretEmail, DisplayName: secretPerson,
	})

	// Debug is the loudest this server goes, so it is what has to be safe.
	// Both loggers are captured: the client logs the request, the service
	// logs the decision, and either could leak.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, secretLocalOne), []byte(secretContent), 0o600); err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	api := drivetest.Client(t, fake, func(o *gapi.Options) { o.Logger = logger })
	svc := service.New(api, service.Options{
		Logger:   logger,
		LocalDir: dir,
		Now:      func() time.Time { return testNow },
	})

	ctx := context.Background()
	// Every path, including the ones that fail: an error path logs more,
	// not less.
	_, _ = svc.GetAccount(ctx)
	_, _ = svc.GetFile(ctx, service.GetFileInput{File: secretFileID})
	_, _ = svc.GetFile(ctx, service.GetFileInput{File: "/" + secretFolder + "/" + secretFileName})
	_, _ = svc.GetFile(ctx, service.GetFileInput{File: "/" + secretFolder + "/does not exist"})
	_, _ = svc.GetFile(ctx, service.GetFileInput{File: "drive:" + secretDrive})
	_, _ = svc.ListFolder(ctx, service.ListFolderInput{Folder: secretFolderID})
	_, _ = svc.ListFolder(ctx, service.ListFolderInput{Folder: "root", Recursive: true})
	_, _ = svc.Search(ctx, service.SearchInput{Name: secretQuery})
	_, _ = svc.Search(ctx, service.SearchInput{Text: secretQuery, Drive: secretDrive})
	_, _ = svc.Search(ctx, service.SearchInput{Owner: secretEmail, Name: secretQuery})
	_, _ = svc.Resolve(ctx, secretFileID, service.ResolveOptions{FollowShortcut: true})

	// The writes: content in and out, and everything that organises.
	_, _ = svc.ReadFile(ctx, service.ReadFileInput{File: secretFileID})
	_, _ = svc.DownloadFile(ctx, service.DownloadFileInput{File: secretFileID})
	_, _ = svc.CreateFolder(ctx, service.CreateFolderInput{Name: secretFolder + " (archive)"})
	_, _ = svc.CreateFile(ctx, service.CreateFileInput{Name: secretFileName, Content: secretContent})
	_, _ = svc.UploadFile(ctx, service.UploadFileInput{LocalPath: secretLocalOne, Parent: secretFolderID})
	_, _ = svc.UpdateContent(ctx, service.UpdateContentInput{File: secretFileID, Content: secretContent})
	_, _ = svc.UpdateFile(ctx, service.UpdateFileInput{File: secretFileID, Name: secretFileName + " v2",
		Properties: map[string]string{secretPerson: secretEmail}})
	_, _ = svc.CopyFile(ctx, service.CopyFileInput{File: secretFileID, Name: secretFileName + " copy"})
	_, _ = svc.CreateShortcut(ctx, service.CreateShortcutInput{Target: secretFileID, Name: secretFileName})
	_, _ = svc.MoveFile(ctx, service.MoveFileInput{File: secretFileID, To: "root"})
	_, _ = svc.TrashFile(ctx, service.TrashInput{File: secretFileID})
	_, _ = svc.RestoreFile(ctx, service.TrashInput{File: secretFileID})

	got := log.String()
	if strings.TrimSpace(got) == "" {
		t.Fatal("nothing was logged at debug level; this test would pass vacuously")
	}
	// The writes have to have reached the network, or this test proves
	// only that calls which never happened logged nothing.
	for _, method := range []string{"method=GET", "method=POST", "method=PATCH"} {
		if !strings.Contains(got, method) {
			t.Fatalf("no %s reached the log, so the write surface was not exercised:\n%s", method, got)
		}
	}

	forbidden := map[string]string{
		secretFileID:   "a full file id",
		secretFolderID: "a full folder id",
		secretDriveID:  "a full shared-drive id",
		secretFileName: "a file name",
		secretFolder:   "a folder name",
		secretDrive:    "a shared drive's name",
		secretEmail:    "an email address",
		secretPerson:   "a person's name",
		secretQuery:    "a search term",
		secretContent:  "file content",
		secretLocalOne: "a local file name",
		dir:            "a local path",
	}
	for value, what := range forbidden {
		if strings.Contains(got, value) {
			t.Errorf("the log carries %s (%q):\n%s", what, value, got)
		}
	}
	// "Kwyjibo" also appears inside the file name and the content, so a
	// bare substring check would not have caught a leak of just the stem.
	if strings.Contains(strings.ToLower(got), "kwyjibo") {
		t.Errorf("the log carries a search term:\n%s", got)
	}
}

func TestLogsStillSayEnoughToDebugWith(t *testing.T) {
	// The rule is "no payloads", not "no logs". A log that says nothing
	// gets turned up to a level that does, and then it says everything.
	fake := drivetest.New()
	t.Cleanup(fake.Close)
	drivetest.SmallTree(fake)

	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	api := drivetest.Client(t, fake, func(o *gapi.Options) { o.Logger = logger })
	svc := service.New(api, service.Options{
		Logger: logger, Now: func() time.Time { return testNow },
	})
	if _, err := svc.GetFile(context.Background(), service.GetFileInput{File: "id-budget-fixture"}); err != nil {
		t.Fatalf("GetFile: %v", err)
	}

	got := log.String()
	for _, want := range []string{"method=GET", "/files/", "status=200", "ms=", "attempt="} {
		if !strings.Contains(got, want) {
			t.Errorf("the log does not carry %q, so a failure could not be traced:\n%s", want, got)
		}
	}
	// An id appears only as a short prefix: enough to correlate the lines
	// of one call with each other, not enough to name the file.
	if !strings.Contains(got, "id-bud…") {
		t.Errorf("ids should appear truncated, for correlation:\n%s", got)
	}
	// The query string carries the search terms and the field list, so it
	// is dropped whole rather than filtered.
	if strings.Contains(got, "fields=") || strings.Contains(got, "supportsAllDrives") {
		t.Errorf("the log carries a query string:\n%s", got)
	}
}
