package gapi_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

func TestPermissionsAreCreatedUpdatedAndRemoved(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	c := drivetest.Client(t, s)

	p, err := c.CreatePermission(t.Context(), "id-budget-fixture", &gdrive.PermissionMeta{
		Type: "user", Role: "reader", EmailAddress: "alice@example.com",
	}, gapi.ShareOptions{SendNotificationEmail: gdrive.Bool(false)})
	if err != nil {
		t.Fatalf("CreatePermission: %v", err)
	}
	if p.ID == "" || p.Role != "reader" {
		t.Fatalf("permission = %+v", p)
	}

	// One permission per principal: a second create is Drive's 400, not a
	// second grant, which is why the service looks the principal up first.
	if _, err := c.CreatePermission(t.Context(), "id-budget-fixture", &gdrive.PermissionMeta{
		Type: "user", Role: "writer", EmailAddress: "alice@example.com",
	}, gapi.ShareOptions{SendNotificationEmail: gdrive.Bool(false)}); err == nil {
		t.Error("a second grant to the same principal was accepted")
	}

	updated, err := c.UpdatePermission(t.Context(), "id-budget-fixture", p.ID,
		&gdrive.PermissionMeta{Role: "writer", ExpirationTime: "2026-12-01T00:00:00Z"},
		gapi.UpdateShareOptions{})
	if err != nil {
		t.Fatalf("UpdatePermission: %v", err)
	}
	if updated.Role != "writer" || updated.ExpirationTime == "" {
		t.Fatalf("updated = %+v", updated)
	}

	// Clearing an expiry is removeExpiration, not an empty field: Drive
	// does not read "" as "remove it", which the reference is explicit
	// about and memory was wrong about.
	cleared, err := c.UpdatePermission(t.Context(), "id-budget-fixture", p.ID,
		&gdrive.PermissionMeta{}, gapi.UpdateShareOptions{RemoveExpiration: true})
	if err != nil {
		t.Fatalf("UpdatePermission: %v", err)
	}
	if cleared.ExpirationTime != "" {
		t.Errorf("expiry = %q, want it cleared", cleared.ExpirationTime)
	}

	if err := c.DeletePermission(t.Context(), "id-budget-fixture", p.ID); err != nil {
		t.Fatalf("DeletePermission: %v", err)
	}
	if len(s.Permissions["id-budget-fixture"]) != 0 {
		t.Errorf("permissions = %+v", s.Permissions["id-budget-fixture"])
	}
}

func TestASharingWriteIsNeverRepeatedAfterAnAmbiguousFailure(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	// A 500 proves Google answered, not that it did nothing: the grant
	// may already exist. A create carries no id that would collapse a
	// repeat, so it must be reported rather than retried.
	s.Fail = drivetest.FailTimes(1, "/permissions", drivetest.Failure{
		Status: http.StatusInternalServerError, Reason: "internalError", Message: "backend error",
	})
	c := drivetest.Client(t, s)

	_, err := c.CreatePermission(t.Context(), "id-budget-fixture", &gdrive.PermissionMeta{
		Type: "user", Role: "reader", EmailAddress: "alice@example.com",
	}, gapi.ShareOptions{SendNotificationEmail: gdrive.Bool(false)})
	if err == nil {
		t.Fatal("the failure was swallowed by a retry")
	}
	if n := s.Count("/permissions"); n != 1 {
		t.Errorf("%d attempts were made, want exactly one", n)
	}
}

func TestCreatingASharedDriveTwiceWithOneRequestIDMakesOneDrive(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	c := drivetest.Client(t, s)

	first, err := c.CreateDrive(t.Context(), "request-id-fixture", &gdrive.DriveMeta{Name: "Research"})
	if err != nil {
		t.Fatalf("CreateDrive: %v", err)
	}
	// The requestId is the whole reason a create is safe to repeat: Drive
	// collapses the second into the first.
	again, err := c.CreateDrive(t.Context(), "request-id-fixture", &gdrive.DriveMeta{Name: "Research"})
	if err != nil {
		t.Fatalf("CreateDrive again: %v", err)
	}
	if again.ID != first.ID {
		t.Errorf("a repeat made a second drive: %s and %s", first.ID, again.ID)
	}
	if len(s.Drives) != 1 {
		t.Errorf("%d drives exist", len(s.Drives))
	}
	if _, err := c.CreateDrive(t.Context(), "", &gdrive.DriveMeta{Name: "x"}); !errors.Is(err, gapi.ErrInvalid) {
		t.Errorf("a create with no requestId = %v, want it refused before the call", err)
	}
}

func TestHidingADriveUsesItsOwnEndpoint(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	d := s.AddDrive("id-drive-fixture", "Marketing")
	c := drivetest.Client(t, s)

	if _, err := c.HideDrive(t.Context(), d.ID); err != nil {
		t.Fatalf("HideDrive: %v", err)
	}
	if !s.Drives[d.ID].Hidden {
		t.Error("the drive is not hidden")
	}
	// The reference gives hide and unhide their own endpoints; a patch
	// field that might be quietly ignored is not the way to change what a
	// person sees in their sidebar.
	if s.Count("/hide") != 1 {
		t.Errorf("hide did not go to its own endpoint: %+v", s.Requests)
	}
	if _, err := c.UnhideDrive(t.Context(), d.ID); err != nil {
		t.Fatalf("UnhideDrive: %v", err)
	}
	if s.Drives[d.ID].Hidden {
		t.Error("the drive is still hidden")
	}
}

func TestTheChangesFeedPagesAndHandsBackTheNextToken(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	c := drivetest.Client(t, s)

	start, err := c.StartPageToken(t.Context(), "")
	if err != nil {
		t.Fatalf("StartPageToken: %v", err)
	}
	for _, name := range []string{"one", "two", "three"} {
		if _, err := c.CreateFile(t.Context(), &gdrive.FileMeta{
			Name: name, MimeType: gdrive.MimeFolder, Parents: []string{s.RootID},
		}, gapi.WriteOptions{}); err != nil {
			t.Fatalf("CreateFile: %v", err)
		}
	}

	page, err := c.ListChanges(t.Context(), gapi.ListChangesOptions{PageToken: start, PageSize: 2})
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if len(page.Changes) != 2 {
		t.Fatalf("first page had %d changes, want 2", len(page.Changes))
	}
	// A page that did not exhaust the feed carries the token to continue
	// with, and only the last one carries the token for next time.
	if page.NextPageToken == "" || page.NewStartPageToken != "" {
		t.Fatalf("first page tokens: next=%q newStart=%q", page.NextPageToken, page.NewStartPageToken)
	}
	last, err := c.ListChanges(t.Context(), gapi.ListChangesOptions{PageToken: page.NextPageToken, PageSize: 2})
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if last.NewStartPageToken == "" {
		t.Error("the last page carries no token for next time")
	}
	if _, err := c.ListChanges(t.Context(), gapi.ListChangesOptions{}); !errors.Is(err, gapi.ErrInvalid) {
		t.Errorf("a feed read with no token = %v, want it refused before the call", err)
	}
}

func TestRevisionsAreListedAndDeleted(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	s.SetContent("id-budget-fixture", "one")
	s.SetContent("id-budget-fixture", "two")
	c := drivetest.Client(t, s)

	revs, err := c.ListRevisions(t.Context(), "id-budget-fixture")
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("%d revisions", len(revs))
	}
	// Drive will not delete the version the file is at now.
	if err := c.DeleteRevision(t.Context(), "id-budget-fixture", revs[1].ID); err == nil {
		t.Error("the head revision was deleted")
	}
	if err := c.DeleteRevision(t.Context(), "id-budget-fixture", revs[0].ID); err != nil {
		t.Fatalf("DeleteRevision: %v", err)
	}
	if len(s.Revisions["id-budget-fixture"]) != 1 {
		t.Errorf("revisions = %d", len(s.Revisions["id-budget-fixture"]))
	}
}

func TestEmptyTrashSendsNoDeprecatedParameter(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	c := drivetest.Client(t, s)

	if err := c.EmptyTrash(t.Context(), ""); err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}
	// enforceSingleParent is deprecated in the reference. It was in this
	// call from memory until the discovery document said otherwise, which
	// is the reason this assertion exists rather than a comment.
	for _, r := range s.Requests {
		if strings.HasSuffix(r.Path, "/files/trash") {
			if r.Query.Get("enforceSingleParent") != "" {
				t.Errorf("a deprecated parameter was sent: %v", r.Query)
			}
		}
	}
	if s.Files["id-old-plan-fixture"] != nil {
		t.Error("the trashed file survived")
	}
}

// TestAReasonIsRecognisedInEitherSpelling covers the confusion that made
// the rate-limit vocabulary silently wrong: Google writes one condition
// as camelCase in the legacy error.errors[] envelope and as
// UPPER_SNAKE_CASE in a google.rpc.ErrorInfo detail, and this client
// prefers the detail. Comparing the camelCase constant with == therefore
// missed every modern response, classifying a throttled read as a
// permission error — the exact bug the classes exist to prevent.
func TestAReasonIsRecognisedInEitherSpelling(t *testing.T) {
	for _, tc := range []struct {
		reason string
		status int
		want   string
	}{
		{"rateLimitExceeded", http.StatusForbidden, gapi.ClassRateLimited},
		{"RATE_LIMIT_EXCEEDED", http.StatusForbidden, gapi.ClassRateLimited},
		{"sharingRateLimitExceeded", http.StatusForbidden, gapi.ClassRateLimited},
		{"SHARING_RATE_LIMIT_EXCEEDED", http.StatusForbidden, gapi.ClassRateLimited},
		{"domainPolicy", http.StatusForbidden, gapi.ClassBlocked},
		{"DOMAIN_POLICY", http.StatusForbidden, gapi.ClassBlocked},
		{"teamDrivesFolderMoveInNotSupported", http.StatusForbidden, gapi.ClassUnsupported},
		{"TEAM_DRIVES_FOLDER_MOVE_IN_NOT_SUPPORTED", http.StatusForbidden, gapi.ClassUnsupported},
		// A plain lack of rights must stay forbidden in both spellings:
		// the point is that the mapping is spelling-blind, not that
		// everything becomes a rate limit.
		{"insufficientFilePermissions", http.StatusForbidden, gapi.ClassForbidden},
		{"INSUFFICIENT_FILE_PERMISSIONS", http.StatusForbidden, gapi.ClassForbidden},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			s := drivetest.New()
			defer s.Close()
			drivetest.SmallTree(s)
			s.Fail = func(*http.Request) *drivetest.Failure {
				return &drivetest.Failure{Status: tc.status, Reason: tc.reason, Message: "refused"}
			}
			c := drivetest.Client(t, s, func(o *gapi.Options) {
				o.Retry = gapi.RetryPolicy{MaxAttempts: 1, BaseDelay: 1, MaxDelay: 1}
			})
			_, err := c.GetFile(t.Context(), "id-budget-fixture", gapi.GetFileOptions{})
			if got := gapi.Class(err); got != tc.want {
				t.Errorf("Class = %s, want %s (err %v)", got, tc.want, err)
			}
		})
	}
}

// TestADailyQuotaIsRateLimitedButNotRetried separates the two things a
// 403 quota reason can mean. A burst is worth waiting out; a daily
// project quota resets on a clock, so retrying only spends attempts and
// the advice "wait a minute and try again" is false.
func TestADailyQuotaIsRateLimitedButNotRetried(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	s.Fail = func(*http.Request) *drivetest.Failure {
		return &drivetest.Failure{Status: http.StatusForbidden, Reason: "dailyLimitExceeded",
			Message: "Daily Limit Exceeded"}
	}
	c := drivetest.Client(t, s)

	_, err := c.GetFile(t.Context(), "id-budget-fixture", gapi.GetFileOptions{})
	if gapi.Class(err) != gapi.ClassRateLimited {
		t.Errorf("Class = %s, want rate_limited", gapi.Class(err))
	}
	if !gapi.IsDailyQuota(err) {
		t.Error("IsDailyQuota did not recognise it")
	}
	if n := s.Count(http.MethodGet); n != 1 {
		t.Errorf("%d attempts were made; backing off cannot free a daily quota", n)
	}

	// A burst, by contrast, is exactly what retrying is for.
	s.Fail = drivetest.FailTimes(2, "/files", drivetest.Failure{
		Status: http.StatusForbidden, Reason: "userRateLimitExceeded", Message: "slow down",
	})
	s.Requested()
	if _, err := c.GetFile(t.Context(), "id-budget-fixture", gapi.GetFileOptions{}); err != nil {
		t.Fatalf("a burst was not retried through: %v", err)
	}
	if n := s.Count(http.MethodGet); n != 3 {
		t.Errorf("%d attempts were made, want two refusals and a success", n)
	}
}
