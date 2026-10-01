package service_test

import (
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mmedum/google-drive-mcp/v2/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v2/internal/service"
)

// A write that is not repeatable and fails after it may have reached
// Google must be reported as [ambiguous_outcome], say it may have been
// applied, and name the read that settles it. docs/architecture.md §11:
// "a sharing write that fails, transiently or otherwise, is reported as
// [ambiguous_outcome] with list_permissions named as the way to find
// out, rather than repeated."
func TestANonRepeatableWriteThatFailsAfterSendingIsAmbiguous(t *testing.T) {
	failures := []struct {
		name string
		f    drivetest.Failure
	}{
		{"503 UNAVAILABLE", drivetest.Failure{Status: http.StatusServiceUnavailable, Reason: "backendError", Message: "Backend Error"}},
		{"500", drivetest.Failure{Status: http.StatusInternalServerError, Reason: "backendError", Message: "Backend Error"}},
		{"connection cut after send", drivetest.Failure{Hijack: true}},
	}
	writes := []struct {
		name string
		path string
		call func(*service.Service, *testing.T) error
	}{
		{"share_file (permissions.create)", "/permissions", func(svc *service.Service, t *testing.T) error {
			_, err := svc.ShareFile(yes(t), service.ShareFileInput{
				File: "id-budget-fixture", Principal: "robin.sample@example.com", Role: "writer",
			})
			return err
		}},
		{"add_comment (comments.create)", "/comments", func(svc *service.Service, t *testing.T) error {
			_, err := svc.AddComment(yes(t), service.AddCommentInput{File: "id-budget-fixture", Content: "a remark"})
			return err
		}},
	}
	for _, w := range writes {
		for _, f := range failures {
			t.Run(w.name+"/"+f.name, func(t *testing.T) {
				svc, fake := setup(t, service.Options{})
				var posts atomic.Int32
				fake.Fail = func(r *http.Request) *drivetest.Failure {
					if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, w.path) {
						return nil
					}
					posts.Add(1)
					out := f.f
					return &out
				}
				err := w.call(svc, t)
				if got := posts.Load(); got != 1 {
					t.Errorf("the write was sent %d times, want 1: it is not repeatable", got)
				}
				var se *service.Error
				if !errors.As(err, &se) {
					t.Fatalf("err = %v, want a *service.Error", err)
				}
				if se.Class != service.ClassAmbiguousIO {
					t.Errorf("class = %q, want %q; message: %s", se.Class, service.ClassAmbiguousIO, se.Message)
				}
				for _, bad := range []string{"could not reach", "failed", "try again"} {
					if strings.Contains(se.Message, bad) {
						t.Errorf("the message says %q of a write that may have been applied: %s", bad, se.Message)
					}
				}
			})
		}
	}
}

// A transport error must not carry the request's query string: a
// search's q= holds the caller's terms, and a path lookup's q= holds a
// file name. The API-error path already drops it (redactPath); the
// transport path wraps net/http's *url.Error, which quotes the full URL.
func TestATransportErrorDoesNotCarryTheSearchTerms(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if r.Method == http.MethodGet && r.URL.Path == "/drive/v3/files" {
			return &drivetest.Failure{Hijack: true}
		}
		return nil
	}
	_, err := svc.Search(t.Context(), service.SearchInput{Text: "canaryterm"})
	if err == nil {
		t.Fatal("a search over a cut connection succeeded")
	}
	for _, bad := range []string{"canaryterm", "q=", "fullText"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("the error carries %q: %v", bad, err)
		}
	}
}

// A delete retried after a 5xx can find the thing gone because the
// first attempt removed it. That 404 is not "nothing was there": the
// outcome is reported as ambiguous, for a read to settle.
func TestARetriedDeleteThatFindsNothingIsAmbiguous(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})
	fake.AddComment("id-budget-fixture", "id-comment-1", "hello")
	var deletes atomic.Int32
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if r.Method != http.MethodDelete {
			return nil
		}
		if deletes.Add(1) == 1 {
			return &drivetest.Failure{Status: http.StatusServiceUnavailable, Reason: "backendError", Message: "Backend Error"}
		}
		return &drivetest.Failure{Status: http.StatusNotFound, Reason: "notFound", Message: "Comment not found"}
	}
	_, err := svc.DeleteComment(yes(t), service.DeleteCommentInput{
		File: "id-budget-fixture", Comment: "id-comment-1", Confirm: true,
	})
	var se *service.Error
	if !errors.As(err, &se) || se.Class != service.ClassAmbiguousIO {
		t.Fatalf("err = %v, want [%s]", err, service.ClassAmbiguousIO)
	}
	if got := deletes.Load(); got != 2 {
		t.Errorf("the delete was sent %d times, want 2: a DELETE is repeatable", got)
	}
}

// A create carrying its own id that got a 5xx may have made the file;
// a repeat that then finds the id taken is ambiguous, not [exists].
func TestACreateWhoseRepeatFindsItsOwnIDIsAmbiguous(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	var creates atomic.Int32
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if r.Method != http.MethodPost || r.URL.Path != "/drive/v3/files" {
			return nil
		}
		if creates.Add(1) == 1 {
			return &drivetest.Failure{Status: http.StatusServiceUnavailable, Reason: "backendError", Message: "Backend Error"}
		}
		return &drivetest.Failure{Status: http.StatusConflict, Reason: "duplicate", Message: "A file with that id already exists"}
	}
	_, err := svc.CreateFolder(yes(t), service.CreateFolderInput{Name: "Reports", AllowDuplicate: true})
	var se *service.Error
	if !errors.As(err, &se) || se.Class != service.ClassAmbiguousIO {
		t.Fatalf("err = %v, want [%s]", err, service.ClassAmbiguousIO)
	}
	if got := creates.Load(); got != 2 {
		t.Errorf("the create was sent %d times, want 2: it carries its own id", got)
	}
}

// A 429 refuses the work before it starts, so a delete that was refused
// and then finds nothing really found nothing.
func TestARefusedThenMissingDeleteIsNotFound(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})
	fake.AddComment("id-budget-fixture", "id-comment-1", "hello")
	var deletes atomic.Int32
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if r.Method != http.MethodDelete {
			return nil
		}
		if deletes.Add(1) == 1 {
			return &drivetest.Failure{Status: http.StatusTooManyRequests, Reason: "rateLimitExceeded", Message: "Rate Limit Exceeded"}
		}
		return &drivetest.Failure{Status: http.StatusNotFound, Reason: "notFound", Message: "Comment not found"}
	}
	_, err := svc.DeleteComment(yes(t), service.DeleteCommentInput{
		File: "id-budget-fixture", Comment: "id-comment-1", Confirm: true,
	})
	var se *service.Error
	if !errors.As(err, &se) || se.Class != service.ClassNotFound {
		t.Fatalf("err = %v, want [%s]", err, service.ClassNotFound)
	}
}
