package service_test

import (
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/service"
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
		t.Errorf("colour = %q, want the hash restored", f.FolderColorRgb)
	}
	if _, err := svc.CreateFolder(t.Context(), service.CreateFolderInput{Name: "x", Color: "puce"}); err == nil {
		t.Error("a colour that is not a hex value was accepted")
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

func TestUpdateFileRefusesAColourOnAFile(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{File: "id-budget-fixture", Color: "#4986e7"})
	if err == nil || !strings.Contains(err.Error(), "colour is a folder's") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestMoveFile(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	dry, err := svc.MoveFile(t.Context(), service.MoveFileInput{
		File: "id-budget-fixture", To: "id-archive-fixture", DryRun: true,
	})
	if err != nil {
		t.Fatalf("MoveFile dry run: %v", err)
	}
	if !dry.JSON.DryRun {
		t.Error("a dry run did not mark itself as one")
	}
	if fake.Files["id-budget-fixture"].Parent() != "id-2026-fixture" {
		t.Fatal("the dry run moved the file")
	}
	if !strings.Contains(dry.Text, "My Drive/Projects/2026") || !strings.Contains(dry.Text, "My Drive/Projects/Archive") {
		t.Errorf("the dry run does not show both locations:\n%s", dry.Text)
	}

	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{File: "id-budget-fixture", To: "id-archive-fixture"})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if fake.Files["id-budget-fixture"].Parent() != "id-archive-fixture" {
		t.Errorf("parent = %q", fake.Files["id-budget-fixture"].Parent())
	}
	if len(got.JSON.Changes) != 1 || got.JSON.Changes[0].Field != "location" {
		t.Errorf("changes = %v, want the location before and after", got.JSON.Changes)
	}
}

func TestMoveFileRefusesAMyDriveFolderIntoASharedDrive(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	_, err := svc.MoveFile(t.Context(), service.MoveFileInput{
		File: "id-projects-fixture", To: "id-campaigns-fixture",
	})
	if err == nil {
		t.Fatal("a My Drive folder moved into a shared drive")
	}
	if !strings.Contains(err.Error(), "[unsupported]") || !strings.Contains(err.Error(), "create_folder") {
		t.Errorf("the refusal does not say what to do instead: %v", err)
	}
	if fake.Files["id-projects-fixture"].Parent() != fake.RootID {
		t.Error("the folder moved anyway")
	}
}

func TestMoveFileSaysWhenItIsAlreadyThere(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{File: "id-budget-fixture", To: "/Projects/2026"})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got.JSON.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", got.JSON.Action)
	}
}

func TestMoveFileMovesAFileIntoASharedDrive(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	if _, err := svc.MoveFile(t.Context(), service.MoveFileInput{
		File: "id-budget-fixture", To: "drive:Marketing/Campaigns",
	}); err != nil {
		t.Fatalf("MoveFile into a shared drive: %v", err)
	}
	moved := fake.Files["id-budget-fixture"]
	if moved.Parent() != "id-campaigns-fixture" || moved.DriveID != "id-drive-marketing" {
		t.Errorf("moved to parent %q in drive %q", moved.Parent(), moved.DriveID)
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
	if !strings.Contains(converted.Text, "The original Budget.xlsx is untouched") {
		t.Errorf("a conversion did not say the original was left alone:\n%s", converted.Text)
	}
}

func TestCopyFileRefusesAFolder(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.CopyFile(t.Context(), service.CopyFileInput{File: "id-2026-fixture"})
	if err == nil || !strings.Contains(err.Error(), "create_folder") {
		t.Fatalf("err = %v, want a refusal naming the way round it", err)
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
