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

func TestCreateFolder(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	got, err := svc.CreateFolder(t.Context(), service.CreateFolderInput{
		Name: "Reports", Parent: "/Projects", Color: "4986e7", Description: "quarterly",
	})
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	f := fake.Files[got.JSON.File.ID]
	if f == nil || !f.IsFolder() || f.Parent() != "id-projects-fixture" {
		t.Fatalf("created %v", f)
	}
	if f.FolderColorRgb != "#4986e7" {
		t.Errorf("color = %q, want the hash restored", f.FolderColorRgb)
	}
	if _, err := svc.CreateFolder(t.Context(), service.CreateFolderInput{Name: "x", Color: "puce"}); err == nil {
		t.Error("a color that is not a hex value was accepted")
	}
}

func TestUpdateFileReportsEveryFieldBeforeAndAfter(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	got, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{
		File: "id-budget-fixture", Name: "Budget 2026.xlsx", Starred: boolPtr(true),
		Properties: map[string]string{"owner-team": "finance"},
	})
	if err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	if fake.Files["id-budget-fixture"].Name != "Budget 2026.xlsx" {
		t.Errorf("name = %q", fake.Files["id-budget-fixture"].Name)
	}
	fields := map[string]bool{}
	for _, c := range got.JSON.Changes {
		fields[c.Field] = true
	}
	for _, want := range []string{"name", "starred", "property owner-team"} {
		if !fields[want] {
			t.Errorf("no change reported for %s: %v", want, got.JSON.Changes)
		}
	}
	if !strings.Contains(got.Text, "Budget.xlsx → Budget 2026.xlsx") {
		t.Errorf("the text does not show the rename:\n%s", got.Text)
	}
}

func TestUpdateFileDeletesAPropertyWithAnEmptyValue(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Files["id-budget-fixture"].Properties = map[string]string{"owner-team": "finance"}

	if _, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{
		File: "id-budget-fixture", Properties: map[string]string{"owner-team": ""},
	}); err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	if _, still := fake.Files["id-budget-fixture"].Properties["owner-team"]; still {
		t.Error("the property is still there")
	}
}

func TestUpdateFileSaysWhenNothingChanged(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	got, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{
		File: "id-budget-fixture", Name: "Budget.xlsx",
	})
	if err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	if got.JSON.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", got.JSON.Action)
	}
}

func TestUpdateFileRefusesAColorOnAFile(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{File: "id-budget-fixture", Color: "#4986e7"})
	if err == nil || !strings.Contains(err.Error(), "color is a folder's") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestCopyFile(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.SetContent("id-budget-fixture", "a,b\n")

	got, err := svc.CopyFile(t.Context(), service.CopyFileInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if got.JSON.File.Name != "Copy of Budget.xlsx" {
		t.Errorf("name = %q, want Drive's own default", got.JSON.File.Name)
	}
	if fake.Files["id-budget-fixture"].Name != "Budget.xlsx" {
		t.Error("the original changed")
	}

	converted, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-budget-fixture", Name: "Budget as a sheet", ConvertTo: "sheet", To: "/Projects/Archive",
	})
	if err != nil {
		t.Fatalf("CopyFile converting: %v", err)
	}
	if converted.JSON.File.MimeType != gdrive.MimeSheet {
		t.Errorf("converted to %q", converted.JSON.File.MimeType)
	}
	if !strings.Contains(converted.Text, "Google imported the copy as a Google Sheet. The original Budget.xlsx is untouched.") {
		t.Errorf("a conversion did not say what the copy became and that the original was left alone:\n%s",
			converted.Text)
	}
}

func TestCopyFileAsksBeforeTheCopyReachesMorePeople(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	linkShared(fake)
	fake.SetContent("id-budget-fixture", "a,b\n")
	in := service.CopyFileInput{File: "id-budget-fixture", To: "id-archive-fixture"}

	no := declines()
	if _, err := svc.CopyFile(service.WithAsker(t.Context(), no), in); err == nil || !strings.Contains(err.Error(), "[blocked]") {
		t.Fatalf("err = %v, want the refusal the person gave", err)
	}
	want := "copy_file: copy the file `Budget.xlsx` into the folder `Archive`?\n\nThe copy would reach more people " +
		"than the original does, or give them more access: anyone with the link can view\n"
	if len(no.asked) != 1 || !strings.HasPrefix(no.asked[0].Text, want) {
		t.Fatalf("questions = %+v, want one starting %q", no.asked, want)
	}
	if fake.Count(http.MethodPost) != 0 {
		t.Fatal("a copy the person declined was made")
	}

	got, err := svc.CopyFile(yes(t), in)
	if err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if got.JSON.SharingBefore != "private to you" || got.JSON.SharingAfter != "anyone with the link can view" {
		t.Errorf("sharing %q → %q, want private to you → anyone with the link can view", got.JSON.SharingBefore, got.JSON.SharingAfter)
	}
	if !strings.Contains(got.Text, "Who can reach the original: private to you. Who can reach the copy: anyone with the link can view.") {
		t.Errorf("the result does not say who reaches each:\n%s", got.Text)
	}
}

func TestCopyFileDryRunShowsWhoWouldReachTheCopyAndAsksNothing(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	linkShared(fake)
	p := &person{}
	got, err := svc.CopyFile(service.WithAsker(t.Context(), p), service.CopyFileInput{
		File: "id-budget-fixture", To: "id-archive-fixture", DryRun: true,
	})
	if err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if len(p.asked) != 0 || fake.Count(http.MethodPost) != 0 {
		t.Fatal("a dry run asked the person or copied")
	}
	want := "Would copy it as Copy of Budget.xlsx into Archive. The copy would reach more people than the original " +
		"does, or give them more access: anyone with the link can view. A real copy puts that to the person first " +
		"when the client can ask. Nothing was copied. Who can reach the original: private to you. Who would reach " +
		"the copy: anyone with the link can view."
	if got.JSON.Note != want {
		t.Errorf("note = %q\nwant   %q", got.JSON.Note, want)
	}
}

// ownedByTheAccount lists the account as the owner of Projects and of
// Archive inside it, the shape Drive gives a My Drive folder: an owner
// grant made on the folder, and the same one again from the folder
// above.
func ownedByTheAccount(fake *drivetest.Server) {
	for _, id := range []string{"id-projects-fixture", "id-archive-fixture"} {
		fake.Grant(id, &gdrive.Permission{Type: "user", Role: "owner", EmailAddress: drivetest.AccountEmail})
	}
}

// A copy is the account's own, so the destination's owner, which is
// the same account, is not one more person who can edit it. Both
// shapes are the ones a live run got wrong: a file into a folder open by
// link, and a folder into a private one.
func TestACopyIsOwnedByTheAccountAndCountsNobodyElse(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   service.CopyFileInput
		link bool
		want string
	}{
		{"a file into a folder open by link", service.CopyFileInput{File: "id-budget-fixture", To: "id-archive-fixture"},
			true, "anyone with the link can view"},
		{"a folder into a private folder", service.CopyFileInput{File: "id-2026-fixture", To: "id-archive-fixture",
			Recursive: true}, false, "private to you"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake := setup(t, service.Options{})
			ownedByTheAccount(fake)
			if tc.link {
				linkShared(fake)
			}
			dry := tc.in
			dry.DryRun = true
			got, err := svc.CopyFile(t.Context(), dry)
			if err != nil {
				t.Fatalf("CopyFile dry run: %v", err)
			}
			if got.JSON.SharingAfter != tc.want {
				t.Errorf("dry run: sharing after = %q, want %q", got.JSON.SharingAfter, tc.want)
			}
			got, err = svc.CopyFile(yes(t), tc.in)
			if err != nil {
				t.Fatalf("CopyFile: %v", err)
			}
			if got.JSON.SharingAfter != tc.want || strings.Contains(got.JSON.Note, "not who this server worked out") {
				t.Errorf("sharing after = %q, note %q; want %q and no mismatch", got.JSON.SharingAfter, got.JSON.Note, tc.want)
			}
		})
	}
}

// A copy takes on its destination's sharing and none of the original's.
func TestACopyCarriesNoneOfTheOriginalsSharing(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-budget-fixture", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: "carol@example.com"})
	got, err := svc.CopyFile(t.Context(), service.CopyFileInput{File: "id-budget-fixture", To: "root", DryRun: true})
	if err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if got.JSON.SharingBefore != "shared with 1 person: 1 can edit" || got.JSON.SharingAfter != "private to you" {
		t.Errorf("sharing %q → %q, want the original shared with carol and the copy private",
			got.JSON.SharingBefore, got.JSON.SharingAfter)
	}
}

// A copy with no destination lands beside the original and takes on that
// folder's sharing, which a folder with limited access does not have.
func TestACopyBesideTheOriginalTakesOnItsFolder(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-projects-fixture", &gdrive.Permission{Type: "domain", Role: "reader", Domain: "example.com"})
	fake.Files["id-archive-fixture"].InheritedPermissionsDisabled = true
	no := declines()
	_, err := svc.CopyFile(service.WithAsker(t.Context(), no), service.CopyFileInput{File: "id-archive-fixture", Recursive: true})
	if err == nil || len(no.asked) != 1 || !strings.Contains(no.asked[0].Text, "into the folder `Projects`?") ||
		!strings.Contains(no.asked[0].Text, "everyone at `example.com` can view") {
		t.Errorf("err = %v, questions %+v; want one naming Projects and its domain", err, no.asked)
	}
}

func TestSharingOffRefusesACopyThatWidens(t *testing.T) {
	svc, fake := setup(t, service.Options{Sharing: config.SharingOff})
	linkShared(fake)
	p := &person{}
	ctx := service.WithAsker(t.Context(), p)
	for _, dry := range []bool{true, false} {
		_, err := svc.CopyFile(ctx, service.CopyFileInput{File: "id-budget-fixture", To: "id-archive-fixture", DryRun: dry})
		want := "[forbidden] this server was started with GDRIVE_SHARING=off, and the copy would reach more people " +
			"than the original does, or give them more access: anyone with the link can view. Nothing was copied."
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("dry run %v: err = %v, want it to start %q", dry, err, want)
		}
	}
	if len(p.asked) != 0 || fake.Count(http.MethodPost) != 0 {
		t.Fatal("a refused copy asked the person or copied")
	}
	if _, err := svc.CopyFile(ctx, service.CopyFileInput{File: "id-budget-fixture", To: "root"}); err != nil {
		t.Errorf("a copy that reaches nobody new was refused: %v", err)
	}
}

func TestACopyOnlyThisAccountReachesAsksNothingWhoeverReachesTheOriginal(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	linkShared(fake)
	// Who reaches the original cannot be read, as for a file this
	// account may only view.
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if strings.HasSuffix(r.URL.Path, "/files/id-budget-fixture/permissions") {
			return &drivetest.Failure{Status: http.StatusForbidden, Reason: "insufficientFilePermissions", Message: "no"}
		}
		return nil
	}
	p := &person{err: service.Errorf(service.ClassBlocked, "not confirmed")}
	ctx := service.WithAsker(t.Context(), p)
	if _, err := svc.CopyFile(ctx, service.CopyFileInput{File: "id-budget-fixture", To: "root"}); err != nil {
		t.Fatalf("a copy into My Drive's root: %v", err)
	}
	if len(p.asked) != 0 {
		t.Fatalf("a copy only this account reaches asked: %s", p.asked[0].Text)
	}
	if _, err := svc.CopyFile(ctx, service.CopyFileInput{File: "id-budget-fixture", To: "id-archive-fixture"}); err == nil {
		t.Fatal("a copy into a link-shared folder went ahead unasked")
	}
	want := "Who can reach it could not be read, so whether the copy would reach more people than the original does is unknown."
	if len(p.asked) != 1 || !strings.Contains(p.asked[0].Text, want) {
		t.Errorf("questions = %+v, want one saying %q", p.asked, want)
	}
}

func TestCopyFileRefusesAFolderUntilItIsAskedRecursively(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.CopyFile(t.Context(), service.CopyFileInput{File: "id-2026-fixture"})
	if err == nil || !strings.Contains(err.Error(), "recursive: true") {
		t.Fatalf("err = %v, want a refusal naming the way round it", err)
	}
	// And the other way: recursive on something that is not a folder is
	// a misunderstanding worth naming rather than ignoring.
	_, err = svc.CopyFile(t.Context(), service.CopyFileInput{File: "id-budget-fixture", Recursive: true})
	if err == nil || !strings.Contains(err.Error(), "not a folder") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestCreateShortcut(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	got, err := svc.CreateShortcut(t.Context(), service.CreateShortcutInput{
		Target: "id-budget-fixture", Parent: "/Projects/Archive", Name: "Budget link",
	})
	if err != nil {
		t.Fatalf("CreateShortcut: %v", err)
	}
	f := fake.Files[got.JSON.File.ID]
	if f == nil || !f.IsShortcut() || f.ShortcutDetails.TargetID != "id-budget-fixture" {
		t.Fatalf("created %v", f)
	}
	if !strings.Contains(got.Text, "act on the shortcut") {
		t.Errorf("the result does not warn what a shortcut does:\n%s", got.Text)
	}
	// Once: the card says it, and the note names what the shortcut is to.
	if n := strings.Count(got.Text, "act on the shortcut"); n != 1 {
		t.Errorf("the result says what a shortcut does %d times:\n%s", n, got.Text)
	}
	if !strings.HasSuffix(got.Text, "note: it points at Budget.xlsx.\n") {
		t.Errorf("the note does not name the target:\n%s", got.Text)
	}
}

func TestTrashAndRestore(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	dry, err := svc.TrashFile(t.Context(), service.TrashInput{File: "id-budget-fixture", DryRun: true})
	if err != nil {
		t.Fatalf("TrashFile dry run: %v", err)
	}
	if !dry.JSON.DryRun || fake.Files["id-budget-fixture"].Trashed {
		t.Fatal("the dry run trashed the file")
	}

	got, err := svc.TrashFile(t.Context(), service.TrashInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if !fake.Files["id-budget-fixture"].Trashed {
		t.Fatal("the file is not in the trash")
	}
	if !strings.Contains(got.Text, "restore_file") || !strings.Contains(got.Text, "30 days") {
		t.Errorf("the result does not say how to undo it:\n%s", got.Text)
	}

	back, err := svc.RestoreFile(t.Context(), service.TrashInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}
	if fake.Files["id-budget-fixture"].Trashed {
		t.Error("the file is still in the trash")
	}
	if !strings.Contains(back.Text, "My Drive/Projects/2026") {
		t.Errorf("a restore did not say where the file went:\n%s", back.Text)
	}
}

func TestTrashAFolderSaysItTakesItsContents(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	got, err := svc.TrashFile(t.Context(), service.TrashInput{File: "id-2026-fixture"})
	if err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if !strings.Contains(got.Text, "with everything inside it") {
		t.Errorf("trashing a folder did not say what goes with it:\n%s", got.Text)
	}
	if !fake.Files["id-budget-fixture"].Trashed {
		t.Error("a file inside the trashed folder is not in the trash")
	}
	if fake.Files["id-budget-fixture"].ExplicitlyTrashed {
		t.Error("a file that went along with its folder is marked as explicitly trashed")
	}
}

func TestTrashActsOnTheShortcutNotItsTarget(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	if _, err := svc.TrashFile(t.Context(), service.TrashInput{File: "id-shortcut-fixture"}); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if !fake.Files["id-shortcut-fixture"].Trashed {
		t.Error("the shortcut is not in the trash")
	}
	if fake.Files["id-budget-fixture"].Trashed {
		t.Fatal("trashing a shortcut destroyed what it pointed at")
	}
}

func TestTrashSaysWhenThereIsNothingToDo(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	got, err := svc.TrashFile(t.Context(), service.TrashInput{File: "id-old-plan-fixture"})
	if err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if got.JSON.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", got.JSON.Action)
	}
}

func TestTrashRespectsWhatDriveSaysThisAccountCanDo(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Files["id-budget-fixture"].Capabilities = &gdrive.Capabilities{CanEdit: true}

	_, err := svc.TrashFile(t.Context(), service.TrashInput{File: "id-budget-fixture"})
	if err == nil || !strings.Contains(err.Error(), "[forbidden]") {
		t.Fatalf("err = %v, want a refusal from the file's own capabilities", err)
	}
	if fake.Files["id-budget-fixture"].Trashed {
		t.Error("the file was trashed anyway")
	}
}

func TestAWriteInvalidatesTheCaches(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	// Warm the path cache, then rename through it.
	if _, err := svc.GetFile(t.Context(), service.GetFileInput{File: "/Projects/2026/Budget.xlsx"}); err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if _, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{
		File: "id-budget-fixture", Name: "Renamed.xlsx",
	}); err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	if _, err := svc.GetFile(t.Context(), service.GetFileInput{File: "/Projects/2026/Budget.xlsx"}); err == nil {
		t.Error("the old path still resolves; the cache outlived the rename")
	}
	out, err := svc.GetFile(t.Context(), service.GetFileInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if !strings.Contains(out, "Renamed.xlsx") {
		t.Errorf("a read after the rename showed the old name:\n%s", out)
	}
	_ = fake
}

func boolPtr(b bool) *bool { return &b }
