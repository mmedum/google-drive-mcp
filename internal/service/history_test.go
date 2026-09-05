package service_test

import (
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/service"
)

func TestListRevisionsIsNewestFirstAndMarksTheCurrentOne(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.SetContent("id-budget-fixture", "first")
	fake.SetContent("id-budget-fixture", "second")
	fake.SetContent("id-budget-fixture", "third")

	out, err := svc.ListRevisions(t.Context(), service.ListRevisionsInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	revs := fake.Revisions["id-budget-fixture"]
	if len(revs) != 3 {
		t.Fatalf("the fixture has %d revisions", len(revs))
	}
	newest, oldest := revs[2].ID, revs[0].ID
	if strings.Index(out, newest) > strings.Index(out, oldest) {
		t.Errorf("the oldest revision is listed first:\n%s", out)
	}
	if !strings.Contains(out, "current") {
		t.Errorf("the current revision is not marked:\n%s", out)
	}
	// The 30-day rule is the fact that makes pinning worth knowing about.
	if !strings.Contains(out, "30 days") {
		t.Errorf("the listing does not say revisions are discarded:\n%s", out)
	}
}

func TestAGoogleDocumentsHistoryRepeatsGooglesOwnCaveat(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.SetContent("id-notes-fixture", "some text")

	out, err := svc.ListRevisions(t.Context(), service.ListRevisionsInput{File: "id-notes-fixture"})
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if !strings.Contains(out, "can be incomplete") {
		t.Errorf("the caveat is missing for a Google document:\n%s", out)
	}
}

func TestListRevisionsRefusesAFolder(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.ListRevisions(t.Context(), service.ListRevisionsInput{File: "id-projects-fixture"})
	if err == nil || !strings.Contains(err.Error(), "no content") {
		t.Errorf("err = %v, want a refusal naming why", err)
	}
}

func TestManageRevisionPinsAndUnpins(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.SetContent("id-budget-fixture", "first")
	fake.SetContent("id-budget-fixture", "second")
	old := fake.Revisions["id-budget-fixture"][0].ID

	got, err := svc.ManageRevision(t.Context(), service.ManageRevisionInput{
		File: "id-budget-fixture", Revision: old, Action: "keep",
	})
	if err != nil {
		t.Fatalf("ManageRevision: %v", err)
	}
	if !fake.Revisions["id-budget-fixture"][0].KeepForever {
		t.Error("the revision was not pinned")
	}
	if len(got.JSON.Changes) != 1 || got.JSON.Changes[0].To != "yes" {
		t.Errorf("changes = %+v", got.JSON.Changes)
	}

	// Pinning what is already pinned changes nothing, and says so.
	again, err := svc.ManageRevision(t.Context(), service.ManageRevisionInput{
		File: "id-budget-fixture", Revision: old, Action: "keep",
	})
	if err != nil {
		t.Fatalf("ManageRevision: %v", err)
	}
	if again.JSON.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", again.JSON.Action)
	}

	unpinned, err := svc.ManageRevision(t.Context(), service.ManageRevisionInput{
		File: "id-budget-fixture", Revision: old, Action: "unkeep",
	})
	if err != nil {
		t.Fatalf("ManageRevision: %v", err)
	}
	if fake.Revisions["id-budget-fixture"][0].KeepForever {
		t.Error("the revision is still pinned")
	}
	// Unpinning may mean it is already gone, which is worth saying.
	if !strings.Contains(unpinned.Text, "may already be past") {
		t.Errorf("the result does not warn what unpinning means:\n%s", unpinned.Text)
	}
}

func TestManageRevisionExplainsAMissingRevision(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.ManageRevision(t.Context(), service.ManageRevisionInput{
		File: "id-budget-fixture", Revision: "id-revision-that-never-was", Action: "keep",
	})
	if err == nil {
		t.Fatal("a revision that is not there was accepted")
	}
	if !strings.Contains(err.Error(), "list_revisions") {
		t.Errorf("the refusal does not say how to find the right id: %v", err)
	}
	if !strings.Contains(err.Error(), "30 days") {
		t.Errorf("the refusal does not say why it might be gone: %v", err)
	}

	for _, action := range []string{"", "pin"} {
		if _, err := svc.ManageRevision(t.Context(), service.ManageRevisionInput{
			File: "id-budget-fixture", Revision: "x", Action: action,
		}); err == nil {
			t.Errorf("action %q was accepted", action)
		}
	}
}

func TestChangesWithNoTokenHandsBackAStartingPoint(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.ListChanges(t.Context(), service.ListChangesInput{})
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if !strings.Contains(out, "page_token") {
		t.Errorf("no token was offered:\n%s", out)
	}
	// The feed has no beginning, and saying so is what stops a model
	// expecting history it will never get.
	if !strings.Contains(out, "no beginning of its own") {
		t.Errorf("the result does not explain the feed:\n%s", out)
	}
}

func TestChangesSinceATokenListWhatHappened(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	token := startToken(t, svc)

	if _, err := svc.CreateFolder(t.Context(), service.CreateFolderInput{
		Name: "Reports", Parent: "id-projects-fixture",
	}); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if _, err := svc.TrashFile(t.Context(), service.TrashInput{File: "id-budget-fixture"}); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}

	out, err := svc.ListChanges(t.Context(), service.ListChangesInput{PageToken: token})
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if !strings.Contains(out, "Reports") {
		t.Errorf("the new folder is not in the feed:\n%s", out)
	}
	// A trashing and a removal must not read the same: only one is
	// reversible.
	if !strings.Contains(out, "trashed (restore_file brings it back)") {
		t.Errorf("a trashed file is not described as reversible:\n%s", out)
	}
	if !strings.Contains(out, "call again later with page_token") {
		t.Errorf("the next token is missing:\n%s", out)
	}
	_ = fake
}

func TestARemovedFileIsNotReportedAsTrashed(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})
	token := startToken(t, svc)

	if _, err := svc.DeleteFile(t.Context(), service.DeleteFileInput{
		File: "id-budget-fixture", Confirm: true,
	}); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if fake.Files["id-budget-fixture"] != nil {
		t.Fatal("the file is still there")
	}

	out, err := svc.ListChanges(t.Context(), service.ListChangesInput{PageToken: token})
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if !strings.Contains(out, "gone (deleted or no longer shared with you)") {
		t.Errorf("a removal is not described as one:\n%s", out)
	}
	if strings.Contains(out, "restore_file brings it back") {
		t.Errorf("a removal was described as reversible:\n%s", out)
	}
}

func TestABadChangeTokenSaysHowToGetAGoodOne(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.ListChanges(t.Context(), service.ListChangesInput{PageToken: "not-a-token"})
	if err == nil {
		t.Fatal("a token that is not one was accepted")
	}
	if !strings.Contains(err.Error(), "no page_token") {
		t.Errorf("the refusal does not say how to recover: %v", err)
	}
}

func TestChangesCanBeLimitedToOneSharedDrive(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.ListChanges(t.Context(), service.ListChangesInput{Drive: "Marketing"})
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if !strings.Contains(out, "shared drive Marketing") {
		t.Errorf("the feed does not say which drive it follows:\n%s", out)
	}
	if _, err := svc.ListChanges(t.Context(), service.ListChangesInput{Drive: "Nope"}); err == nil {
		t.Error("a shared drive that is not there was accepted")
	}
}

// startToken reads the starting point out of a no-token call, which is
// the only way a caller ever gets one.
func startToken(t *testing.T, svc *service.Service) string {
	t.Helper()
	out, err := svc.ListChanges(t.Context(), service.ListChangesInput{})
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	_, rest, ok := strings.Cut(out, `page_token "`)
	if !ok {
		t.Fatalf("no token in:\n%s", out)
	}
	token, _, ok := strings.Cut(rest, `"`)
	if !ok {
		t.Fatalf("no token in:\n%s", out)
	}
	return token
}

func TestANullEntryInTheFeedDoesNotTakeTheServerDown(t *testing.T) {
	// JSON can carry a null in an array and Drive has no reason to send
	// one, which is exactly why nothing was checking: model.NewChange
	// returns nil for it and the renderer dereferenced that. A malformed
	// page should cost one missing row, not the whole stdio server.
	svc, fake := setup(t, service.Options{})
	token := startToken(t, svc)
	if _, err := svc.CreateFolder(t.Context(), service.CreateFolderInput{Name: "Reports"}); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	fake.Changes = append(fake.Changes, nil)

	out, err := svc.ListChanges(t.Context(), service.ListChangesInput{PageToken: token})
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if out == "" {
		t.Error("the feed rendered nothing at all")
	}
	// The real change either side of the null is still reported: a
	// malformed entry costs one row, not the page.
	if !strings.Contains(out, "Reports") {
		t.Errorf("the null swallowed the change beside it:\n%s", out)
	}
}

func TestTheStartingPointDoesNotClaimNothingHasChanged(t *testing.T) {
	// The first call names no token, so it has no page of changes to be
	// empty. "nothing has changed since that token" reads as a completed
	// poll, which is the one thing this answer is not.
	svc, _ := setup(t, service.Options{})
	out, err := svc.ListChanges(t.Context(), service.ListChangesInput{})
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if strings.Contains(out, "nothing has changed since that token") {
		t.Errorf("the starting point reads as a completed poll:\n%s", out)
	}
	if !strings.Contains(out, "starting point") {
		t.Errorf("the starting point does not say what it is:\n%s", out)
	}
}

func TestPinningARevisionThroughAShortcutKeepsTheContext(t *testing.T) {
	// The path that changed something used to render a poorer card than
	// the path that changed nothing: pin through a shortcut and the card
	// did not say a shortcut had been followed, then the same call again
	// suddenly did.
	svc, fake := setup(t, service.Options{})
	fake.SetContent("id-budget-fixture", "first")
	fake.SetContent("id-budget-fixture", "second")
	rev := fake.Revisions["id-budget-fixture"][0].ID

	got, err := svc.ManageRevision(t.Context(), service.ManageRevisionInput{
		File: "id-shortcut-fixture", Revision: rev, Action: "keep",
	})
	if err != nil {
		t.Fatalf("ManageRevision: %v", err)
	}
	if !strings.Contains(got.Text, "followed the shortcut") {
		t.Errorf("the card does not say a shortcut was followed:\n%s", got.Text)
	}
	again, err := svc.ManageRevision(t.Context(), service.ManageRevisionInput{
		File: "id-shortcut-fixture", Revision: rev, Action: "keep",
	})
	if err != nil {
		t.Fatalf("ManageRevision: %v", err)
	}
	if strings.Contains(got.Text, "followed the shortcut") != strings.Contains(again.Text, "followed the shortcut") {
		t.Errorf("the same input rendered differently the second time:\n%s\n---\n%s", got.Text, again.Text)
	}
}
