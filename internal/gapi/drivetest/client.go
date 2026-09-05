package drivetest

import (
	"context"
	"net/url"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/time/rate"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
)

// Client returns a Drive client wired to this fake, with the limiters
// open and the backoff instant. Every test that talks to the fake needs
// the same four non-obvious options, and three hand-written copies had
// already drifted: one of them left a limiter at its production rate.
//
// Pass overrides to change one field; anything left zero keeps the
// test-friendly default.
func Client(t *testing.T, s *Server, overrides ...func(*gapi.Options)) *gapi.Client {
	t.Helper()
	o := gapi.Options{
		BaseURL:        s.BaseURL(),
		Timeout:        5 * time.Second,
		ReadLimiter:    rate.NewLimiter(rate.Inf, 1),
		WriteLimiter:   rate.NewLimiter(rate.Inf, 1),
		SharingLimiter: rate.NewLimiter(rate.Inf, 1),
		Sleep:          func(context.Context, time.Duration) error { return nil },
		// The fake is not a Google host, so the production allowlist has
		// to be stood down here and nowhere else.
		AllowURL: func(*url.URL) bool { return true },
	}
	for _, f := range overrides {
		f(&o)
	}
	return gapi.New(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"}), o)
}

// NoCredentials returns a client wired to this fake whose token source
// always fails, for testing what a server without a login answers.
func NoCredentials(t *testing.T, s *Server) *gapi.Client {
	t.Helper()
	o := gapi.Options{
		BaseURL:     s.BaseURL(),
		AllowURL:    func(*url.URL) bool { return true },
		ReadLimiter: rate.NewLimiter(rate.Inf, 1),
		Retry:       gapi.RetryPolicy{MaxAttempts: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
	}
	return gapi.New(gapi.NoCredentials{}, o)
}

// SmallTree fills the fake with the synthetic tree the tests share:
// My Drive/Projects/2026 holding a spreadsheet and a doc, an Archive
// folder with a trashed file, a shortcut, and a shared drive with a
// folder and a document inside it. Everything is invented.
func SmallTree(s *Server) {
	s.SetNow(func() time.Time { return time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC) })
	s.AddFolder("id-projects-fixture", "Projects", s.RootID)
	s.AddFolder("id-2026-fixture", "2026", "id-projects-fixture")
	s.AddFolder("id-archive-fixture", "Archive", "id-projects-fixture")
	s.AddFile("id-budget-fixture", "Budget.xlsx",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "id-2026-fixture",
		Size(4096, "d41d8cd98f00b204e9800998ecf8427e"))
	s.AddFile("id-notes-fixture", "Meeting notes", "application/vnd.google-apps.document", "id-2026-fixture")
	s.AddFile("id-old-plan-fixture", "Old plan", "application/vnd.google-apps.document", "id-archive-fixture", Trashed())
	s.AddShortcut("id-shortcut-fixture", "Budget shortcut", "id-projects-fixture", "id-budget-fixture")

	s.AddDrive("id-drive-marketing", "Marketing")
	s.AddFolder("id-campaigns-fixture", "Campaigns", "id-drive-marketing", InDrive("id-drive-marketing"))
	s.AddFile("id-q3-plan-fixture", "Q3 plan", "application/vnd.google-apps.document", "id-campaigns-fixture",
		InDrive("id-drive-marketing"))
}
