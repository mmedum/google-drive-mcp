package service_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// The call counts §11 of docs/architecture.md commits to. They are here
// rather than in the benchmarks because a round trip to Google is what
// a person actually waits for, and a target only a hand-run benchmark
// checks is a target nothing enforces. Against the fake a call costs
// microseconds, so nothing but the count is being measured.

// calls counts the requests the fake served, by what they were.
func calls(fake *drivetest.Server) map[string]int {
	out := map[string]int{}
	for _, r := range fake.Requested() {
		switch {
		case strings.HasSuffix(r.Path, "/files"):
			if r.Query.Get("q") != "" {
				out["list"]++
			} else {
				out["other"]++
			}
		case strings.Contains(r.Path, "/files/"):
			out["get"]++
		default:
			out["other"]++
		}
	}
	return out
}

// TestGetFileCostsTheFileAndItsAncestors is the target as the code
// actually behaves, which is not what §11 first claimed. "At most two
// calls" holds for a file at the top of My Drive; below that, the card's
// location line is a climb, and a climb is one read per folder above the
// file. The alternative would be a card that says where a file is only
// sometimes, which is worse than a card that costs a read per level and
// then never costs it again.
func TestGetFileCostsTheFileAndItsAncestors(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-toplevel-fixture", "Top", gdrive.MimeDocument, fake.RootID)

	fake.Requested()
	if _, err := svc.GetFile(t.Context(), service.GetFileInput{File: "id-toplevel-fixture"}); err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got := calls(fake); got["get"]+got["list"]+got["other"] > 2 {
		t.Errorf("get_file on a file at the top of My Drive cost %v, and the target is two", got)
	}

	// Three folders down: the file, and one read per folder above it.
	svc, fake = setup(t, service.Options{})
	fake.Requested()
	if _, err := svc.GetFile(t.Context(), service.GetFileInput{File: "id-budget-fixture"}); err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	got := calls(fake)
	const ancestors = 3 // 2026, Projects, the root
	if total := got["get"] + got["list"] + got["other"]; total > 1+ancestors {
		t.Errorf("get_file three folders down cost %d calls (%v), want the file and its %d ancestors",
			total, got, ancestors)
	}
	// And then it is free: the file and every folder above it are cached.
	fake.Requested()
	if _, err := svc.GetFile(t.Context(), service.GetFileInput{File: "id-budget-fixture"}); err != nil {
		t.Fatalf("GetFile again: %v", err)
	}
	if again := calls(fake); again["get"]+again["list"]+again["other"] != 0 {
		t.Errorf("a second get_file of the same file cost %v, and everything it needs was cached", again)
	}
}

func TestAPathOfDepthDCostsDListingsAndThenNone(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Requested()
	// /Projects/2026/Budget.xlsx is three segments below the root.
	if _, err := svc.Resolve(t.Context(), "/Projects/2026/Budget.xlsx", service.ResolveOptions{}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	first := calls(fake)
	if first["list"] > 3 {
		t.Errorf("resolving a path three deep cost %d listings, want at most three", first["list"])
	}
	if _, err := svc.Resolve(t.Context(), "/Projects/2026/Budget.xlsx", service.ResolveOptions{}); err != nil {
		t.Fatalf("Resolve again: %v", err)
	}
	if second := calls(fake); second["list"] != 0 {
		t.Errorf("the second resolve cost %d listings; the path cache should have answered all of it", second["list"])
	}
}

func TestASearchPageCostsOneListAndBoundedParentLookups(t *testing.T) {
	// The parent of a hit is what turns an id into a location, and a page
	// of a hundred hits from a hundred folders would be a hundred reads
	// without a budget. Here they share one folder, so the cache should
	// make it exactly one.
	svc, fake := setup(t, service.Options{})
	for i := range 100 {
		fake.AddFile(fmt.Sprintf("id-target-hit-%03d", i), fmt.Sprintf("Report %03d", i),
			gdrive.MimeDocument, "id-2026-fixture")
	}
	fake.Requested()
	if _, err := svc.Search(t.Context(), service.SearchInput{Name: "Report"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	got := calls(fake)
	if got["list"] != 1 {
		t.Errorf("a search page cost %d listings, want one", got["list"])
	}
	if got["get"] > 20 {
		t.Errorf("a search page cost %d parent reads, and the budget is twenty", got["get"])
	}
	// The hits share one folder, so what the reads pay for is the chain
	// above it — 2026, Projects, the root — once, not once per hit.
	if got["get"] > 3 {
		t.Errorf("a hundred hits in one folder cost %d parent reads; the cache should make it one per "+
			"folder in the chain above them", got["get"])
	}
}

func TestATreeWalkCostsOneListingPerFolder(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	folders := 0
	for i := range 5 {
		id := fmt.Sprintf("id-target-folder-%d", i)
		fake.AddFolder(id, fmt.Sprintf("Folder %d", i), "id-2026-fixture")
		folders++
		for j := range 3 {
			fake.AddFile(fmt.Sprintf("id-target-file-%d-%d", i, j), fmt.Sprintf("File %d %d", i, j),
				gdrive.MimeDocument, id)
		}
	}
	fake.Requested()
	if _, err := svc.ListFolder(t.Context(), service.ListFolderInput{
		Folder: "id-2026-fixture", Recursive: true, MaxDepth: 10, MaxItems: 2000,
	}); err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	got := calls(fake)
	// The folder being listed, plus one per folder found inside it.
	if want := folders + 1; got["list"] > want {
		t.Errorf("the walk cost %d listings for %d folders, want at most %d", got["list"], folders, want)
	}
}
