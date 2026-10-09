package service_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v2/internal/config"
	"github.com/mmedum/google-drive-mcp/v2/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v2/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v2/internal/service"
)

// tree fills the fake with a folder holding a file, a subfolder with two
// files in it, and a shortcut, so a copy has every shape to handle.
func copyTree(fake *drivetest.Server) {
	fake.AddFolder("id-source-folder-fixture", "Source", fake.RootID)
	fake.AddFile("id-source-doc-fixture", "Plan", gdrive.MimeDocument, "id-source-folder-fixture")
	fake.AddFolder("id-source-sub-fixture", "Detail", "id-source-folder-fixture")
	fake.AddFile("id-source-sheet-fixture", "Numbers", gdrive.MimeSheet, "id-source-sub-fixture")
	fake.AddFile("id-source-blob-fixture", "scan.pdf", "application/pdf", "id-source-sub-fixture")
	fake.AddShortcut("id-source-link-fixture", "Budget link", "id-source-folder-fixture", "id-budget-fixture")
	fake.AddFolder("id-destination-fixture", "Destination", fake.RootID)
}

// childrenNamed returns the names of a folder's children in the fake.
func childrenNamed(fake *drivetest.Server, parent string) []string {
	var out []string
	for _, f := range fake.Files {
		if f.Parent() == parent {
			out = append(out, f.Name)
		}
	}
	return out
}

// A subfolder this account cannot list would copy as an empty folder,
// and the copy would look whole, so the walk refuses it before writing.
func TestRecursiveCopyRefusesASubfolderItCannotList(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	copyTree(fake)
	fake.Files["id-source-sub-fixture"].Capabilities = &gdrive.Capabilities{}
	before := len(fake.Files)

	_, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-source-folder-fixture", To: "id-destination-fixture", Recursive: true,
	})
	want := "[forbidden] this account cannot list what is inside Detail, in Source, so it cannot copy Source whole; " +
		"nothing has been copied."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
	if len(fake.Files) != before {
		t.Errorf("the refused copy wrote %d files", len(fake.Files)-before)
	}
}

func TestRecursiveCopyRebuildsTheWholeTree(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	copyTree(fake)

	got, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-source-folder-fixture", To: "id-destination-fixture", Name: "Source copy", Recursive: true,
	})
	if err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if got.JSON.Action != "copied" {
		t.Errorf("action = %q", got.JSON.Action)
	}
	root := got.JSON.File.ID
	if root == "" {
		t.Fatal("the result carries no id for the new folder")
	}
	if parent := fake.Files[root].Parent(); parent != "id-destination-fixture" {
		t.Errorf("the copy landed in %q", parent)
	}
	names := childrenNamed(fake, root)
	if len(names) != 3 {
		t.Errorf("the copy holds %v, want the folder's three children", names)
	}
	// The subfolder is a new folder holding new copies, not a second
	// parent on the originals.
	var sub string
	for _, f := range fake.Files {
		if f.Parent() == root && f.IsFolder() {
			sub = f.ID
		}
	}
	if sub == "" || sub == "id-source-sub-fixture" {
		t.Fatalf("the subfolder was not copied: %q", sub)
	}
	if inner := childrenNamed(fake, sub); len(inner) != 2 {
		t.Errorf("the copied subfolder holds %v, want two files", inner)
	}
	// The originals are untouched, which is the whole promise of a copy.
	if inner := childrenNamed(fake, "id-source-sub-fixture"); len(inner) != 2 {
		t.Errorf("the source subfolder now holds %v", inner)
	}
	if !strings.Contains(got.Text, "copied 5 of 5 items") {
		t.Errorf("the result does not count what it did:\n%s", got.Text)
	}
}

func TestACopiedShortcutPointsWhereTheOriginalPoints(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	copyTree(fake)

	got, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-source-folder-fixture", To: "id-destination-fixture", Recursive: true,
	})
	if err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	var link *gdrive.File
	for _, f := range fake.Files {
		if f.Parent() == got.JSON.File.ID && f.IsShortcut() {
			link = f
		}
	}
	if link == nil {
		t.Fatal("the shortcut was not made again in the copy")
	}
	if link.ShortcutDetails == nil || link.ShortcutDetails.TargetID != "id-budget-fixture" {
		t.Errorf("the new shortcut points at %+v", link.ShortcutDetails)
	}
	if !strings.Contains(got.Text, "pointing where the originals point") {
		t.Errorf("the result does not say where the shortcuts point:\n%s", got.Text)
	}
}

func TestATreeOverTheBudgetIsRefusedBeforeAnythingIsCopied(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	copyTree(fake)
	before := len(fake.Files)

	_, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-source-folder-fixture", To: "id-destination-fixture", Recursive: true, MaxItems: 2,
	})
	if err == nil {
		t.Fatal("a tree over the budget was copied")
	}
	if !strings.Contains(err.Error(), "nothing has been copied") {
		t.Errorf("the refusal does not say the tree is untouched: %v", err)
	}
	if len(fake.Files) != before {
		t.Errorf("the refusal still wrote %d files", len(fake.Files)-before)
	}
	if _, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-source-folder-fixture", To: "id-destination-fixture", Recursive: true, MaxItems: 9000,
	}); err == nil || !strings.Contains(err.Error(), "2000") {
		t.Errorf("a budget above the ceiling was not refused with the ceiling named: %v", err)
	}
}

func TestACopyIntoItselfIsRefused(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	copyTree(fake)
	for _, into := range []string{"id-source-folder-fixture", "id-source-sub-fixture"} {
		_, err := svc.CopyFile(t.Context(), service.CopyFileInput{
			File: "id-source-folder-fixture", To: into, Recursive: true,
		})
		if err == nil {
			t.Fatalf("copying into %s was allowed", into)
		}
		if !strings.Contains(err.Error(), "inside what is being copied") {
			t.Errorf("into %s: %v", into, err)
		}
	}
}

func TestADryRunCopiesNothingAndSaysHowBigItIs(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	copyTree(fake)
	before := len(fake.Files)

	got, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-source-folder-fixture", To: "id-destination-fixture", Recursive: true, DryRun: true,
	})
	if err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if !got.JSON.DryRun {
		t.Error("the result does not say it was a dry run")
	}
	if len(fake.Files) != before {
		t.Error("a dry run wrote something")
	}
	for _, want := range []string{"1 folder", "3 files", "1 shortcut", "Nothing was copied"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("the dry run does not say %q:\n%s", want, got.Text)
		}
	}
}

// TestADryRunOfOneFileCopiesNothing holds the single-file path to the
// dry-run contract. It used to copy: only a tree read dry_run.
func TestADryRunOfOneFileCopiesNothing(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	copyTree(fake)
	before := len(fake.Files)

	got, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-source-sheet-fixture", To: "id-destination-fixture", DryRun: true,
	})
	if err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if !got.JSON.DryRun {
		t.Error("the result does not say it was a dry run")
	}
	if len(fake.Files) != before || fake.Count(http.MethodPost) != 0 {
		t.Error("a dry run wrote something")
	}
	if !strings.Contains(got.Text, "Nothing was copied") {
		t.Errorf("the dry run does not say nothing was copied:\n%s", got.Text)
	}
}

// TestOneItemFailingDoesNotHideTheRest is the honest half of a bulk
// write: there is no rollback, so what did copy stays and what did not
// is named.
func TestOneItemFailingDoesNotHideTheRest(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	copyTree(fake)
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/id-source-sheet-fixture/copy") {
			return &drivetest.Failure{Status: http.StatusForbidden, Reason: "insufficientFilePermissions",
				Message: "The user does not have sufficient permissions for this file."}
		}
		return nil
	}
	got, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-source-folder-fixture", To: "id-destination-fixture", Recursive: true,
	})
	if err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if !strings.Contains(got.Text, "copied 4 of 5 items") {
		t.Errorf("the count does not reflect the failure:\n%s", got.Text)
	}
	for _, want := range []string{"Numbers", "did not copy", "What did copy is real"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("the result does not say %q:\n%s", want, got.Text)
		}
	}
}

// TestATreeCopyAsksForItsIdsOnce is the round trip the deferred cleanup
// in §17a is about: one generateIds for the whole tree rather than one
// per item.
func TestATreeCopyAsksForItsIdsOnce(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	copyTree(fake)
	fake.Requested()

	if _, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-source-folder-fixture", To: "id-destination-fixture", Recursive: true,
	}); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	generated := 0
	for _, r := range fake.Requested() {
		if strings.Contains(r.Path, "generateIds") {
			generated++
		}
	}
	if generated != 1 {
		t.Errorf("the copy made %d calls to generateIds, want one for the whole tree", generated)
	}
}

func TestConvertingAFolderIsRefused(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	copyTree(fake)
	_, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-source-folder-fixture", Recursive: true, ConvertTo: "doc",
	})
	if err == nil || !strings.Contains(err.Error(), "not a file with content to import") {
		t.Errorf("err = %v", err)
	}
}

// TestKeepRevisionForeverReachesTheCopiedFiles is a review finding: the
// argument was accepted and silently dropped for a recursive copy, so a
// caller asking for the copies to be pinned got copies that were not.
func TestKeepRevisionForeverReachesTheCopiedFiles(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	copyTree(fake)
	fake.Requested()

	if _, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-source-folder-fixture", To: "id-destination-fixture",
		Recursive: true, KeepRevisionForever: true,
	}); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	copies, pinned := 0, 0
	for _, r := range fake.Requested() {
		if !strings.HasSuffix(r.Path, "/copy") {
			continue
		}
		copies++
		if r.Query.Get("keepRevisionForever") == "true" {
			pinned++
		}
	}
	if copies == 0 {
		t.Fatal("nothing was copied, so this test is looking at nothing")
	}
	if pinned != copies {
		t.Errorf("%d of %d copies asked Drive to keep the revision", pinned, copies)
	}
}

// sharedOutTree shares Projects with everyone at example.com, and a
// folder outside it the same way. Archive, inside Projects, has limited
// access, which keeps the domain out of it.
func sharedOutTree(fake *drivetest.Server) {
	domain := &gdrive.Permission{Type: "domain", Role: "reader", Domain: "example.com"}
	fake.Grant("id-projects-fixture", domain)
	fake.Files["id-archive-fixture"].InheritedPermissionsDisabled = true
	fake.AddFolder("id-shared-out-fixture", "Shared out", fake.RootID)
	fake.Grant("id-shared-out-fixture", &gdrive.Permission{Type: "domain", Role: "reader", Domain: "example.com"})
}

// A tree's copy reaches who the destination does, all through. The
// folder reaching those people already widens nothing for it, while a
// folder inside it with limited access is reached further.
func TestATreeCopyAsksAboutEachPartItWouldReachFurther(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	sharedOutTree(fake)
	in := service.CopyFileInput{File: "id-projects-fixture", To: "id-shared-out-fixture", Recursive: true}

	no := declines()
	if _, err := svc.CopyFile(service.WithAsker(t.Context(), no), in); err == nil || !strings.Contains(err.Error(), "[blocked]") {
		t.Fatalf("err = %v, want the refusal the person gave", err)
	}
	want := "copy_file: copy the folder `Projects`, with everything inside it, into the folder `Shared out`?\n\n" +
		"The copy would reach more people than these parts of the original do, or give them more access:\n\n" +
		"the folder `Archive`, inside it, which has limited access: everyone at `example.com` can view with the link\n"
	if len(no.asked) != 1 || !strings.HasPrefix(no.asked[0].Text, want) {
		t.Fatalf("questions = %+v, want one starting %q", no.asked, want)
	}
	if fake.Count(http.MethodPost) != 0 {
		t.Fatal("a copy the person declined wrote something")
	}
	if _, err := svc.CopyFile(yes(t), in); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if len(childrenNamed(fake, "id-shared-out-fixture")) != 1 {
		t.Error("an accepted tree copy was not made")
	}
}

func TestSharingOffRefusesATreeCopyThatWidensAnyPart(t *testing.T) {
	svc, fake := setup(t, service.Options{Sharing: config.SharingOff})
	sharedOutTree(fake)
	_, err := svc.CopyFile(yes(t), service.CopyFileInput{File: "id-projects-fixture", To: "id-shared-out-fixture", Recursive: true})
	want := "[forbidden] this server was started with GDRIVE_SHARING=off. Inside it, Archive has limited access, and " +
		"the copy of it would reach more people than it does: everyone at example.com can view with the link. Nothing was copied."
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("err = %v, want it to start %q", err, want)
	}
	if fake.Count(http.MethodPost) != 0 {
		t.Error("a refused tree copy wrote something")
	}
}
