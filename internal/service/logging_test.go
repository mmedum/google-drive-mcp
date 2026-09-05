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
	// A domain is its own kind of secret: sharing with one names an
	// organisation even when it names no person.
	secretDomain   = "quimby-restructuring.example"
	secretPerson   = "Bartholomew Quimby"
	secretQuery    = "Kwyjibo"
	secretContent  = "the numbers nobody outside this room has seen"
	secretLocalOne = "Kwyjibo-restructuring-notes.txt"
	// A comment is somebody's words about the file, which is as
	// disclosing as the file, and a reply carries a second person's.
	secretComment = "Quimby says the Grimsby line is being wound down"
	secretReply   = "do not put that in writing anywhere"
	// An access request names who asked and why.
	secretRequest = "I need this before the Sprawlmart board meets"
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
	fake.AddComment(secretFileID, "id-comment-fixture", secretComment,
		drivetest.ByOther(secretPerson), drivetest.AssignedTo(secretEmail),
		drivetest.WithReply("id-reply-fixture", secretPerson, secretReply, ""))
	proposal := fake.AddProposal(secretFileID, "id-request-fixture", secretEmail, "writer")
	proposal.RequestMessage = secretRequest

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

	// Access, shared drives and history. The sharing calls matter most
	// here: they are the only ones that take an email address as an
	// argument, so they are where one would reach a log.
	_, _ = svc.ListPermissions(ctx, service.ListPermissionsInput{File: secretFileID})
	_, _ = svc.ShareFile(ctx, service.ShareFileInput{
		File: secretFileID, Principal: secretEmail, Role: "writer", Message: secretContent})
	_, _ = svc.ShareFile(ctx, service.ShareFileInput{
		File: secretFileID, Principal: "anyone", Role: "reader", AllowAnyone: true})
	_, _ = svc.ShareFile(ctx, service.ShareFileInput{
		File: secretFileID, Principal: "domain:" + secretDomain, Role: "reader"})
	_, _ = svc.UnshareFile(ctx, service.UnshareFileInput{File: secretFileID, Principal: secretEmail})
	_, _ = svc.UnshareFile(ctx, service.UnshareFileInput{File: secretFileID, RemoveLink: true})
	_, _ = svc.ListDrives(ctx, service.ListDrivesInput{Name: secretDrive})
	_, _ = svc.ManageDrive(ctx, service.ManageDriveInput{Action: "rename", Drive: secretDrive, Name: secretFolder})
	_, _ = svc.ManageDrive(ctx, service.ManageDriveInput{Action: "create", Name: secretDrive + " II"})
	_, _ = svc.ListRevisions(ctx, service.ListRevisionsInput{File: secretFileID})
	_, _ = svc.ManageRevision(ctx, service.ManageRevisionInput{
		File: secretFileID, Revision: fake.Revisions[secretFileID][0].ID, Action: "keep"})
	_, _ = svc.ListChanges(ctx, service.ListChangesInput{})
	_, _ = svc.ListChanges(ctx, service.ListChangesInput{PageToken: "0", Drive: secretDrive})

	// Comments and access requests. A comment carries a second person's
	// words, and an access request carries an address and a reason, so
	// both are new kinds of subject rather than new calls on an old one.
	_, _ = svc.ListComments(ctx, service.ListCommentsInput{File: secretFileID, IncludeDeleted: true})
	_, _ = svc.AddComment(ctx, service.AddCommentInput{File: secretFileID, Content: secretComment})
	_, _ = svc.ReplyComment(ctx, service.ReplyCommentInput{
		File: secretFileID, Comment: "id-comment-fixture", Content: secretReply})
	_, _ = svc.ReplyComment(ctx, service.ReplyCommentInput{
		File: secretFileID, Comment: "id-comment-fixture", Action: "resolve"})
	_, _ = svc.ReplyComment(ctx, service.ReplyCommentInput{
		File: secretFileID, Comment: "id-comment-fixture", Action: "edit", Content: secretComment + " (edited)"})
	_, _ = svc.ListAccessRequests(ctx, service.ListAccessRequestsInput{File: secretFileID})
	_, _ = svc.ResolveAccessRequest(ctx, service.ResolveAccessRequestInput{
		File: secretFileID, Request: "id-request-fixture", Action: "accept", Role: "reader"})

	// The destructive surface logs too, and it is the one that issues
	// DELETE. It runs on a copy, so the fixtures the assertions below
	// need are still there.
	destructive := service.New(api, service.Options{
		Logger: logger, LocalDir: dir, Destructive: true,
		Now: func() time.Time { return testNow },
	})
	doomed, err := destructive.CreateFile(ctx, service.CreateFileInput{
		Name: secretFileName + " (doomed)", Content: secretContent})
	if err == nil {
		_, _ = destructive.DeleteFile(ctx, service.DeleteFileInput{
			File: doomed.JSON.File.ID, Confirm: true})
	}
	_, _ = destructive.EmptyTrash(ctx, service.EmptyTrashInput{Drive: secretDrive, Confirm: true})
	_, _ = destructive.DeleteComment(ctx, service.DeleteCommentInput{
		File: secretFileID, Comment: "id-comment-fixture", Confirm: true})

	got := log.String()
	if strings.TrimSpace(got) == "" {
		t.Fatal("nothing was logged at debug level; this test would pass vacuously")
	}
	// The writes have to have reached the network, or this test proves
	// only that calls which never happened logged nothing.
	for _, method := range []string{"method=GET", "method=POST", "method=PATCH", "method=DELETE"} {
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
		secretDomain:   "a domain shared with",
		secretComment:  "what somebody said in a comment",
		secretReply:    "what somebody said in a reply",
		secretRequest:  "why somebody asked for access",
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
