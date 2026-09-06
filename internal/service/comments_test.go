package service_test

import (
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

func TestListCommentsShowsThreadsRepliesAndWhatIsOpen(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddComment("id-budget-fixture", "id-comment-1", "is this row double counted?",
		drivetest.WithReply("id-reply-1", "Other Person", "yes", ""))
	fake.AddComment("id-budget-fixture", "id-comment-2", "typo",
		drivetest.WithReply("id-reply-2", "Other Person", "fixed", "resolve"))

	out, err := svc.ListComments(t.Context(), service.ListCommentsInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	for _, want := range []string{"2 comment threads, 1 open", "id-comment-1", "open", "resolved",
		"| is this row double counted?", "| yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not carry %q:\n%s", want, out)
		}
	}
}

func TestListCommentsRefusesAFolderAndAnOversizedPage(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	if _, err := svc.ListComments(t.Context(), service.ListCommentsInput{File: "id-projects-fixture"}); err == nil {
		t.Error("a folder was accepted, and Drive has no comments on a folder")
	}
	_, err := svc.ListComments(t.Context(), service.ListCommentsInput{File: "id-budget-fixture", PageSize: 500})
	if err == nil {
		t.Fatal("a page size above Drive's ceiling was accepted")
	}
	if !strings.Contains(err.Error(), "100") {
		t.Errorf("the refusal does not name the ceiling: %v", err)
	}
}

func TestSinceTakesTheSameDateFormsASearchDoes(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddComment("id-budget-fixture", "id-comment-1", "old")
	if _, err := svc.ListComments(t.Context(), service.ListCommentsInput{
		File: "id-budget-fixture", Since: "2026-01-01",
	}); err != nil {
		t.Fatalf("a plain date was refused: %v", err)
	}
	sent := lastQuery(t, fake, "GET", "/comments", "startModifiedTime")
	if !strings.HasPrefix(sent, "2026-01-01T") {
		t.Errorf("startModifiedTime = %q, want the RFC 3339 form of the date", sent)
	}
	if _, err := svc.ListComments(t.Context(), service.ListCommentsInput{
		File: "id-budget-fixture", Since: "last tuesday",
	}); err == nil {
		t.Error("a date this server cannot read was passed to Drive rather than refused")
	}
}

func TestAddCommentRefusesWhatDriveWould(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	if _, err := svc.AddComment(t.Context(), service.AddCommentInput{File: "id-budget-fixture"}); err == nil {
		t.Error("a comment with no content was sent")
	}
	// capabilities.canComment is Drive's own answer, and it is checked
	// before the call rather than after the refusal.
	fake.Files["id-budget-fixture"].Capabilities = &gdrive.Capabilities{CanEdit: true}
	_, err := svc.AddComment(t.Context(), service.AddCommentInput{
		File: "id-budget-fixture", Content: "hello",
	})
	if err == nil {
		t.Fatal("a comment was attempted on a file this account cannot comment on")
	}
	if !strings.Contains(err.Error(), "[forbidden]") {
		t.Errorf("class = %v", err)
	}
	if fake.Count("/comments") != 0 {
		t.Error("the refusal still cost a call to Drive")
	}
}

func TestReplyCommentResolvesReopensAndSaysWhenNothingChanged(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddComment("id-budget-fixture", "id-comment-1", "is this right?")

	got, err := svc.ReplyComment(t.Context(), service.ReplyCommentInput{
		File: "id-budget-fixture", Comment: "id-comment-1", Action: "resolve",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.JSON.Action != "resolved" {
		t.Errorf("action = %q, want resolved", got.JSON.Action)
	}
	if !fake.Comments["id-budget-fixture"][0].Resolved {
		t.Error("the thread is not resolved in Drive")
	}

	// Resolving a resolved thread is not a second reply: Drive would
	// take one, and everybody watching the file would see it.
	before := len(fake.Comments["id-budget-fixture"][0].Replies)
	again, err := svc.ReplyComment(t.Context(), service.ReplyCommentInput{
		File: "id-budget-fixture", Comment: "id-comment-1", Action: "resolve",
	})
	if err != nil {
		t.Fatalf("resolve again: %v", err)
	}
	if again.JSON.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", again.JSON.Action)
	}
	if got := len(fake.Comments["id-budget-fixture"][0].Replies); got != before {
		t.Errorf("replies = %d, want the %d already there", got, before)
	}

	reopened, err := svc.ReplyComment(t.Context(), service.ReplyCommentInput{
		File: "id-budget-fixture", Comment: "id-comment-1", Action: "reopen",
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.JSON.Action != "reopened" || fake.Comments["id-budget-fixture"][0].Resolved {
		t.Errorf("action = %q, resolved = %v", reopened.JSON.Action, fake.Comments["id-budget-fixture"][0].Resolved)
	}
}

func TestReplyCommentNeedsWordsToSayAnything(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddComment("id-budget-fixture", "id-comment-1", "is this right?")
	_, err := svc.ReplyComment(t.Context(), service.ReplyCommentInput{
		File: "id-budget-fixture", Comment: "id-comment-1",
	})
	if err == nil {
		t.Fatal("an empty reply was sent")
	}
	if !strings.Contains(err.Error(), "resolve") {
		t.Errorf("the refusal does not point at the action that closes a thread without words: %v", err)
	}
}

func TestEditChangesAReplyOrTheCommentItOpened(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddComment("id-budget-fixture", "id-comment-1", "frist draft",
		drivetest.WithReply("id-reply-1", "Other Person", "lgtm", ""))

	if _, err := svc.ReplyComment(t.Context(), service.ReplyCommentInput{
		File: "id-budget-fixture", Comment: "id-comment-1", Action: "edit", Content: "first draft",
	}); err != nil {
		t.Fatalf("edit the comment: %v", err)
	}
	if got := fake.Comments["id-budget-fixture"][0].Content; got != "first draft" {
		t.Errorf("comment = %q", got)
	}
	if _, err := svc.ReplyComment(t.Context(), service.ReplyCommentInput{
		File: "id-budget-fixture", Comment: "id-comment-1", Action: "edit",
		Reply: "id-reply-1", Content: "looks good to me",
	}); err != nil {
		t.Fatalf("edit the reply: %v", err)
	}
	if got := fake.Comments["id-budget-fixture"][0].Replies[0].Content; got != "looks good to me" {
		t.Errorf("reply = %q", got)
	}
}

func TestUnknownCommentActionNamesTheOnesThatExist(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddComment("id-budget-fixture", "id-comment-1", "hello")
	_, err := svc.ReplyComment(t.Context(), service.ReplyCommentInput{
		File: "id-budget-fixture", Comment: "id-comment-1", Action: "close", Content: "x",
	})
	if err == nil {
		t.Fatal("an action nobody implemented was accepted")
	}
	for _, action := range service.CommentActions() {
		if !strings.Contains(err.Error(), action) {
			t.Errorf("the refusal does not name %q: %v", action, err)
		}
	}
}

func TestDeleteCommentIsGatedTwice(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddComment("id-budget-fixture", "id-comment-1", "hello")
	_, err := svc.DeleteComment(t.Context(), service.DeleteCommentInput{
		File: "id-budget-fixture", Comment: "id-comment-1", Confirm: true,
	})
	if err == nil {
		t.Fatal("a comment was deleted with GDRIVE_ENABLE_DESTRUCTIVE unset")
	}

	svc, fake = setup(t, service.Options{Destructive: true})
	fake.AddComment("id-budget-fixture", "id-comment-1", "hello")
	if _, err := svc.DeleteComment(t.Context(), service.DeleteCommentInput{
		File: "id-budget-fixture", Comment: "id-comment-1",
	}); err == nil {
		t.Error("a comment was deleted without confirm")
	}
	if fake.Comments["id-budget-fixture"][0].Deleted {
		t.Fatal("the unconfirmed call deleted it anyway")
	}
	got, err := svc.DeleteComment(t.Context(), service.DeleteCommentInput{
		File: "id-budget-fixture", Comment: "id-comment-1", Confirm: true,
	})
	if err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}
	if got.JSON.Action != "deleted" || !fake.Comments["id-budget-fixture"][0].Deleted {
		t.Errorf("action = %q, deleted = %v", got.JSON.Action, fake.Comments["id-budget-fixture"][0].Deleted)
	}
}

func TestCommentWritesAreRefusedInReadOnlyMode(t *testing.T) {
	svc, fake := setup(t, service.Options{ReadOnly: true})
	fake.AddComment("id-budget-fixture", "id-comment-1", "hello")
	if _, err := svc.AddComment(t.Context(), service.AddCommentInput{
		File: "id-budget-fixture", Content: "hello",
	}); err == nil {
		t.Error("add_comment worked in read-only mode")
	}
	if _, err := svc.ReplyComment(t.Context(), service.ReplyCommentInput{
		File: "id-budget-fixture", Comment: "id-comment-1", Content: "hello",
	}); err == nil {
		t.Error("reply_comment worked in read-only mode")
	}
	// The listing still answers: seeing a conversation is not changing it.
	if _, err := svc.ListComments(t.Context(), service.ListCommentsInput{File: "id-budget-fixture"}); err != nil {
		t.Errorf("list_comments was refused in read-only mode: %v", err)
	}
}

func TestAMissingThreadSaysWhereTheIdsComeFrom(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.ReplyComment(t.Context(), service.ReplyCommentInput{
		File: "id-budget-fixture", Comment: "id-nothing", Content: "hello",
	})
	if err == nil {
		t.Fatal("a reply to a thread that is not there succeeded")
	}
	if !strings.Contains(err.Error(), "list_comments") {
		t.Errorf("the refusal does not say where the ids come from: %v", err)
	}
}
