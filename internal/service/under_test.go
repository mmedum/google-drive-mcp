package service_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/v2/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v2/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v2/internal/service"
)

// deepTree adds a folder two levels under Projects with a document in
// it, and a shortcut in Projects to a folder outside it.
func deepTree(fake *drivetest.Server) {
	fake.AddFolder("id-q1-fixture", "Q1", "id-2026-fixture")
	fake.AddFile("id-deep-fixture", "Deep doc", gdrive.MimeDocument, "id-q1-fixture")
	fake.AddFolder("id-elsewhere-fixture", "Elsewhere", fake.RootID)
	fake.AddFile("id-outside-fixture", "Outside doc", gdrive.MimeDocument, "id-elsewhere-fixture")
	fake.AddShortcut("id-to-elsewhere-fixture", "Elsewhere shortcut", "id-projects-fixture", "id-elsewhere-fixture")
}

func TestSearchUnderFolderReachesEveryFolderBelowAndNoShortcut(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	deepTree(fake)
	fake.Requested()

	out, err := svc.Search(t.Context(), service.SearchInput{Kind: "doc", UnderFolder: "id-projects-fixture"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.HasPrefix(out, "search: kind doc, anywhere under Projects (4 folders) — 2 hits\n") {
		t.Errorf("the title does not say what was searched:\n%s", out)
	}
	for _, want := range []string{"Deep doc", "Meeting notes"} {
		if !strings.Contains(out, want) {
			t.Errorf("%s is missing:\n%s", want, out)
		}
	}
	for _, not := range []string{"Outside doc", "Q3 plan"} {
		if strings.Contains(out, not) {
			t.Errorf("%s is outside the folder, or behind a shortcut, and was found:\n%s", not, out)
		}
	}
	// Projects, then 2026 and Archive, then Q1: three levels, one listing
	// each, and the search itself.
	if got := calls(fake)["list"]; got != 4 {
		t.Errorf("the search cost %d listings, want 4: one per level of folders and the search", got)
	}
}

func TestSearchUnderASharedDriveFolderSearchesThatDrive(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Requested()

	out, err := svc.Search(t.Context(), service.SearchInput{Kind: "doc", UnderFolder: "drive:Marketing"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(out, "Q3 plan") || strings.Contains(out, "Meeting notes") {
		t.Errorf("the search under the shared drive did not find what is in it, and only that:\n%s", out)
	}
	for _, r := range fake.Requested() {
		if strings.HasSuffix(r.Path, "/files") && r.Query.Get("q") != "" && r.Query.Get("driveId") != "id-drive-marketing" {
			t.Errorf("a listing went outside the shared drive: driveId %q, q %q", r.Query.Get("driveId"), r.Query.Get("q"))
		}
	}
}

func TestSearchUnderFolderNamesTheFoldersItCannotList(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFolder("id-closed-fixture", "Closed", "id-2026-fixture", drivetest.WithCapabilities(gdrive.Capabilities{}))

	out, err := svc.Search(t.Context(), service.SearchInput{UnderFolder: "/Projects"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.HasSuffix(out, "\nthis account cannot list 1 folder under Projects: Closed. "+
		"Folders below them are not searched.\n") {
		t.Errorf("the result does not name the folder it could not list:\n%s", out)
	}
}

func TestSearchUnderFolderRefusesATreePastTheCap(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFolder("id-wide-fixture", "Wide", fake.RootID)
	// The folder and 99 below it is exactly the cap.
	for i := range service.MaxUnderFolders - 1 {
		fake.AddFolder(fmt.Sprintf("id-wide-%03d-fixture", i), fmt.Sprintf("Sub %03d", i), "id-wide-fixture")
	}
	if _, err := svc.Search(t.Context(), service.SearchInput{UnderFolder: "id-wide-fixture"}); err != nil {
		t.Fatalf("a tree of exactly %d folders was refused: %v", service.MaxUnderFolders, err)
	}

	fake.AddFolder("id-wide-one-more-fixture", "One more", "id-wide-098-fixture")
	_, err := svc.Search(t.Context(), service.SearchInput{UnderFolder: "id-wide-fixture"})
	want := "[invalid] Wide holds more than 100 folders, counting itself, and one search covers at most that many."
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("err = %v, want it to start %q", err, want)
	}
}

func TestSearchUnderFolderRefusesWhatItCannotSearch(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	for _, tc := range []struct {
		in   service.SearchInput
		want string
	}{
		{service.SearchInput{UnderFolder: "/Projects", InFolder: "/Projects"}, "not both"},
		{service.SearchInput{UnderFolder: "id-budget-fixture"}, "under_folder must name a folder"},
	} {
		if _, err := svc.Search(t.Context(), tc.in); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.in, err, tc.want)
		}
	}
}

// nextToken reads the page_token a listing offers.
func nextToken(t *testing.T, out string) string {
	t.Helper()
	_, after, ok := strings.Cut(out, `page_token "`)
	if !ok {
		t.Fatalf("the listing offers no page_token:\n%s", out)
	}
	token, _, _ := strings.Cut(after, `"`)
	return token
}

func TestSearchUnderFolderPagesOverTheSameFolders(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	deepTree(fake)
	in := service.SearchInput{Kind: "doc", UnderFolder: "/Projects", OrderBy: "name", Limit: 1}

	first, err := svc.Search(t.Context(), in)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	in.PageToken = nextToken(t, first)
	fake.Requested()
	second, err := svc.Search(t.Context(), in)
	if err != nil {
		t.Fatalf("the next page: %v", err)
	}
	if !strings.Contains(first, "Deep doc") || !strings.Contains(second, "Meeting notes") {
		t.Errorf("the two pages are not the two documents in name order:\n%s\n%s", first, second)
	}
	// The folders were walked once, for the first page.
	if got := calls(fake)["list"]; got != 1 {
		t.Errorf("the next page cost %d listings, want 1: the folder set is kept", got)
	}

	for _, tc := range []struct {
		in   service.SearchInput
		want string
	}{
		{service.SearchInput{Kind: "doc", UnderFolder: "/Projects/2026", PageToken: in.PageToken},
			"this page_token came from a search under another folder"},
		{service.SearchInput{Kind: "doc", PageToken: in.PageToken},
			"this page_token came from a search with under_folder"},
		{service.SearchInput{Kind: "doc", UnderFolder: "/Projects", PageToken: "offset-1"},
			"this page_token did not come from a search with under_folder"},
		{service.SearchInput{Kind: "doc", UnderFolder: "/Projects", PageToken: "under.not-base64!"},
			"this page_token is not one this server gave out"},
	} {
		if _, err := svc.Search(t.Context(), tc.in); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("page_token %q under %q: err = %v, want %q", tc.in.PageToken, tc.in.UnderFolder, err, tc.want)
		}
	}
}

// A continuation that no longer has the folder set walks again, and
// refuses when the folders are not the ones the first page searched.
func TestSearchUnderFolderRefusesAPageWhoseFoldersChanged(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	deepTree(fake)
	in := service.SearchInput{Kind: "doc", UnderFolder: "/Projects", OrderBy: "name", Limit: 1}
	first, err := svc.Search(t.Context(), in)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	in.PageToken = nextToken(t, first)

	// A second process has no kept set, and walks the same folders.
	later := service.New(drivetest.Client(t, fake), service.Options{Now: func() time.Time { return testNow }})
	if _, err := later.Search(t.Context(), in); err != nil {
		t.Fatalf("a continuation over the same folders was refused: %v", err)
	}

	fake.AddFolder("id-new-fixture", "New", "id-archive-fixture")
	again := service.New(drivetest.Client(t, fake), service.Options{Now: func() time.Time { return testNow }})
	_, err = again.Search(t.Context(), in)
	if err == nil || !strings.Contains(err.Error(), "the folders under Projects changed since the first page") {
		t.Errorf("err = %v, want a refusal saying the folders changed", err)
	}
}
