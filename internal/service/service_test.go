package service_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/v2/internal/config"
	"github.com/mmedum/google-drive-mcp/v2/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v2/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v2/internal/render"
	"github.com/mmedum/google-drive-mcp/v2/internal/service"
)

var testNow = time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)

// setup builds a service over a fake Drive holding the shared synthetic
// tree (see drivetest.SmallTree).
// setup is the fake, the synthetic tree and a service wired to them,
// with the clock fixed so nothing depends on when a run happened.
//
// It takes a testing.TB rather than a *testing.T so the benchmarks use
// the same wiring: a benchmark with its own copy of this is a second
// fixture that drifts from the one every test uses, and drivetest.Client
// was widened for the same reason.
func setup(t testing.TB, o service.Options) (*service.Service, *drivetest.Server) {
	t.Helper()
	fake := drivetest.New()
	t.Cleanup(fake.Close)
	drivetest.SmallTree(fake)
	if o.Now == nil {
		o.Now = func() time.Time { return testNow }
	}
	return service.New(drivetest.Client(t, fake), o), fake
}

// yes is a context whose asking writes are put to a person who
// accepts every question, for the tests of what a write does rather
// than of asking.
func yes(t testing.TB) context.Context {
	return service.WithAsker(t.Context(), accepting{})
}

type accepting struct{}

func (accepting) Ask(context.Context, render.Question) error { return nil }
func (accepting) Asks() bool                                 { return true }
func (accepting) Shows() bool                                { return true }

// hitCount reads the count out of a rendered listing head without
// keeping any of the names in it. The integration tests need the same
// thing, and had their own copy with its own hand-rolled digit loop:
// one that returned 0 for anything unparseable, where this one says so.
func hitCount(t *testing.T, out string) int {
	t.Helper()
	head := strings.SplitN(out, "\n", 2)[0]
	fields := strings.Fields(head)
	for i, f := range fields {
		if (f != "hits" && f != "hit") || i == 0 {
			continue
		}
		n, err := strconv.Atoi(fields[i-1])
		if err != nil {
			t.Fatalf("the listing head does not carry a count: %q", head)
		}
		return n
	}
	t.Fatalf("the listing head does not say how many hits there were: %q", head)
	return 0
}

func TestResolveByID(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	res, err := svc.Resolve(context.Background(), "id-budget-fixture", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.File.Name != "Budget.xlsx" {
		t.Errorf("resolved to %q", res.File.Name)
	}
}

func TestResolveByPath(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	res, err := svc.Resolve(context.Background(), "/Projects/2026/Budget.xlsx", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.File.ID != "id-budget-fixture" {
		t.Errorf("resolved to %q", res.File.ID)
	}
	// A path of depth d costs d listings, then it is cached.
	before := fake.Count("/files")
	if _, err := svc.Resolve(context.Background(), "/Projects/2026/Budget.xlsx", service.ResolveOptions{}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if after := fake.Count("/files") - before; after > 1 {
		t.Errorf("a repeated path cost %d calls; the cache should have covered the walk", after)
	}
}

func TestResolveRoot(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	for _, in := range []string{"root", "my_drive", "https://drive.google.com/drive/my-drive"} {
		res, err := svc.Resolve(context.Background(), in, service.ResolveOptions{})
		if err != nil {
			t.Fatalf("Resolve(%q): %v", in, err)
		}
		if res.File.Name != "My Drive" {
			t.Errorf("Resolve(%q) = %q", in, res.File.Name)
		}
	}
}

func TestResolveAmbiguousPathListsCandidates(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	// Drive allows two files of one name side by side. This server never
	// takes the first match.
	fake.AddFile("id-budget-2-fixture", "Budget.xlsx", "application/pdf", "id-2026-fixture")

	_, err := svc.Resolve(context.Background(), "/Projects/2026/Budget.xlsx", service.ResolveOptions{})
	if err == nil {
		t.Fatal("an ambiguous path should be refused")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "[ambiguous]") {
		t.Errorf("class = %q", msg)
	}
	for _, want := range []string{"id-budget-fixture", "id-budget-2-fixture", "Excel spreadsheet", "PDF"} {
		if !strings.Contains(msg, want) {
			t.Errorf("candidates missing %q: %s", want, msg)
		}
	}
}

func TestResolveMissingPathSaysWhatToDo(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.Resolve(context.Background(), "/Projects/2026/Nope.xlsx", service.ResolveOptions{})
	if err == nil || !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Fatalf("err = %v, want not_found", err)
	}
	// The one listing this spends names the siblings, which is what the
	// next call needs; a bare count would only prompt another lookup.
	for _, want := range []string{"nothing named", "search_files matches word beginnings",
		"That folder holds", "Budget.xlsx", "Meeting notes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %q: %v", want, err)
		}
	}
}

func TestMissingPathInABigFolderOffersListFolder(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	for i := range 20 {
		fake.AddFile("id-crowd-fixture-"+string(rune('a'+i)), "Crowd "+string(rune('A'+i)), "text/plain", "id-2026-fixture")
	}
	_, err := svc.Resolve(context.Background(), "/Projects/2026/Nope.xlsx", service.ResolveOptions{})
	if err == nil {
		t.Fatal("want a not-found")
	}
	if !strings.Contains(err.Error(), "and more (list_folder shows them all)") {
		t.Errorf("a long folder should stop naming and point at list_folder: %v", err)
	}
}

func TestAMistypedIDIsNotRetriedAsAName(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Requested()
	// A stale or mistyped id is the commonest bad input; retrying it as a
	// name would cost two listings to reach the same answer.
	_, err := svc.Resolve(context.Background(), "1AbC23dEfGh45iJkLmN67opQrStUvWxYz", service.ResolveOptions{})
	if err == nil || !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Fatalf("err = %v, want not_found", err)
	}
	for _, r := range fake.Requested() {
		if r.Path == "/drive/v3/files" {
			t.Error("a mistyped id should not have cost a listing")
		}
	}
}

func TestResolveMissingPathInAnEmptyFolder(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFolder("id-empty-folder-fixture", "Empty", "id-projects-fixture")
	_, err := svc.Resolve(context.Background(), "/Projects/Empty/anything", service.ResolveOptions{})
	if err == nil || !strings.Contains(err.Error(), "That folder is empty") {
		t.Fatalf("err = %v, want the empty-folder hint", err)
	}
}

func TestResolveSkipsTrashedItemsOnAPath(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	if _, err := svc.Resolve(context.Background(), "/Projects/Archive/Old plan", service.ResolveOptions{}); err == nil {
		t.Fatal("a trashed item should not be reachable by path")
	}
	// It is still reachable by id.
	res, err := svc.Resolve(context.Background(), "id-old-plan-fixture", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve by id: %v", err)
	}
	if !res.File.Trashed {
		t.Error("the trashed file should still be readable by id")
	}
}

func TestResolveFollowsAShortcutOnlyWhenAsked(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	plain, err := svc.Resolve(context.Background(), "id-shortcut-fixture", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plain.File.ID != "id-shortcut-fixture" || plain.FollowedShortcut != "" {
		t.Errorf("without FollowShortcut the shortcut itself is the target: %+v", plain)
	}
	followed, err := svc.Resolve(context.Background(), "id-shortcut-fixture", service.ResolveOptions{FollowShortcut: true})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if followed.File.ID != "id-budget-fixture" || followed.FollowedShortcut != "id-shortcut-fixture" {
		t.Errorf("with FollowShortcut the target is returned and named: %+v", followed)
	}
}

func TestResolveFollowsAShortcutInTheMiddleOfAPath(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFolder("id-real-folder-fixture", "Real", "id-2026-fixture")
	fake.AddFile("id-inside-txt-fixture", "Inside.txt", "text/plain", "id-real-folder-fixture")
	fake.AddShortcut("id-folder-shortcut", "Link", "id-projects-fixture", "id-real-folder-fixture")

	res, err := svc.Resolve(context.Background(), "/Projects/Link/Inside.txt", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.File.ID != "id-inside-txt-fixture" {
		t.Errorf("resolved to %q", res.File.ID)
	}
}

func TestResolveSharedDrivePath(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	res, err := svc.Resolve(context.Background(), "drive:Marketing/Campaigns/Q3 plan", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.File.ID != "id-q3-plan-fixture" {
		t.Errorf("resolved to %q", res.File.ID)
	}
	if res.DriveName != "Marketing" {
		t.Errorf("drive name = %q", res.DriveName)
	}
}

func TestResolveSharedDriveRoot(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	res, err := svc.Resolve(context.Background(), "drive:Marketing", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.File.ID != "id-drive-marketing" {
		t.Errorf("a shared drive's root folder id is the drive id, got %q", res.File.ID)
	}
}

func TestResolveUnknownDriveListsTheOnesThatExist(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.Resolve(context.Background(), "drive:Nonexistent/x", service.ResolveOptions{})
	if err == nil || !strings.Contains(err.Error(), "Marketing") {
		t.Fatalf("err = %v, want the visible drives listed", err)
	}
}

func TestResolveAmbiguousDriveName(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddDrive("id-drive-marketing-2", "Marketing")
	_, err := svc.Resolve(context.Background(), "drive:Marketing", service.ResolveOptions{})
	if err == nil || !strings.HasPrefix(err.Error(), "[ambiguous]") {
		t.Fatalf("err = %v, want ambiguous", err)
	}
	if !strings.Contains(err.Error(), "id-drive-marketing-2") {
		t.Errorf("both drive ids should be offered: %v", err)
	}
}

func TestResolveRejectsAnUnreadableReference(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.Resolve(context.Background(), "https://example.com/nope", service.ResolveOptions{})
	if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Fatalf("err = %v, want invalid", err)
	}
}

func TestResolveMissingIDExplainsResourceKeys(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.Resolve(context.Background(), "id-does-not-exist", service.ResolveOptions{})
	if err == nil || !strings.HasPrefix(err.Error(), "[not_found]") {
		t.Fatalf("err = %v, want not_found", err)
	}
	if !strings.Contains(err.Error(), "resourcekey") {
		t.Errorf("the message should mention resource keys: %v", err)
	}
}

func TestResolveSendsAResourceKeyFromAURL(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-linked-pdf-fixture", "Linked.pdf", "application/pdf", "id-projects-fixture", drivetest.WithResourceKey("key-abc"))
	fake.Requested()
	_, err := svc.Resolve(context.Background(),
		"https://drive.google.com/file/d/id-linked-pdf-fixture/view?resourcekey=key-abc", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	reqs := fake.Requested()
	if len(reqs) == 0 || reqs[0].ResourceKeys != "id-linked-pdf-fixture/key-abc" {
		t.Errorf("the resource key from the URL did not ride along: %+v", reqs)
	}
}

func TestLocation(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	res, err := svc.Resolve(context.Background(), "id-budget-fixture", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := svc.Location(context.Background(), res.File).String(); got != "My Drive/Projects/2026" {
		t.Errorf("location = %q", got)
	}
}

func TestLocationInASharedDrive(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	res, err := svc.Resolve(context.Background(), "id-q3-plan-fixture", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := svc.Location(context.Background(), res.File).String(); got != "Marketing (shared drive)/Campaigns" {
		t.Errorf("location = %q", got)
	}
}

func TestLocationOfAFileSharedWithMe(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	shared := fake.AddFile("id-theirs-fixture", "Their file", gdrive.MimeDocument, "",
		drivetest.Owner("Other Person", "other@example.com"))
	shared.SharedWithMeTime = "2026-03-01T00:00:00Z"
	res, err := svc.Resolve(context.Background(), "id-theirs-fixture", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := svc.Location(context.Background(), res.File).String(); got != "Shared with me" {
		t.Errorf("location = %q", got)
	}
}

func TestGetFileCard(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.GetFile(context.Background(), service.GetFileInput{File: "/Projects/2026/Budget.xlsx"})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	for _, want := range []string{
		"Budget.xlsx — Excel spreadsheet", "id: id-budget-fixture",
		"location: My Drive/Projects/2026", "size: 4.0 KiB", "you can:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("card missing %q:\n%s", want, out)
		}
	}
}

func TestGetFileOnAGoogleDocSaysWhereItsContentIsEdited(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.GetFile(context.Background(), service.GetFileInput{File: "id-notes-fixture"})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if !strings.Contains(out, "Docs API, which this server does not offer") {
		t.Errorf("the file boundary should be stated:\n%s", out)
	}
	if !strings.Contains(out, "export formats:") {
		t.Errorf("a Workspace document should list its export formats:\n%s", out)
	}
}

func TestGetFileOnAShortcutDescribesTheShortcut(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.GetFile(context.Background(), service.GetFileInput{File: "id-shortcut-fixture"})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if !strings.Contains(out, "points at: id-budget-fixture") {
		t.Errorf("the shortcut's target should be named:\n%s", out)
	}
	if !strings.Contains(out, "act on the shortcut itself") {
		t.Errorf("the shortcut rule should be stated:\n%s", out)
	}
}

func TestGetFileReadsPermissionsForASharedDriveItem(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-q3-plan-fixture", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: "a@example.com", DisplayName: "A Person"})
	out, err := svc.GetFile(context.Background(), service.GetFileInput{File: "id-q3-plan-fixture"})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if !strings.Contains(out, "1 can edit") {
		t.Errorf("shared-drive permissions were not read:\n%s", out)
	}
}

func TestListFolderPage(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.ListFolder(context.Background(), service.ListFolderInput{Folder: "/Projects/2026"})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	if !strings.Contains(out, "My Drive/Projects/2026 — 2 items, folders first") {
		t.Errorf("title wrong:\n%s", out)
	}
	if !strings.Contains(out, "Budget.xlsx") || !strings.Contains(out, "Meeting notes") {
		t.Errorf("children missing:\n%s", out)
	}
	if strings.Contains(out, "Old plan") {
		t.Errorf("a trashed file should not be listed:\n%s", out)
	}
}

func TestListFolderExcludesTrashUnlessAsked(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.ListFolder(context.Background(), service.ListFolderInput{Folder: "/Projects/Archive", IncludeTrashed: true})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	if !strings.Contains(out, "Old plan") || !strings.Contains(out, "trashed") {
		t.Errorf("trashed items should appear when asked for:\n%s", out)
	}
}

func TestListFolderFiltersByKind(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.ListFolder(context.Background(), service.ListFolderInput{Folder: "id-2026-fixture", Kind: "doc"})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	if strings.Contains(out, "Budget.xlsx") {
		t.Errorf("kind filter did not apply:\n%s", out)
	}
	if !strings.Contains(out, "Meeting notes") {
		t.Errorf("the matching file is missing:\n%s", out)
	}
}

func TestListFolderRejectsANonFolder(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.ListFolder(context.Background(), service.ListFolderInput{Folder: "id-budget-fixture"})
	if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Fatalf("err = %v, want invalid", err)
	}
	if !strings.Contains(err.Error(), "get_file describes it") {
		t.Errorf("the error should say what to do instead: %v", err)
	}
}

func TestListFolderRejectsAnUnknownKind(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.ListFolder(context.Background(), service.ListFolderInput{Folder: "id-2026-fixture", Kind: "spreadsheets"})
	if err == nil || !strings.HasPrefix(err.Error(), "[invalid]") {
		t.Fatalf("err = %v, want invalid", err)
	}
}

func TestListFolderDefaultsToMyDriveRoot(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.ListFolder(context.Background(), service.ListFolderInput{})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	if !strings.Contains(out, "Projects") {
		t.Errorf("the root listing should show Projects:\n%s", out)
	}
}

func TestListFolderPaging(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	for i := range 5 {
		fake.AddFile("id-many-fixture-"+string(rune('a'+i)), "Many "+string(rune('A'+i)), "text/plain", "id-2026-fixture")
	}
	out, err := svc.ListFolder(context.Background(), service.ListFolderInput{Folder: "id-2026-fixture", PageSize: 3})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	if !strings.Contains(out, "page_token") {
		t.Errorf("a truncated listing must offer the continuation:\n%s", out)
	}
}

// A folder this account can see but not list still answers a listing,
// so an empty page must not read as an empty folder.
func TestListFolderSaysWhenItCannotListAnEmptyPage(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFolder("id-closed-fixture", "Closed", fake.RootID, drivetest.WithCapabilities(gdrive.Capabilities{}))
	out, err := svc.ListFolder(t.Context(), service.ListFolderInput{Folder: "id-closed-fixture"})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	want := "My Drive/Closed — 0 items, folders first\n" +
		"this account can see this folder but cannot list what is in it, so it may not be empty. " +
		"Ask its owner for access.\n"
	if out != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}

func TestListFolderSaysWhenItCannotListTheItemsShown(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFolder("id-closed-fixture", "Closed", fake.RootID, drivetest.WithCapabilities(gdrive.Capabilities{}))
	fake.AddFile("id-shown-fixture", "Shown", gdrive.MimeDocument, "id-closed-fixture")
	out, err := svc.ListFolder(t.Context(), service.ListFolderInput{Folder: "id-closed-fixture"})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	if !strings.Contains(out, "Shown") ||
		!strings.HasSuffix(out, "\nthis account cannot list this folder, so these may not be all of its items.\n") {
		t.Errorf("the page does not say its items may not be all:\n%s", out)
	}
}

// A walk reads the capability of every folder it enters, and names the
// ones it could not list.
func TestTreeNamesTheFoldersItCannotList(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFolder("id-closed-fixture", "Closed", "id-projects-fixture", drivetest.WithCapabilities(gdrive.Capabilities{}))
	out, err := svc.ListFolder(t.Context(), service.ListFolderInput{Folder: "/Projects", Recursive: true})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	for _, want := range []string{
		"Closed/  [id-closed-fixture]  (0 items, this account cannot list it)",
		"\nthis account cannot list 1 folder: Closed. They may hold more than is shown.\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the tree does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "2026/  [id-2026-fixture]  (2 items, this account cannot list it)") {
		t.Errorf("a folder this account can list is marked:\n%s", out)
	}
}

func TestListFolderTree(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.ListFolder(context.Background(), service.ListFolderInput{Folder: "/Projects", Recursive: true})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	for _, want := range []string{"tree, depth 3", "├──", "2026/", "Budget.xlsx"} {
		if !strings.Contains(out, want) {
			t.Errorf("tree missing %q:\n%s", want, out)
		}
	}
}

func TestTreeSaysWhereItStopped(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.ListFolder(context.Background(), service.ListFolderInput{Folder: "/Projects", Recursive: true, MaxDepth: 1})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	if !strings.Contains(out, "not entered: depth limit 1") {
		t.Errorf("a folder that was not entered must say so:\n%s", out)
	}
	if !strings.Contains(out, "stopped short in") {
		t.Errorf("the walk should report where it stopped:\n%s", out)
	}
}

func TestTreeHonorsTheItemBudget(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	for i := range 10 {
		fake.AddFile("id-bulk-fixture-"+string(rune('a'+i)), "Bulk "+string(rune('A'+i)), "text/plain", "id-2026-fixture")
	}
	out, err := svc.ListFolder(context.Background(), service.ListFolderInput{Folder: "/Projects", Recursive: true, MaxItems: 4})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	if !strings.Contains(out, "item limit 4") {
		t.Errorf("the item budget should be named where it stopped:\n%s", out)
	}
}

func TestSearchByName(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.Search(context.Background(), service.SearchInput{Name: "Budget"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(out, "Budget.xlsx") {
		t.Errorf("search missed the file:\n%s", out)
	}
	if !strings.Contains(out, "My Drive/Projects/2026") {
		t.Errorf("a hit must say where it lives:\n%s", out)
	}
}

// Google's front end refuses a search it finds too long with an HTML
// page, before Drive reads it. The page never reaches the error; the
// error says what the status means, and that the query was too long
// when that is what it most likely was.
func TestASearchGoogleRefusesWithAPageSaysWhyAndQuotesNoPage(t *testing.T) {
	page := "<!DOCTYPE html>\n<html lang=en><title>Error 400 (Bad Request)</title></html>"
	long := strings.Repeat("'id-root-my-drive' in parents or ", 400) + "'id-root-my-drive' in parents"
	for _, tc := range []struct {
		name   string
		status int
		raw    string
		want   string
	}{
		{"a long query answered 400 with a page", http.StatusBadRequest, long,
			"[invalid] Google refused the search before Drive read it, most likely as too long: its query is "},
		{"a query answered 414", http.StatusRequestURITooLong, long,
			"[invalid] Google refused the search as too long (HTTP 414): its query is "},
		{"a short query answered 400 with a page", http.StatusBadRequest, "name = 'x'",
			"[invalid] searching Drive failed: HTTP 400 Bad Request, answered with an error page rather than a Drive error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake := setup(t, service.Options{})
			fake.Fail = drivetest.FailTimes(1, "/files", drivetest.Failure{Status: tc.status, Page: page})
			_, err := svc.Search(t.Context(), service.SearchInput{RawQuery: tc.raw})
			if err == nil || !strings.HasPrefix(err.Error(), tc.want) || strings.Contains(err.Error(), "<") {
				t.Errorf("err = %v\nwant it to start %q and quote no page", err, tc.want)
			}
		})
	}
}

func TestSearchNameIsAWordPrefixNotASubstring(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.Search(context.Background(), service.SearchInput{Name: "udget"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if strings.Contains(out, "Budget.xlsx") {
		t.Errorf("`contains` is a word-prefix match, not a substring:\n%s", out)
	}
	if !strings.Contains(out, "matches the beginnings of words") {
		t.Errorf("an empty result should explain the semantics:\n%s", out)
	}
}

func TestSearchFindsSharedDriveFiles(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.Search(context.Background(), service.SearchInput{Name: "Q3"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(out, "Q3 plan") {
		t.Errorf("shared drives must be searched by default:\n%s", out)
	}
	if !strings.Contains(out, "Marketing (shared drive)") {
		t.Errorf("the hit should say which drive it is in:\n%s", out)
	}
}

func TestSearchInOneDrive(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.Search(context.Background(), service.SearchInput{Kind: "doc", Drive: "Marketing"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if strings.Contains(out, "Meeting notes") {
		t.Errorf("a one-drive search should not reach My Drive:\n%s", out)
	}
	if !strings.Contains(out, "Q3 plan") {
		t.Errorf("the drive's own file is missing:\n%s", out)
	}
}

func TestSearchInFolder(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.Search(context.Background(), service.SearchInput{Kind: "doc", InFolder: "/Projects/2026"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(out, "Meeting notes") || strings.Contains(out, "Q3 plan") {
		t.Errorf("in_folder should limit to direct children:\n%s", out)
	}
}

func TestSearchInFolderRejectsANonFolder(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.Search(context.Background(), service.SearchInput{Name: "x", InFolder: "id-budget-fixture"})
	if err == nil || !strings.Contains(err.Error(), "must name a folder") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchExcludesTrashByDefault(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.Search(context.Background(), service.SearchInput{Name: "Old"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if strings.Contains(out, "Old plan") {
		t.Errorf("trashed files should not appear in a plain search:\n%s", out)
	}
	out, err = svc.Search(context.Background(), service.SearchInput{Name: "Old", Trashed: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(out, "Old plan") {
		t.Errorf("trashed: true should find it:\n%s", out)
	}
}

// A search with nothing to narrow it is every live file this account
// can see, in the order asked for, and its title says so.
func TestASearchWithNoFilterIsEverythingYouCanSee(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.Search(t.Context(), service.SearchInput{OrderBy: "name"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.HasPrefix(out, "search: everything you can see, by name — ") {
		t.Errorf("the title does not say what was searched:\n%s", out)
	}
	for _, want := range []string{"Budget.xlsx", "Meeting notes", "Q3 plan"} {
		if !strings.Contains(out, want) {
			t.Errorf("%s is missing:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Old plan") {
		t.Errorf("a trashed file is in a search that did not ask for the trash:\n%s", out)
	}
	if out, _ := svc.Search(t.Context(), service.SearchInput{}); !strings.HasPrefix(out, "search: everything you can see, by modified — ") {
		t.Errorf("the default order is not named:\n%s", out)
	}
}

// order_by shared puts the files most recently shared with this account
// first, and with no scope it is a search of those files alone.
func TestOrderBySharedImpliesSharedWithMe(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-first-share-fixture", "Older share", gdrive.MimeDocument, "",
		drivetest.Owner("Jane Doe", "jane@example.com"), drivetest.SharedWithMe("2026-02-01T09:00:00Z", "Jane Doe", "jane@example.com"))
	fake.AddFile("id-second-share-fixture", "Newer share", gdrive.MimeDocument, "",
		drivetest.Owner("John Doe", "john@example.com"), drivetest.SharedWithMe("2026-03-01T09:00:00Z", "John Doe", "john@example.com"))
	out, err := svc.Search(t.Context(), service.SearchInput{OrderBy: "shared"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	lines := strings.Split(out, "\n")
	if lines[0] != "search: shared with me (order_by shared implies it) — 2 hits" {
		t.Errorf("title = %q", lines[0])
	}
	if len(lines) < 3 || !strings.Contains(lines[1], "Newer share") || !strings.Contains(lines[2], "Older share") {
		t.Fatalf("the newest share is not first:\n%s", out)
	}
	if !strings.HasSuffix(lines[1], "; shared with you 2026-03-01 09:00Z (5 days ago) by John Doe <john@example.com>") {
		t.Errorf("the row does not say who shared it and when: %q", lines[1])
	}
	if !strings.Contains(lines[1], "Shared with me") {
		t.Errorf("a file shared with this account is not placed under Shared with me: %q", lines[1])
	}
}

func TestOrderBySharedRefusesAnotherScope(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.Search(t.Context(), service.SearchInput{OrderBy: "shared", Scope: "my_drive"})
	want := `[invalid] order_by shared orders the files shared with you by when they were shared, so it takes ` +
		`scope shared_with_me or no scope, not "my_drive"`
	if err == nil || err.Error() != want {
		t.Errorf("err = %v\nwant %s", err, want)
	}
}

func TestFileCardSaysWhoSharedItWithYou(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-theirs-fixture", "Their plan", gdrive.MimeDocument, "",
		drivetest.Owner("Jane Doe", "jane@example.com"), drivetest.SharedWithMe("2026-03-01T09:00:00Z", "Jane Doe", "jane@example.com"))
	out, err := svc.GetFile(t.Context(), service.GetFileInput{File: "id-theirs-fixture"})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if !strings.Contains(out, "\nshared with you: 2026-03-01 09:00Z (5 days ago) by Jane Doe <jane@example.com>\n") {
		t.Errorf("the card does not say who shared it and when:\n%s", out)
	}
	own, err := svc.GetFile(t.Context(), service.GetFileInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if strings.Contains(own, "shared with you") {
		t.Errorf("a file nobody shared with this account says it was:\n%s", own)
	}
}

// A file this account shared with itself, as a shared drive's file can
// be, names the account as "you" on the card and in a search row.
func TestTheAccountIsMarkedWhenItSharedTheFile(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-self-shared-fixture", "Self shared", gdrive.MimeDocument, "",
		drivetest.SharedWithMe("2026-03-01T09:00:00Z", drivetest.AccountName, drivetest.AccountEmail))
	fake.Files["id-self-shared-fixture"].SharingUser.Me = true

	card, err := svc.GetFile(t.Context(), service.GetFileInput{File: "id-self-shared-fixture"})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	row, err := svc.Search(t.Context(), service.SearchInput{OrderBy: "shared"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, out := range []string{card, row} {
		if !strings.Contains(out, "2026-03-01 09:00Z (5 days ago) by Test Person (you)") {
			t.Errorf("the account is not marked as the one who shared it:\n%s", out)
		}
	}
}

// A shared drive records who trashed an item; when it was this account,
// the card says "you".
func TestTheAccountIsMarkedWhenItTrashedTheFile(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	if _, err := svc.TrashFile(t.Context(), service.TrashInput{File: "id-q3-plan-fixture"}); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	card, err := svc.GetFile(t.Context(), service.GetFileInput{File: "id-q3-plan-fixture"})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if !strings.Contains(card, "\ntrashed: yes, by Test Person (you) on ") {
		t.Errorf("the account is not marked as the one who trashed it:\n%s", card)
	}
}

// visibilityTree adds one file per kind of reach, each in its own way
// open to people it does not name.
func visibilityTree(fake *drivetest.Server) {
	for _, f := range []struct {
		id, name string
		grant    *gdrive.Permission
	}{
		{"id-by-link-fixture", "By link", &gdrive.Permission{Type: "anyone", Role: "reader"}},
		{"id-findable-fixture", "Findable", &gdrive.Permission{Type: "anyone", Role: "reader", AllowFileDiscovery: true}},
		{"id-company-fixture", "Company", &gdrive.Permission{Type: "domain", Role: "reader", Domain: "example.com"}},
		{"id-named-fixture", "Named", &gdrive.Permission{Type: "user", Role: "reader", EmailAddress: "jane@example.com"}},
	} {
		fake.AddFile(f.id, f.name, gdrive.MimeDocument, "id-2026-fixture")
		fake.Grant(f.id, f.grant)
	}
}

func TestSearchByVisibility(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	visibilityTree(fake)
	for _, c := range []struct {
		visibility, title string
		want              []string
	}{
		{"anyone", "search: kind doc, open to anyone on the internet — 2 hits", []string{"By link", "Findable"}},
		{"link", "search: kind doc, open to anyone with the link — 1 hit", []string{"By link"}},
		{"domain", "search: kind doc, open to everyone in the organization — 1 hit", []string{"Company"}},
		{"limited", "search: kind doc, open only to the people and groups it is shared with — 3 hits",
			[]string{"Meeting notes", "Named", "Q3 plan"}},
	} {
		out, err := svc.Search(t.Context(), service.SearchInput{Kind: "doc", Visibility: c.visibility, OrderBy: "name"})
		if err != nil {
			t.Fatalf("%s: %v", c.visibility, err)
		}
		// The title carries the count, so the names below are all the hits.
		if title, _, _ := strings.Cut(out, "\n"); title != c.title {
			t.Errorf("%s: title = %q, want %q", c.visibility, title, c.title)
		}
		for _, name := range c.want {
			if !strings.Contains(out, name) {
				t.Errorf("%s: %s is missing:\n%s", c.visibility, name, out)
			}
		}
	}
}

// shared_with finds a file whether the address may view, comment or
// edit it. The fake reads readers narrowly, so a query that asked
// readers alone would miss the editor here.
func TestSearchSharedWithFindsViewersCommentersAndEditors(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	for _, f := range []struct{ id, name, role, address string }{
		{"id-viewer-fixture", "Viewer", "reader", "jane@example.com"},
		{"id-commenter-fixture", "Commenter", "commenter", "jane@example.com"},
		{"id-editor-fixture", "Editor", "writer", "jane@example.com"},
		{"id-someone-else-fixture", "Someone else", "writer", "john@example.com"},
	} {
		fake.AddFile(f.id, f.name, gdrive.MimeDocument, "id-2026-fixture")
		fake.Grant(f.id, &gdrive.Permission{Type: "user", Role: f.role, EmailAddress: f.address})
	}
	out, err := svc.Search(t.Context(), service.SearchInput{SharedWith: "jane@example.com", OrderBy: "name"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.HasPrefix(out, "search: shared with jane@example.com — 3 hits\n") {
		t.Errorf("title wrong:\n%s", out)
	}
	for _, want := range []string{"Viewer", "Commenter", "Editor"} {
		if !strings.Contains(out, want) {
			t.Errorf("%s is missing:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Someone else") {
		t.Errorf("a file shared with another address is a hit:\n%s", out)
	}
}

func TestSearchRefusesAVisibilityOrAddressItCannotRead(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	for _, c := range []struct {
		in   service.SearchInput
		want string
	}{
		{service.SearchInput{Visibility: "public"}, `[invalid] visibility "public" is not one of anyone, domain, limited, link`},
		{service.SearchInput{SharedWith: "jane"}, `[invalid] shared_with takes one address of a person or group, ` +
			`like someone@example.com, not "jane"`},
		{service.SearchInput{SharedWith: "jane@example.com john@example.com"}, `[invalid] shared_with takes one ` +
			`address of a person or group, like someone@example.com, not "jane@example.com john@example.com"`},
	} {
		_, err := svc.Search(t.Context(), c.in)
		if err == nil || err.Error() != c.want {
			t.Errorf("%+v: err = %v\nwant %s", c.in, err, c.want)
		}
	}
}

func TestSearchRejectsBadEnums(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	cases := []service.SearchInput{
		{Name: "x", Kind: "spreadsheet"},
		{Name: "x", Scope: "everything"},
		{Name: "x", OrderBy: "relevance"},
		{Name: "x", ModifiedAfter: "last tuesday"},
	}
	for _, in := range cases {
		if _, err := svc.Search(context.Background(), in); err == nil {
			t.Errorf("%+v should be refused", in)
		}
	}
}

func TestSearchTimeFilters(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.Search(context.Background(), service.SearchInput{Kind: "doc", ModifiedAfter: "2026-01-01"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(out, "Meeting notes") {
		t.Errorf("a date filter dropped a matching file:\n%s", out)
	}
	out, err = svc.Search(context.Background(), service.SearchInput{Kind: "doc", ModifiedBefore: "2020-01-01"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if strings.Contains(out, "Meeting notes") {
		t.Errorf("a date filter should have excluded everything:\n%s", out)
	}
}

func TestSearchFullText(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Content["id-notes-fixture"] = "the quarterly revenue numbers"
	out, err := svc.Search(context.Background(), service.SearchInput{Text: "quarterly"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(out, "Meeting notes") {
		t.Errorf("full-text search missed the file:\n%s", out)
	}
}

func TestSearchScopeSharedWithMe(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	shared := fake.AddFile("id-theirs-fixture", "Their plan", gdrive.MimeDocument, "",
		drivetest.Owner("Other Person", "other@example.com"))
	shared.SharedWithMeTime = "2026-03-01T00:00:00Z"
	out, err := svc.Search(context.Background(), service.SearchInput{Kind: "doc", Scope: "shared_with_me"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(out, "Their plan") {
		t.Errorf("shared-with-me scope missed the file:\n%s", out)
	}
	if strings.Contains(out, "Meeting notes") {
		t.Errorf("own files should not appear in shared-with-me:\n%s", out)
	}
}

func TestGetAccount(t *testing.T) {
	svc, _ := setup(t, service.Options{Sharing: config.SharingAll, LocalDir: "/tmp/drive", MaxDownload: 1 << 30})
	out, err := svc.GetAccount(context.Background())
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	for _, want := range []string{"person@example.com", "storage:", "Marketing", "sharing", "file transfer"} {
		if !strings.Contains(out, want) {
			t.Errorf("account output missing %q:\n%s", want, out)
		}
	}
}

func TestGetAccountReportsTheConfiguredLimits(t *testing.T) {
	svc, _ := setup(t, service.Options{ReadOnly: true, Sharing: config.SharingOff})
	out, err := svc.GetAccount(context.Background())
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if !strings.Contains(out, "read-only") || !strings.Contains(out, "GDRIVE_SHARING=off") {
		t.Errorf("the settings in force should be reported:\n%s", out)
	}
	if !strings.Contains(out, "file transfer: off") {
		t.Errorf("an unset local dir means no transfers, and that should be said:\n%s", out)
	}
}

func TestDrives(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	drives, err := svc.Drives(context.Background())
	if err != nil {
		t.Fatalf("Drives: %v", err)
	}
	if len(drives) != 1 || drives[0].Name != "Marketing" {
		t.Errorf("drives = %+v", drives)
	}
}

func TestOptionsRoundTrip(t *testing.T) {
	svc, _ := setup(t, service.Options{ReadOnly: true, Destructive: true, Labels: true})
	o := svc.Options()
	if !o.ReadOnly || !o.Destructive || !o.Labels {
		t.Errorf("options lost: %+v", o)
	}
}

func TestIDShapedWordThatNamesNoFileIsTriedAsAName(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	// A word in the id alphabet, long enough to be an id, that is really
	// a file name sitting in My Drive.
	fake.AddFile("id-quarterlyreview-fixture", "QuarterlyReviewNotes", gdrive.MimeDocument, fake.RootID)
	res, err := svc.Resolve(context.Background(), "QuarterlyReviewNotes", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.File.ID != "id-quarterlyreview-fixture" {
		t.Errorf("resolved to %q", res.File.ID)
	}
}

func TestIDFromAURLIsNeverTriedAsAName(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-someothername-fixture", "SomeOtherNameThatIsLong", gdrive.MimeDocument, fake.RootID)
	_, err := svc.Resolve(context.Background(),
		"https://drive.google.com/file/d/SomeOtherNameThatIsLong/view", service.ResolveOptions{})
	if err == nil {
		t.Fatal("an id lifted from a URL is an id; it must not fall back to a name")
	}
}

func TestDeepLocationSaysWhatIsAboveRatherThanInventingIt(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	// A chain longer than the walk will climb.
	parent := "id-projects-fixture"
	for i := range 30 {
		id := "id-deep-fixture-" + string(rune('a'+i))
		fake.AddFolder(id, "Level"+string(rune('A'+i)), parent)
		parent = id
	}
	fake.AddFile("id-deepfile-fixture", "Deep.txt", "text/plain", parent)
	res, err := svc.Resolve(context.Background(), "id-deepfile-fixture", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := svc.Location(context.Background(), res.File).String()
	if !strings.HasPrefix(got, "My Drive/…/") {
		t.Errorf("a path with unread ancestors must put the gap where it is, got %q", got)
	}
	if strings.HasSuffix(got, "/…") {
		t.Errorf("the gap is above the shown folders, not below them: %q", got)
	}
}

func TestMyDriveRootKnowsWhereItIs(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	// Drive answers to the alias "root" but reports the real id, so the
	// two have to be tied together. Without that the root looks like a
	// file that lost its parent.
	res, err := svc.Resolve(context.Background(), "root", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := svc.Location(context.Background(), res.File).String(); got != "My Drive" {
		t.Errorf("the root's own location = %q, want My Drive", got)
	}
	out, err := svc.ListFolder(context.Background(), service.ListFolderInput{Folder: "root"})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	if !strings.HasPrefix(out, "My Drive — ") {
		t.Errorf("listing the root is headed %q", strings.SplitN(out, "\n", 2)[0])
	}
}

func TestAFileWithNoParentIsStillReportedAsOrphaned(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-orphan-fixture", "Orphan.txt", "text/plain", "")
	res, err := svc.Resolve(context.Background(), "id-orphan-fixture", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := svc.Location(context.Background(), res.File).String(); !strings.Contains(got, "no folder") {
		t.Errorf("an orphan should say so, got %q", got)
	}
}

func TestSharedDriveRootKnowsWhereItIs(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.ListFolder(context.Background(), service.ListFolderInput{Folder: "drive:Marketing"})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	if !strings.HasPrefix(out, "Marketing (shared drive) — ") {
		t.Errorf("listing a shared drive root is headed %q", strings.SplitN(out, "\n", 2)[0])
	}
}

func TestNoLoginSaysSoPlainly(t *testing.T) {
	fake := drivetest.New()
	t.Cleanup(fake.Close)
	drivetest.SmallTree(fake)
	svc := service.New(drivetest.NoCredentials(t, fake), service.Options{Now: func() time.Time { return testNow }})

	_, err := svc.GetFile(context.Background(), service.GetFileInput{File: "id-budget-fixture"})
	if err == nil || !strings.HasPrefix(err.Error(), "[auth]") {
		t.Fatalf("err = %v, want auth", err)
	}
	// "Google refused this login" would be wrong: there is no login to refuse.
	if strings.Contains(err.Error(), "refused") {
		t.Errorf("having no credentials is not a refusal: %v", err)
	}
	if !strings.Contains(err.Error(), "google-drive-mcp login") {
		t.Errorf("the message must say what to run: %v", err)
	}
}

func TestAPathNamingAShortcutResolvesToTheShortcut(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	// The shortcut is what sits in the folder, so it is what the path
	// names. Organizing, sharing and trashing act on it, and following it
	// here would make a later trash_file destroy the target instead.
	res, err := svc.Resolve(context.Background(), "/Projects/Budget shortcut", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.File.ID != "id-shortcut-fixture" {
		t.Errorf("resolved to %q, want the shortcut itself", res.File.ID)
	}
	out, err := svc.GetFile(context.Background(), service.GetFileInput{File: "/Projects/Budget shortcut"})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if !strings.Contains(out, "points at: id-budget-fixture") {
		t.Errorf("the card should describe the shortcut and name its target:\n%s", out)
	}

	// Asking to follow it still works, and says that it did.
	followed, err := svc.Resolve(context.Background(), "/Projects/Budget shortcut", service.ResolveOptions{FollowShortcut: true})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if followed.File.ID != "id-budget-fixture" || followed.FollowedShortcut != "id-shortcut-fixture" {
		t.Errorf("with FollowShortcut: %+v", followed)
	}
}

func TestASharedDriveFileWithNoGrantsIsNotCalledUnknown(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	// A permission list that was read and is empty means the drive's own
	// access reaches it. Reporting "unknown" there hides real exposure.
	out, err := svc.GetFile(context.Background(), service.GetFileInput{File: "id-q3-plan-fixture"})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if strings.Contains(out, "cannot read the permission list") {
		t.Errorf("a readable empty list is not unknown:\n%s", out)
	}
	if !strings.Contains(out, "everyone with access to the shared drive Marketing") {
		t.Errorf("the drive's own reach should be stated:\n%s", out)
	}
}

func TestNamesDifferingOnlyInCaseAreAmbiguous(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	// Drive's `name =` ignores case (observed live, spike A), so two
	// siblings differing only in case both match one lookup. Taking
	// either would be taking the first match.
	fake.AddFile("id-budget-lower-fixture", "budget.xlsx", "text/plain", "id-2026-fixture")
	_, err := svc.Resolve(context.Background(), "/Projects/2026/Budget.xlsx", service.ResolveOptions{})
	if err == nil {
		t.Fatal("two names differing only in case should be ambiguous")
	}
	if !strings.HasPrefix(err.Error(), "[ambiguous]") {
		t.Fatalf("err = %v, want ambiguous", err)
	}
	for _, want := range []string{"id-budget-fixture", "id-budget-lower-fixture"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("both candidates should be offered, missing %s: %v", want, err)
		}
	}
}

func TestAPathResolvesRegardlessOfCase(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	// The other side of the same coin: with no collision, a path typed in
	// the wrong case still finds the one file it can mean.
	res, err := svc.Resolve(context.Background(), "/projects/2026/budget.xlsx", service.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.File.ID != "id-budget-fixture" {
		t.Errorf("resolved to %q", res.File.ID)
	}
}

func TestATreeIsHeadedWithTheFolderItShows(t *testing.T) {
	// A tree headed with its parent's path names something other than
	// what it lists, and the flat listing of the same folder got it
	// right, so the two disagreed.
	svc, _ := setup(t, service.Options{})

	flat, err := svc.ListFolder(context.Background(), service.ListFolderInput{Folder: "/Projects/2026"})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	tree, err := svc.ListFolder(context.Background(), service.ListFolderInput{
		Folder: "/Projects/2026", Recursive: true,
	})
	if err != nil {
		t.Fatalf("ListFolder recursive: %v", err)
	}
	const want = "My Drive/Projects/2026"
	for name, out := range map[string]string{"listing": flat, "tree": tree} {
		head, _, _ := strings.Cut(out, "\n")
		if !strings.HasPrefix(head, want) {
			t.Errorf("the %s is headed %q, want it to start with %q", name, head, want)
		}
	}
}

func TestATreeOfADriveRootIsHeadedWithTheDrive(t *testing.T) {
	// A drive's root contributes no name of its own: it is already the
	// head of the path, and adding it would read as a folder inside
	// itself.
	svc, _ := setup(t, service.Options{})
	out, err := svc.ListFolder(context.Background(), service.ListFolderInput{
		Folder: "root", Recursive: true, MaxDepth: 1,
	})
	if err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	head, _, _ := strings.Cut(out, "\n")
	if !strings.HasPrefix(head, "My Drive — tree") {
		t.Errorf("the tree is headed %q", head)
	}
}
