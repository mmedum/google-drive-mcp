package gapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

func TestCommentsAndRepliesRoundTrip(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	c := drivetest.Client(t, s)

	made, err := c.CreateComment(t.Context(), "id-budget-fixture", &gdrive.CommentMeta{Content: "is this right?"})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if made.ID == "" || made.Content != "is this right?" || made.Resolved {
		t.Fatalf("comment = %+v", made)
	}

	reply, err := c.CreateReply(t.Context(), "id-budget-fixture", made.ID, &gdrive.ReplyMeta{Content: "no"})
	if err != nil {
		t.Fatalf("CreateReply: %v", err)
	}

	// Resolving is a reply carrying an action: Drive has no field on the
	// comment to set, which is why reply_comment is where resolving lives.
	if _, err := c.CreateReply(t.Context(), "id-budget-fixture", made.ID,
		&gdrive.ReplyMeta{Action: gdrive.ReplyActionResolve}); err != nil {
		t.Fatalf("CreateReply resolve: %v", err)
	}
	got, err := c.GetComment(t.Context(), "id-budget-fixture", made.ID, false)
	if err != nil {
		t.Fatalf("GetComment: %v", err)
	}
	if !got.Resolved {
		t.Error("a reply with action resolve did not resolve the thread")
	}
	if len(got.Replies) != 2 {
		t.Fatalf("thread carries %d replies, want the two that were added", len(got.Replies))
	}

	if _, err := c.UpdateComment(t.Context(), "id-budget-fixture", made.ID,
		&gdrive.CommentMeta{Content: "is this still right?"}); err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}
	if _, err := c.UpdateReply(t.Context(), "id-budget-fixture", made.ID, reply.ID,
		&gdrive.ReplyMeta{Content: "not any more"}); err != nil {
		t.Fatalf("UpdateReply: %v", err)
	}

	page, err := c.ListComments(t.Context(), "id-budget-fixture", gapi.ListCommentsOptions{})
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(page.Comments) != 1 || page.Comments[0].Content != "is this still right?" {
		t.Fatalf("listing = %+v", page.Comments)
	}

	if err := c.DeleteReply(t.Context(), "id-budget-fixture", made.ID, reply.ID); err != nil {
		t.Fatalf("DeleteReply: %v", err)
	}
	if err := c.DeleteComment(t.Context(), "id-budget-fixture", made.ID); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}
	page, err = c.ListComments(t.Context(), "id-budget-fixture", gapi.ListCommentsOptions{})
	if err != nil {
		t.Fatalf("ListComments after delete: %v", err)
	}
	if len(page.Comments) != 0 {
		t.Errorf("a deleted thread is still in the default listing: %+v", page.Comments)
	}

	// A tombstone is what includeDeleted is for: the thread stays and its
	// words are gone.
	page, err = c.ListComments(t.Context(), "id-budget-fixture", gapi.ListCommentsOptions{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("ListComments includeDeleted: %v", err)
	}
	if len(page.Comments) != 1 || !page.Comments[0].Deleted || page.Comments[0].Content != "" {
		t.Errorf("tombstone = %+v", page.Comments)
	}
}

// TestEveryCommentReadAsksForFields covers the one rule this corner of
// the API has that no other does: comments.list, get, create and update
// answer 400 without a fields parameter. The fake refuses it too, so a
// call that forgot would fail here rather than in production — this
// asserts the parameter is on the wire, which is the half a passing call
// does not prove on its own.
func TestEveryCommentReadAsksForFields(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	c := drivetest.Client(t, s)

	made, err := c.CreateComment(t.Context(), "id-budget-fixture", &gdrive.CommentMeta{Content: "hello"})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if _, err := c.GetComment(t.Context(), "id-budget-fixture", made.ID, false); err != nil {
		t.Fatalf("GetComment: %v", err)
	}
	if _, err := c.ListComments(t.Context(), "id-budget-fixture", gapi.ListCommentsOptions{}); err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if _, err := c.UpdateComment(t.Context(), "id-budget-fixture", made.ID,
		&gdrive.CommentMeta{Content: "hello again"}); err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}
	seen := 0
	for _, r := range s.Requested() {
		if !strings.Contains(r.Path, "/comments") || r.Method == http.MethodDelete {
			continue
		}
		seen++
		if r.Query.Get("fields") == "" {
			t.Errorf("%s %s carried no fields parameter, which Drive requires here", r.Method, r.Path)
		}
	}
	if seen < 4 {
		t.Fatalf("only %d comment requests were recorded; the assertion above cannot have been looking "+
			"at the four calls this test makes", seen)
	}
}

// TestCommentEndpointsSendNoSupportsAllDrives holds a fact from the
// discovery document: the comment methods take no supportsAllDrives, so
// sending one would be an unknown parameter on every call.
func TestCommentEndpointsSendNoSupportsAllDrives(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	c := drivetest.Client(t, s)
	if _, err := c.ListComments(t.Context(), "id-q3-plan-fixture", gapi.ListCommentsOptions{}); err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	recorded := s.Requested()
	if len(recorded) != 1 {
		t.Fatalf("recorded %d requests, want the one this test makes", len(recorded))
	}
	if _, ok := recorded[0].Query["supportsAllDrives"]; ok {
		t.Error("a comment call carried supportsAllDrives, which the reference gives it no parameter for")
	}
}

// TestACommentIsNeverPostedTwice is the retry rule where it matters
// most: a comment carries no pre-generated id, so a second attempt after
// an ambiguous 500 would put the same remark in front of everybody who
// can see the file. The transport must give up instead.
func TestACommentIsNeverPostedTwice(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	s.Fail = drivetest.FailTimes(1, "/comments", drivetest.Failure{
		Status: http.StatusInternalServerError, Reason: "backendError", Message: "backend error",
	})
	c := drivetest.Client(t, s)
	if _, err := c.CreateComment(t.Context(), "id-budget-fixture", &gdrive.CommentMeta{Content: "once"}); err == nil {
		t.Fatal("a comment create was retried through a 500 and reported success")
	}
	posts := 0
	for _, r := range s.Requested() {
		if r.Method == http.MethodPost && strings.Contains(r.Path, "/comments") {
			posts++
		}
	}
	if posts != 1 {
		t.Errorf("the create was attempted %d times; a POST with no id of its own may be attempted once", posts)
	}
}

func TestCommentPagingCarriesTheToken(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	for _, text := range []string{"first", "second", "third"} {
		s.AddComment("id-budget-fixture", "id-comment-"+text, text)
	}
	c := drivetest.Client(t, s)
	page, err := c.ListComments(t.Context(), "id-budget-fixture", gapi.ListCommentsOptions{PageSize: 2})
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(page.Comments) != 2 || page.NextPageToken == "" {
		t.Fatalf("first page = %d comments, token %q", len(page.Comments), page.NextPageToken)
	}
	rest, err := c.ListComments(t.Context(), "id-budget-fixture",
		gapi.ListCommentsOptions{PageSize: 2, PageToken: page.NextPageToken})
	if err != nil {
		t.Fatalf("ListComments page 2: %v", err)
	}
	if len(rest.Comments) != 1 || rest.NextPageToken != "" {
		t.Errorf("second page = %d comments, token %q", len(rest.Comments), rest.NextPageToken)
	}
}

func TestCommentCallsRefuseAnEmptyID(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	c := drivetest.Client(t, s)
	if _, err := c.GetComment(t.Context(), "id-budget-fixture", "", false); err == nil {
		t.Error("a comment read with no comment id was sent")
	}
	if err := c.DeleteReply(t.Context(), "id-budget-fixture", "id-comment-1", ""); err == nil {
		t.Error("a reply delete with no reply id was sent")
	}
	if len(s.Requested()) != 0 {
		t.Error("a request with a missing id reached the network")
	}
}
