package service_test

import (
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

func TestDestructiveToolsRefuseWithoutTheDeployersSwitch(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	calls := map[string]func() error{
		"delete_file": func() error {
			_, err := svc.DeleteFile(t.Context(), service.DeleteFileInput{File: "id-budget-fixture", Confirm: true})
			return err
		},
		"empty_trash": func() error {
			_, err := svc.EmptyTrash(t.Context(), service.EmptyTrashInput{Confirm: true})
			return err
		},
		"delete_drive": func() error {
			_, err := svc.DeleteDrive(t.Context(), service.DeleteDriveInput{Drive: "Marketing", Confirm: true})
			return err
		},
		"delete_revision": func() error {
			_, err := svc.DeleteRevision(t.Context(), service.DeleteRevisionInput{
				File: "id-budget-fixture", Revision: "x", Confirm: true})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil {
				t.Fatal("it ran without GDRIVE_ENABLE_DESTRUCTIVE")
			}
			if !strings.Contains(err.Error(), "GDRIVE_ENABLE_DESTRUCTIVE") {
				t.Errorf("the refusal does not name the setting: %v", err)
			}
			// The reversible way out is what the caller should do instead.
			if !strings.Contains(err.Error(), "trash_file") {
				t.Errorf("the refusal does not offer the reversible tool: %v", err)
			}
		})
	}
	if fake.Files["id-budget-fixture"] == nil {
		t.Fatal("a refusal still destroyed something")
	}
}

func TestDestructiveToolsAlsoNeedConfirmOnTheCall(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})

	// The deployer's switch says the tool may exist; confirm says this
	// particular call was meant. They are different questions.
	_, err := svc.DeleteFile(t.Context(), service.DeleteFileInput{File: "id-budget-fixture"})
	if err == nil {
		t.Fatal("a delete went through without confirm")
	}
	if !strings.Contains(err.Error(), "confirm: true") {
		t.Errorf("the refusal does not say what to pass: %v", err)
	}
	if fake.Files["id-budget-fixture"] == nil {
		t.Fatal("the refusal still deleted the file")
	}

	if _, err := svc.EmptyTrash(t.Context(), service.EmptyTrashInput{}); err == nil ||
		!strings.Contains(err.Error(), "confirm: true") {
		t.Errorf("empty_trash without confirm: %v", err)
	}
}

func TestDeleteFileDestroysItWithNoWayBack(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})

	got, err := svc.DeleteFile(t.Context(), service.DeleteFileInput{
		File: "id-budget-fixture", Confirm: true,
	})
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if fake.Files["id-budget-fixture"] != nil {
		t.Fatal("the file is still there")
	}
	if got.JSON.Action != "deleted" {
		t.Errorf("action = %q", got.JSON.Action)
	}
	// "It is in the trash" would be a lie, and the difference is the
	// whole point of this tool being separate.
	if !strings.Contains(got.Text, "nothing to restore") {
		t.Errorf("the result does not say it cannot be brought back:\n%s", got.Text)
	}
}

func TestDeletingAFolderSaysItTakesItsContents(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})
	got, err := svc.DeleteFile(t.Context(), service.DeleteFileInput{
		File: "id-2026-fixture", Confirm: true, DryRun: true,
	})
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if !strings.Contains(got.Text, "everything inside it") {
		t.Errorf("the dry run does not say a folder takes its contents:\n%s", got.Text)
	}
	if fake.Files["id-2026-fixture"] == nil || fake.Files["id-budget-fixture"] == nil {
		t.Fatal("a dry run destroyed something")
	}

	if _, err := svc.DeleteFile(t.Context(), service.DeleteFileInput{
		File: "id-2026-fixture", Confirm: true,
	}); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if fake.Files["id-budget-fixture"] != nil {
		t.Error("the folder went but its contents did not")
	}
}

func TestEmptyTrashSaysHowMuchItWouldDestroy(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})

	got, err := svc.EmptyTrash(t.Context(), service.EmptyTrashInput{DryRun: true})
	if err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}
	// It is the one destructive call that names no item, so the count is
	// the only thing standing between a caller and a surprise.
	if !strings.Contains(got.Text, "1 item in it right now") {
		t.Errorf("the dry run does not say how much is in there:\n%s", got.Text)
	}
	if fake.Files["id-old-plan-fixture"] == nil {
		t.Fatal("a dry run emptied the trash")
	}

	if _, err := svc.EmptyTrash(t.Context(), service.EmptyTrashInput{Confirm: true}); err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}
	if fake.Files["id-old-plan-fixture"] != nil {
		t.Error("the trashed file survived")
	}
	// Nothing outside the trash is touched.
	if fake.Files["id-budget-fixture"] == nil {
		t.Error("emptying the trash took a live file with it")
	}
}

func TestDeleteDriveRefusesOneThatStillHoldsThings(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})
	// Both drives exist before the first lookup: the service caches the
	// drive list, so one added later would not be visible yet.
	empty := fake.AddDrive("id-drive-empty", "Retired")

	_, err := svc.DeleteDrive(t.Context(), service.DeleteDriveInput{Drive: "Marketing", Confirm: true})
	if err == nil {
		t.Fatal("a shared drive with items in it was deleted")
	}
	// Drive's own safeguard, and the way round it, which is deliberately
	// not an option on this call.
	if !strings.Contains(err.Error(), "trash what is in there first") {
		t.Errorf("the refusal does not say what to do instead: %v", err)
	}
	if fake.Drives["id-drive-marketing"] == nil {
		t.Fatal("the drive went anyway")
	}

	if _, err := svc.DeleteDrive(t.Context(), service.DeleteDriveInput{
		Drive: "Retired", Confirm: true,
	}); err != nil {
		t.Fatalf("DeleteDrive on an empty drive: %v", err)
	}
	if fake.Drives[empty.ID] != nil {
		t.Error("the empty drive is still there")
	}
}

func TestDeleteRevisionRefusesTheCurrentOneAndGoogleDocuments(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})
	fake.SetContent("id-budget-fixture", "first")
	fake.SetContent("id-budget-fixture", "second")
	revs := fake.Revisions["id-budget-fixture"]
	old, head := revs[0].ID, revs[1].ID

	_, err := svc.DeleteRevision(t.Context(), service.DeleteRevisionInput{
		File: "id-budget-fixture", Revision: head, Confirm: true,
	})
	if err == nil || !strings.Contains(err.Error(), "will not delete") {
		t.Errorf("the current revision: %v", err)
	}

	fake.SetContent("id-notes-fixture", "a doc")
	_, err = svc.DeleteRevision(t.Context(), service.DeleteRevisionInput{
		File: "id-notes-fixture", Revision: fake.Revisions["id-notes-fixture"][0].ID, Confirm: true,
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[unsupported]") {
		t.Errorf("a Google document's revision: %v", err)
	}

	got, err := svc.DeleteRevision(t.Context(), service.DeleteRevisionInput{
		File: "id-budget-fixture", Revision: old, Confirm: true,
	})
	if err != nil {
		t.Fatalf("DeleteRevision: %v", err)
	}
	if len(fake.Revisions["id-budget-fixture"]) != 1 {
		t.Errorf("revisions = %d, want the old one gone", len(fake.Revisions["id-budget-fixture"]))
	}
	if !strings.Contains(got.Text, "current content is untouched") {
		t.Errorf("the result does not say the file itself is fine:\n%s", got.Text)
	}
}

func TestDestructiveToolsAreNeverAvailableReadOnly(t *testing.T) {
	// Read-only wins over the destructive switch: a deployer who set
	// both meant the stricter one.
	svc, _ := setup(t, service.Options{ReadOnly: true, Destructive: true})
	_, err := svc.DeleteFile(t.Context(), service.DeleteFileInput{File: "id-budget-fixture", Confirm: true})
	if err == nil {
		t.Fatal("a delete ran on a read-only server")
	}
	if !strings.Contains(err.Error(), "GDRIVE_READ_ONLY") {
		t.Errorf("the refusal does not name the setting that won: %v", err)
	}
}

func TestDeleteFileWillNotDestroyTheTopOfADrive(t *testing.T) {
	// "root" is Drive's alias for My Drive's root, and a shared drive's
	// id is its own root folder's id, so either can be passed where a
	// file is expected. Google refuses both through canDelete; this
	// asserts the server refuses them on its own, because a bounded call
	// becoming an unbounded one must not rest on a field the other side
	// computes.
	svc, fake := setup(t, service.Options{Destructive: true})
	for _, ref := range []string{"root", drivetest.RootFolderID, "drive:Marketing", "id-drive-marketing"} {
		t.Run(ref, func(t *testing.T) {
			_, err := svc.DeleteFile(t.Context(), service.DeleteFileInput{File: ref, Confirm: true})
			if err == nil {
				t.Fatalf("delete_file accepted %q", ref)
			}
			if !strings.HasPrefix(err.Error(), "[forbidden]") {
				t.Errorf("err = %v, want a forbidden class", err)
			}
		})
	}
	if fake.Files[drivetest.RootFolderID] == nil || fake.Drives["id-drive-marketing"] == nil {
		t.Fatal("the top of a drive was destroyed")
	}
	// Capabilities are Drive's answer, not ours: with them absent the
	// refusal must still hold.
	fake.Files[drivetest.RootFolderID].Capabilities = nil
	if _, err := svc.DeleteFile(t.Context(), service.DeleteFileInput{
		File: "root", Confirm: true,
	}); err == nil {
		t.Error("with no capabilities from Drive, the root was deletable")
	}
}

func TestEmptyTrashCountsOnlyTheTrashItWillEmpty(t *testing.T) {
	// Without a drive the subject is this account's own trash. A listing
	// defaults to every drive it can see, so counting that way would
	// report a shared drive's trashed items as part of what is about to
	// be destroyed — overstating the damage of a call whose whole job is
	// to state it accurately.
	svc, fake := setup(t, service.Options{Destructive: true})
	fake.AddFile("id-drive-trash-fixture", "Old brief", "application/pdf", "id-campaigns-fixture",
		drivetest.InDrive("id-drive-marketing"), drivetest.Trashed())

	got, err := svc.EmptyTrash(t.Context(), service.EmptyTrashInput{DryRun: true})
	if err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}
	if !strings.Contains(got.Text, "1 item in it right now") {
		t.Errorf("the count includes a shared drive's trash:\n%s", got.Text)
	}

	// Named, the shared drive's own trash is what gets counted.
	inDrive, err := svc.EmptyTrash(t.Context(), service.EmptyTrashInput{Drive: "Marketing", DryRun: true})
	if err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}
	if !strings.Contains(inDrive.Text, "1 item in it right now") {
		t.Errorf("the shared drive's trash was not counted:\n%s", inDrive.Text)
	}
	if !strings.Contains(inDrive.Text, "Marketing") {
		t.Errorf("the result does not name whose trash:\n%s", inDrive.Text)
	}
}
