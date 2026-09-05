package gapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/time/rate"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// newClient wires a client to a fake Drive with the limiters wide open
// and no sleeping, so a retry test finishes in microseconds.
func newClient(t *testing.T, s *drivetest.Server) *gapi.Client {
	t.Helper()
	return drivetest.Client(t, s)
}

func fixture(t *testing.T) *drivetest.Server {
	t.Helper()
	s := drivetest.New()
	t.Cleanup(s.Close)
	drivetest.SmallTree(s)
	return s
}

func TestAbout(t *testing.T) {
	s := fixture(t)
	c := newClient(t, s)
	about, err := c.About(context.Background())
	if err != nil {
		t.Fatalf("About: %v", err)
	}
	if about.User.EmailAddress != drivetest.AccountEmail {
		t.Errorf("account = %q", about.User.EmailAddress)
	}
	if !about.CanCreateDrives {
		t.Error("canCreateDrives lost")
	}
	if about.StorageQuota == nil || about.StorageQuota.Limit == "" {
		t.Error("storage quota lost")
	}
}

func TestAboutRejectsAResponseWithoutAUser(t *testing.T) {
	s := fixture(t)
	s.About = &gdrive.About{}
	c := newClient(t, s)
	if _, err := c.About(context.Background()); !errors.Is(err, gapi.ErrUnexpected) {
		t.Fatalf("err = %v, want ErrUnexpected", err)
	}
}

func TestGetFile(t *testing.T) {
	s := fixture(t)
	c := newClient(t, s)
	f, err := c.GetFile(context.Background(), "id-budget-fixture", gapi.GetFileOptions{})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if f.Name != "Budget.xlsx" || f.Parent() != "id-2026-fixture" {
		t.Errorf("file = %+v", f)
	}
	if f.Size != "4096" || f.MD5Checksum == "" {
		t.Errorf("blob details lost: %+v", f)
	}
	if f.Capabilities == nil || !f.Capabilities.CanEdit {
		t.Error("capabilities lost")
	}
	// Every call must carry supportsAllDrives, or shared-drive items go
	// missing, which is the most common failure in the alternatives.
	reqs := s.Requested()
	if len(reqs) != 1 || reqs[0].Query.Get("supportsAllDrives") != "true" {
		t.Errorf("supportsAllDrives missing: %+v", reqs)
	}
}

func TestGetFileNotFound(t *testing.T) {
	s := fixture(t)
	c := newClient(t, s)
	_, err := c.GetFile(context.Background(), "id-does-not-exist", gapi.GetFileOptions{})
	if !errors.Is(err, gapi.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if gapi.Class(err) != "not_found" {
		t.Errorf("class = %q", gapi.Class(err))
	}
}

func TestGetFileExportFormats(t *testing.T) {
	s := fixture(t)
	c := newClient(t, s)
	f, err := c.GetFile(context.Background(), "id-notes-fixture", gapi.GetFileOptions{})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	formats := gapi.ExportFormats(f)
	if len(formats) == 0 {
		t.Fatal("a Google Doc should advertise export formats")
	}
	want := map[string]bool{"pdf": false, "docx": false, "md": false, "txt": false}
	for _, f := range formats {
		if _, ok := want[f]; ok {
			want[f] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("export format %q missing from %v", name, formats)
		}
	}
	// The links themselves never leave the client.
	for _, f := range formats {
		if strings.Contains(f, "http") {
			t.Errorf("export format %q looks like a URL", f)
		}
	}
	if gapi.ExportFormats(nil) != nil {
		t.Error("ExportFormats(nil) should be empty")
	}
}

func TestListFilesInAFolder(t *testing.T) {
	s := fixture(t)
	c := newClient(t, s)
	list, err := c.ListFiles(context.Background(), gapi.ListQuery{
		Q:       "'id-2026-fixture' in parents and trashed = false",
		OrderBy: "folder,name_natural",
	})
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(list.Files) != 2 {
		t.Fatalf("got %d files, want 2", len(list.Files))
	}
	if list.Files[0].Name != "Budget.xlsx" || list.Files[1].Name != "Meeting notes" {
		t.Errorf("order = %q, %q", list.Files[0].Name, list.Files[1].Name)
	}
	reqs := s.Requested()
	if reqs[0].Query.Get("includeItemsFromAllDrives") != "true" || reqs[0].Query.Get("corpora") != "allDrives" {
		t.Errorf("shared drives were not included: %v", reqs[0].Query)
	}
}

func TestListFilesPages(t *testing.T) {
	s := fixture(t)
	for i := range 5 {
		s.AddFile("id-extra-fixture-"+string(rune('a'+i)), "Extra "+string(rune('A'+i)), "text/plain", "id-projects-fixture")
	}
	c := newClient(t, s)
	first, err := c.ListFiles(context.Background(), gapi.ListQuery{Q: "'id-projects-fixture' in parents", PageSize: 2, OrderBy: "name"})
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(first.Files) != 2 || first.NextPageToken == "" {
		t.Fatalf("first page = %d files, token %q", len(first.Files), first.NextPageToken)
	}
	second, err := c.ListFiles(context.Background(), gapi.ListQuery{Q: "'id-projects-fixture' in parents", PageSize: 2, OrderBy: "name", PageToken: first.NextPageToken})
	if err != nil {
		t.Fatalf("ListFiles page 2: %v", err)
	}
	if len(second.Files) != 2 {
		t.Fatalf("second page = %d files", len(second.Files))
	}
	if second.Files[0].ID == first.Files[0].ID {
		t.Error("the second page repeated the first")
	}
}

func TestListFilesInOneSharedDrive(t *testing.T) {
	s := fixture(t)
	c := newClient(t, s)

	list, err := c.ListFiles(context.Background(), gapi.ListQuery{
		Q: "'id-drive-marketing' in parents", DriveID: "id-drive-marketing",
	})
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(list.Files) != 1 || list.Files[0].Name != "Campaigns" {
		t.Fatalf("shared drive listing = %+v", list.Files[0])
	}
	reqs := s.Requested()
	if reqs[0].Query.Get("corpora") != "drive" || reqs[0].Query.Get("driveId") != "id-drive-marketing" {
		t.Errorf("one-drive listing needs corpora=drive and driveId: %v", reqs[0].Query)
	}
}

func TestListFilesRejectsABrokenQuery(t *testing.T) {
	s := fixture(t)
	c := newClient(t, s)
	_, err := c.ListFiles(context.Background(), gapi.ListQuery{Q: "name ~~ 'x'"})
	if !errors.Is(err, gapi.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestGenerateIDs(t *testing.T) {
	s := fixture(t)
	c := newClient(t, s)
	ids, err := c.GenerateIDs(context.Background(), 3)
	if err != nil {
		t.Fatalf("GenerateIDs: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("got %d ids, want 3", len(ids))
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestListPermissions(t *testing.T) {
	s := fixture(t)
	s.Grant("id-budget-fixture", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: "other@example.com", DisplayName: "Other Person"})
	s.Grant("id-budget-fixture", &gdrive.Permission{Type: "anyone", Role: "reader"})
	c := newClient(t, s)
	perms, err := c.ListPermissions(context.Background(), "id-budget-fixture")
	if err != nil {
		t.Fatalf("ListPermissions: %v", err)
	}
	if len(perms) != 2 {
		t.Fatalf("got %d permissions, want 2", len(perms))
	}
}

func TestListPermissionsNotFound(t *testing.T) {
	s := fixture(t)
	c := newClient(t, s)
	if _, err := c.ListPermissions(context.Background(), "id-nope"); !errors.Is(err, gapi.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRetriesRateLimiting(t *testing.T) {
	s := fixture(t)
	s.Fail = drivetest.FailTimes(2, "/files/", drivetest.Failure{
		Status: 403, Reason: "userRateLimitExceeded", Message: "Rate Limit Exceeded",
	})
	c := newClient(t, s)
	if _, err := c.GetFile(context.Background(), "id-budget-fixture", gapi.GetFileOptions{}); err != nil {
		t.Fatalf("GetFile should have retried through the rate limit: %v", err)
	}
	if n := s.Count("/files/"); n != 3 {
		t.Errorf("attempts = %d, want 3", n)
	}
}

func TestRetriesServerErrorsAndHonoursRetryAfter(t *testing.T) {
	s := fixture(t)
	s.Fail = drivetest.FailTimes(1, "/files/", drivetest.Failure{
		Status: 503, Reason: "backendError", Message: "Backend Error", RetryAfter: "1",
	})
	var slept []time.Duration
	c := gapi.New(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "t"}), gapi.Options{
		BaseURL:     s.BaseURL(),
		ReadLimiter: rate.NewLimiter(rate.Inf, 1),
		AllowURL:    func(*url.URL) bool { return true },
		Sleep:       func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil },
	})
	if _, err := c.GetFile(context.Background(), "id-budget-fixture", gapi.GetFileOptions{}); err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if len(slept) != 1 || slept[0] != time.Second {
		t.Errorf("Retry-After ignored: %v", slept)
	}
}

func TestGivesUpAfterTheAttemptBudget(t *testing.T) {
	s := fixture(t)
	s.Fail = func(*http.Request) *drivetest.Failure {
		return &drivetest.Failure{Status: 500, Reason: "internalError", Message: "Internal Error"}
	}
	c := newClient(t, s)
	_, err := c.GetFile(context.Background(), "id-budget-fixture", gapi.GetFileOptions{})
	if !errors.Is(err, gapi.ErrServer) {
		t.Fatalf("err = %v, want ErrServer", err)
	}
	if n := s.Count("/files/"); n != 5 {
		t.Errorf("attempts = %d, want the 5-attempt budget", n)
	}
}

func TestRetriesADroppedConnectionOnReads(t *testing.T) {
	s := fixture(t)
	s.Fail = drivetest.FailTimes(1, "/files/", drivetest.Failure{Hijack: true})
	c := newClient(t, s)
	if _, err := c.GetFile(context.Background(), "id-budget-fixture", gapi.GetFileOptions{}); err != nil {
		t.Fatalf("a read should retry a dropped connection: %v", err)
	}
}

func TestErrorClasses(t *testing.T) {
	cases := []struct {
		status int
		reason string
		want   string
	}{
		{401, "", "auth"},
		{403, "ACCESS_TOKEN_SCOPE_INSUFFICIENT", "forbidden"},
		{403, "domainPolicy", "blocked"},
		{400, "invalidSharingRequest", "blocked"},
		{400, "shareOutNotPermittedForContent", "blocked"},
		{403, "insufficientFilePermissions", "forbidden"},
		{404, "notFound", "not_found"},
		{409, "duplicate", "exists"},
		{400, "duplicate", "exists"},
		{429, "", "rate_limited"},
		{400, "teamDrivesFolderMoveInNotSupported", "unsupported"},
		{400, "invalid", "invalid"},
	}
	for _, c := range cases {
		s := fixture(t)
		s.Fail = func(*http.Request) *drivetest.Failure {
			return &drivetest.Failure{Status: c.status, Reason: c.reason, Message: "message from Google"}
		}
		cl := gapi.New(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "t"}), gapi.Options{
			BaseURL: s.BaseURL(), AllowURL: func(*url.URL) bool { return true },
			ReadLimiter: rate.NewLimiter(rate.Inf, 1),
			Retry:       gapi.RetryPolicy{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
			Sleep:       func(context.Context, time.Duration) error { return nil },
		})
		_, err := cl.GetFile(context.Background(), "id-budget-fixture", gapi.GetFileOptions{})
		if got := gapi.Class(err); got != c.want {
			t.Errorf("HTTP %d %s: class = %q, want %q (err %v)", c.status, c.reason, got, c.want, err)
		}
		if c.want == "blocked" && gapi.Message(err) != "message from Google" {
			t.Errorf("a policy refusal must carry Google's own message, got %q", gapi.Message(err))
		}
		s.Close()
	}
}

func TestAbuseAndOwnershipHelpers(t *testing.T) {
	s := fixture(t)
	s.Fail = func(*http.Request) *drivetest.Failure {
		return &drivetest.Failure{Status: 403, Reason: "cannotDownloadAbusiveFile", Message: "flagged"}
	}
	c := newClient(t, s)
	_, err := c.GetFile(context.Background(), "id-budget-fixture", gapi.GetFileOptions{})
	if !gapi.IsAbuse(err) {
		t.Error("IsAbuse should recognise cannotDownloadAbusiveFile")
	}
	if gapi.IsNotOwner(err) {
		t.Error("IsNotOwner should not match an abuse refusal")
	}
	if gapi.Reason(err) != "cannotDownloadAbusiveFile" {
		t.Errorf("Reason = %q", gapi.Reason(err))
	}
	if gapi.Reason(errors.New("plain")) != "" || gapi.Message(errors.New("plain")) != "" {
		t.Error("a plain error has no Google reason or message")
	}
}

func TestResourceKeysRideAlong(t *testing.T) {
	s := fixture(t)
	s.AddFile("id-shared-pdf-fixture", "Shared.pdf", "application/pdf", "id-projects-fixture", drivetest.WithResourceKey("key-abc"))
	c := newClient(t, s)

	// The key arrives in the first response and is sent on every later
	// call for that id without the caller passing it again.
	if _, err := c.GetFile(context.Background(), "id-shared-pdf-fixture", gapi.GetFileOptions{}); err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got := c.ResourceKey("id-shared-pdf-fixture"); got != "key-abc" {
		t.Fatalf("resource key not remembered: %q", got)
	}
	s.Requested()
	if _, err := c.GetFile(context.Background(), "id-shared-pdf-fixture", gapi.GetFileOptions{}); err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	reqs := s.Requested()
	if reqs[0].ResourceKeys != "id-shared-pdf-fixture/key-abc" {
		t.Errorf("resource key header = %q", reqs[0].ResourceKeys)
	}
}

func TestResourceKeyFromAURLIsUsedOnTheFirstCall(t *testing.T) {
	s := fixture(t)
	s.AddFile("id-linked-pdf-fixture", "Linked.pdf", "application/pdf", "id-projects-fixture", drivetest.WithResourceKey("key-xyz"))
	c := newClient(t, s)
	if _, err := c.GetFile(context.Background(), "id-linked-pdf-fixture", gapi.GetFileOptions{ResourceKey: "key-from-url"}); err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	reqs := s.Requested()
	if reqs[0].ResourceKeys != "id-linked-pdf-fixture/key-from-url" {
		t.Errorf("the key from the URL should ride on the first call, got %q", reqs[0].ResourceKeys)
	}
}

func TestShortcutTargetKeyIsRemembered(t *testing.T) {
	s := fixture(t)
	s.AddFile("id-target-fixture", "Target.pdf", "application/pdf", "id-projects-fixture", drivetest.WithResourceKey("key-target"))
	s.AddShortcut("id-shortcut-fixture", "Target shortcut", "id-2026-fixture", "id-target-fixture")
	c := newClient(t, s)
	if _, err := c.GetFile(context.Background(), "id-shortcut-fixture", gapi.GetFileOptions{}); err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got := c.ResourceKey("id-target-fixture"); got != "key-target" {
		t.Errorf("the shortcut's target key was not remembered: %q", got)
	}
}

func TestRefusesToSendCredentialsElsewhere(t *testing.T) {
	s := fixture(t)
	// The default allowlist, pointed at a non-Google host.
	c := gapi.New(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "t"}), gapi.Options{
		BaseURL: s.BaseURL(), ReadLimiter: rate.NewLimiter(rate.Inf, 1),
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	_, err := c.GetFile(context.Background(), "id-budget-fixture", gapi.GetFileOptions{})
	if err == nil || !strings.Contains(err.Error(), "refusing to send credentials") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if n := s.Count("/files/"); n != 0 {
		t.Errorf("the request went out anyway (%d times)", n)
	}
}

func TestAllowsGoogleHosts(t *testing.T) {
	// The allowlist is checked through the exported client, so an
	// unreachable host proves only that the URL passed the check.
	for _, host := range []string{
		"https://www.googleapis.com/drive/v3",
		"https://drive.googleapis.com/drive/v3",
		"https://doc-0s-4c-docs.googleusercontent.com/x",
	} {
		c := gapi.New(gapi.NoCredentials{}, gapi.Options{BaseURL: host, Retry: gapi.RetryPolicy{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}})
		_, err := c.GetFile(context.Background(), "id", gapi.GetFileOptions{})
		if err != nil && strings.Contains(err.Error(), "refusing to send credentials") {
			t.Errorf("%s should be allowed", host)
		}
	}
	for _, host := range []string{
		"https://evil.example.com/drive/v3",
		"http://www.googleapis.com/drive/v3",
		// A lookalike that ends in a Google host rather than being one.
		"https://www.googleapis.com.evil.example/drive/v3",
		// An explicit port is refused rather than stripped: Google's
		// endpoints do not use one, and this check is what decides
		// whether an access token leaves the machine.
		"https://www.googleapis.com:8443/drive/v3",
	} {
		c := gapi.New(gapi.NoCredentials{}, gapi.Options{BaseURL: host, Retry: gapi.RetryPolicy{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}})
		_, err := c.GetFile(context.Background(), "id", gapi.GetFileOptions{})
		if err == nil || !strings.Contains(err.Error(), "refusing to send credentials") {
			t.Errorf("%s should be refused, got %v", host, err)
		}
	}
}

func TestNoCredentialsIsAnAuthError(t *testing.T) {
	s := fixture(t)
	c := gapi.New(gapi.NoCredentials{}, gapi.Options{
		BaseURL: s.BaseURL(), AllowURL: func(*url.URL) bool { return true },
		ReadLimiter: rate.NewLimiter(rate.Inf, 1),
		Retry:       gapi.RetryPolicy{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
	})
	_, err := c.About(context.Background())
	if gapi.Class(err) != "auth" {
		t.Fatalf("class = %q (err %v), want auth", gapi.Class(err), err)
	}
	if !errors.Is(err, gapi.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
	if !strings.Contains(err.Error(), "login") {
		t.Errorf("the error should say how to fix it: %v", err)
	}
}

func TestNoCredentialsCarriesItsReason(t *testing.T) {
	ts := gapi.NoCredentials{Reason: errors.New("keyring locked")}
	_, err := ts.Token()
	if err == nil || !strings.Contains(err.Error(), "keyring locked") {
		t.Fatalf("err = %v", err)
	}
	if _, err := (gapi.NoCredentials{}).Token(); !errors.Is(err, gapi.ErrNoCredentials) {
		t.Errorf("err = %v", err)
	}
}

func TestShortIDTruncates(t *testing.T) {
	if got := gapi.ShortID("1AbCdEfGhIjKlMnOp"); got != "1AbCdE…" {
		t.Errorf("ShortID = %q", got)
	}
	if got := gapi.ShortID("short"); got != "short" {
		t.Errorf("ShortID = %q", got)
	}
}

func TestClassOfAnUnknownError(t *testing.T) {
	if got := gapi.Class(errors.New("something else")); got != "unexpected" {
		t.Errorf("Class = %q", got)
	}
}

func TestContextCancellationIsNotRetried(t *testing.T) {
	s := fixture(t)
	c := newClient(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetFile(ctx, "id-budget-fixture", gapi.GetFileOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestListDrivesReturnsWhatDriveReturned(t *testing.T) {
	s := fixture(t)
	hidden := s.AddDrive("id-drive-archive", "Archive")
	hidden.Hidden = true
	c := newClient(t, s)

	// Hidden is a sidebar setting, not an API filter. The client passes
	// it on and the service decides what to do with it.
	list, err := c.ListDrives(context.Background(), gapi.ListDrivesOptions{})
	if err != nil {
		t.Fatalf("ListDrives: %v", err)
	}
	if len(list.Drives) != 2 {
		t.Fatalf("got %d drives, want both: %+v", len(list.Drives), list.Drives)
	}
	var sawHidden bool
	for _, d := range list.Drives {
		if d.Hidden {
			sawHidden = true
		}
	}
	if !sawHidden {
		t.Error("the hidden flag was dropped")
	}
}

func TestNoCredentialsStaysDistinguishableFromARejectedLogin(t *testing.T) {
	s := fixture(t)
	c := drivetest.NoCredentials(t, s)
	_, err := c.About(context.Background())
	// Both are auth failures, but only one is fixed by logging in for the
	// first time, and the service says something different for each.
	if !errors.Is(err, gapi.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
	if !errors.Is(err, gapi.ErrNoCredentials) {
		t.Errorf("err = %v, want ErrNoCredentials to stay reachable through the chain", err)
	}

	rejected := &gapi.AuthError{Code: "invalid_grant", Msg: "Token has been expired or revoked."}
	if !errors.Is(rejected, gapi.ErrUnauthorized) {
		t.Error("a rejected login is still unauthorized")
	}
	if errors.Is(rejected, gapi.ErrNoCredentials) {
		t.Error("a rejected login is not the same as having no credentials")
	}
}

func TestBackoffStaysUnderTheDocumentedCap(t *testing.T) {
	s := fixture(t)
	s.Fail = func(*http.Request) *drivetest.Failure {
		return &drivetest.Failure{Status: 503, Reason: "backendError", Message: "Backend Error"}
	}
	var slept []time.Duration
	policy := gapi.RetryPolicy{MaxAttempts: 5, BaseDelay: 500 * time.Millisecond, MaxDelay: 30 * time.Second}
	c := gapi.New(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "t"}), gapi.Options{
		BaseURL: s.BaseURL(), AllowURL: func(*url.URL) bool { return true },
		ReadLimiter: rate.NewLimiter(rate.Inf, 1), Retry: policy,
		Sleep: func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil },
	})
	// Enough runs to catch a jitter formula that can overshoot the cap.
	for range 40 {
		_, _ = c.GetFile(context.Background(), "id-budget-fixture", gapi.GetFileOptions{})
	}
	if len(slept) == 0 {
		t.Fatal("no backoff was taken")
	}
	for _, d := range slept {
		if d > policy.MaxDelay {
			t.Fatalf("backoff %s exceeds the documented cap of %s", d, policy.MaxDelay)
		}
		if d <= 0 {
			t.Fatalf("backoff %s is not a wait", d)
		}
	}
}

func TestEveryAttemptTakesALimiterToken(t *testing.T) {
	s := fixture(t)
	s.Fail = drivetest.FailTimes(3, "/files/", drivetest.Failure{
		Status: 429, Reason: "rateLimitExceeded", Message: "Rate Limit Exceeded",
	})
	// A limiter with no burst: each attempt has to wait for a token, so a
	// call that retries three times takes measurably longer than one that
	// does not. Retries are caused by rate limiting, so exempting them
	// would push hardest exactly when Drive is asking for less.
	c := gapi.New(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "t"}), gapi.Options{
		BaseURL: s.BaseURL(), AllowURL: func(*url.URL) bool { return true },
		ReadLimiter: rate.NewLimiter(rate.Limit(200), 1),
		Sleep:       func(context.Context, time.Duration) error { return nil },
	})
	start := time.Now()
	if _, err := c.GetFile(context.Background(), "id-budget-fixture", gapi.GetFileOptions{}); err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	// Four attempts at 200/s is at least 15ms of waiting; one attempt is none.
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Errorf("four attempts took %s; retries appear to bypass the limiter", elapsed)
	}
}

func TestGenerateIDsRefusesMoreThanDriveAllows(t *testing.T) {
	s := fixture(t)
	c := newClient(t, s)
	// The client clamps to Drive's own ceiling, so a caller asking for
	// more gets the maximum rather than an error.
	ids, err := c.GenerateIDs(context.Background(), 5000)
	if err != nil {
		t.Fatalf("GenerateIDs: %v", err)
	}
	if len(ids) != 1000 {
		t.Errorf("got %d ids, want the 1000 Drive allows", len(ids))
	}
	if _, err := c.GenerateIDs(context.Background(), 0); err != nil {
		t.Errorf("a count of zero should mean the default, not an error: %v", err)
	}
}
