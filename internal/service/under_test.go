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

	plain, err := svc.Search(t.Context(), service.SearchInput{Kind: "doc", OrderBy: "name", Limit: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, tc := range []struct {
		in   service.SearchInput
		want string
	}{
		{service.SearchInput{Kind: "doc", UnderFolder: "/Projects/2026", PageToken: in.PageToken},
			"this page_token came from a search under another folder"},
		{service.SearchInput{Kind: "doc", PageToken: in.PageToken},
			"this page_token came from a search with under_folder"},
		{service.SearchInput{Kind: "doc", UnderFolder: "/Projects", PageToken: nextToken(t, plain)},
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

// A folder shared by a link from before 2021 is searched only with its
// resource key. The walk learns a key from its listing, and sends it in
// the next level's query and in the search, as in_folder does.
func TestSearchUnderFolderCarriesResourceKeys(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFolder("id-quarter-fixture", "Q1", "id-2026-fixture", drivetest.WithResourceKey("rk-q1"))
	fake.AddFolder("id-q1-inner-fixture", "Inner", "id-quarter-fixture")
	fake.Requested()
	if _, err := svc.Search(t.Context(), service.SearchInput{UnderFolder: "/Projects"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	var walked, below, searched bool
	for _, r := range fake.Requested() {
		if !strings.HasSuffix(r.Path, "/files") {
			continue
		}
		q, keyed := r.Query.Get("q"), strings.Contains(r.ResourceKeys, "id-quarter-fixture/rk-q1")
		switch {
		case strings.Contains(q, "mimeType = 'application/vnd.google-apps.folder'") && strings.Contains(q, "'id-2026-fixture' in parents"):
			walked = strings.Contains(r.Query.Get("fields"), "resourceKey")
		case strings.Contains(q, "mimeType = 'application/vnd.google-apps.folder'") && strings.Contains(q, "'id-quarter-fixture' in parents"):
			below = keyed
		case strings.Contains(q, "'id-quarter-fixture' in parents"):
			searched = keyed
		}
	}
	if !walked || !below || !searched {
		t.Errorf("the walk asked for keys %v, the level below carried Q1's %v, the search carried it %v; want all three",
			walked, below, searched)
	}

	fake.Requested()
	if _, err := svc.Search(t.Context(), service.SearchInput{InFolder: "id-quarter-fixture"}); err != nil {
		t.Fatalf("Search in_folder: %v", err)
	}
	if got := fake.Requested(); len(got) == 0 || !strings.Contains(got[len(got)-1].ResourceKeys, "id-quarter-fixture/rk-q1") {
		t.Errorf("a search in_folder did not carry the folder's resource key: %+v", got)
	}
}

// Drive may answer a page short of what was asked, with a token for the
// rest, so the walk pages through each level to its end.
func TestSearchUnderFolderPagesThroughEachLevel(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	deepTree(fake)
	fake.FilePageCap = 1
	out, err := svc.Search(t.Context(), service.SearchInput{Kind: "doc", UnderFolder: "/Projects"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.HasPrefix(out, "search: kind doc, anywhere under Projects (4 folders) — 1 hit\n") {
		t.Errorf("a walk served a folder a page did not cover every folder:\n%s", out)
	}
}

// A search outside the trash walks no trashed folder; a search of the
// trash walks them, since what is inside a trashed folder is in the
// trash with it.
func TestSearchUnderFolderWalksTrashedFoldersOnlyInTheTrash(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFolder("id-binned-fixture", "Binned", "id-2026-fixture", drivetest.Trashed())
	for trashed, want := range map[bool]string{false: "(3 folders)", true: "(4 folders)"} {
		out, err := svc.Search(t.Context(), service.SearchInput{UnderFolder: "/Projects", Trashed: trashed})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if !strings.Contains(out, "anywhere under Projects "+want) {
			t.Errorf("trashed %v: want %s:\n%s", trashed, want, out)
		}
	}
}

func TestScopeMyDriveIsRefusedInASharedDrive(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	for _, in := range []service.SearchInput{
		{Scope: "my_drive", UnderFolder: "drive:Marketing"},
		{Scope: "my_drive", Drive: "Marketing"},
	} {
		_, err := svc.Search(t.Context(), in)
		if err == nil || !strings.HasPrefix(err.Error(), "[invalid] scope my_drive searches My Drive and what is shared with you") {
			t.Errorf("%+v: err = %v, want a refusal of scope my_drive", in, err)
		}
	}
}

// Drive's page token belongs to the request that made it, so a
// continuation with any other filter, order or scope is refused.
func TestASearchPageContinuesOnlyTheSearchItCameFrom(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	first := service.SearchInput{Kind: "doc", OrderBy: "name", Limit: 1}
	out, err := svc.Search(t.Context(), first)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	token := nextToken(t, out)
	same := first
	same.PageToken, same.Limit = token, 5
	if _, err := svc.Search(t.Context(), same); err != nil {
		t.Errorf("the same search with another limit: %v", err)
	}
	for name, in := range map[string]service.SearchInput{
		"another name":  {Kind: "doc", OrderBy: "name", Name: "Meeting"},
		"another kind":  {Kind: "sheet", OrderBy: "name"},
		"another order": {Kind: "doc", OrderBy: "modified"},
		"another scope": {Kind: "doc", OrderBy: "name", Scope: "my_drive"},
		"the trash":     {Kind: "doc", OrderBy: "name", Trashed: true},
		"a drive":       {Kind: "doc", OrderBy: "name", Drive: "Marketing"},
	} {
		in.PageToken = token
		_, err := svc.Search(t.Context(), in)
		if err == nil || !strings.Contains(err.Error(), "this page_token came from a search with other filters") {
			t.Errorf("%s: err = %v, want the continuation refused", name, err)
		}
	}
	if _, err := svc.Search(t.Context(), service.SearchInput{Kind: "doc", PageToken: "offset-1"}); err == nil ||
		!strings.Contains(err.Error(), "is not one this server gave out") {
		t.Errorf("Drive's own token: err = %v, want it refused", err)
	}
}
