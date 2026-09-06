package service_test

import (
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/service"
)

func TestListApprovalsSaysWhenItIsWaitingOnYou(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddApproval("id-notes-fixture", "id-approval-1", "person@example.com", "other@example.com")

	out, err := svc.ListApprovals(t.Context(), service.ListApprovalsInput{File: "id-notes-fixture"})
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	// The one thing a reader has to act on is said in the head and again
	// on the entry, rather than left to be worked out from responses.
	for _, want := range []string{"1 waiting on you", "WAITING ON YOU", "id-approval-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not carry %q:\n%s", want, out)
		}
	}
}

func TestListApprovalsOnAFileWithNone(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.ListApprovals(t.Context(), service.ListApprovalsInput{File: "id-notes-fixture"})
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if !strings.Contains(out, "no approvals") {
		t.Errorf("a file with no approvals did not say so:\n%s", out)
	}
}

// approvals.list returns a minimal response without an explicit fields
// parameter — the same trap the comment endpoints have, found in the
// discovery document this time rather than in a live run. A caller that
// forgets reads an empty list and believes there are no approvals.
func TestApprovalsAlwaysAskForFields(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddApproval("id-notes-fixture", "id-approval-1", "person@example.com")

	if _, err := svc.ListApprovals(t.Context(), service.ListApprovalsInput{File: "id-notes-fixture"}); err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	if got := lastQuery(t, fake, "GET", "/approvals", "fields"); got == "" {
		t.Error("approvals.list was called with no fields parameter, which returns a minimal response")
	}
}

func TestStartingAnApprovalNeedsSomebodyToAnswerIt(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-notes-fixture", Action: "start",
	})
	if err == nil {
		t.Fatal("an approval with no reviewers was sent to Drive")
	}
	if !strings.Contains(err.Error(), "reviewers") {
		t.Errorf("the refusal does not name the missing argument: %v", err)
	}
}

// Every approval action mails somebody and there is no way to turn it
// off. A result that did not say so would leave the caller to find out
// from the recipients.
func TestStartingAnApprovalSaysThatItMailedPeople(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	res, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-notes-fixture", Action: "start",
		Reviewers: []string{"one@example.com", "two@example.com"},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(res.Text, "mailed") {
		t.Errorf("the result does not say that reviewers were mailed:\n%s", res.Text)
	}
}

// lock_file is the consequence nobody expects from "start an approval",
// so it has to be reported in the words that describe it.
func TestLockingTheFileForAnApprovalIsReported(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	res, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-notes-fixture", Action: "start",
		Reviewers: []string{"one@example.com"}, LockFile: true,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(res.Text, "LOCKED") {
		t.Errorf("the result does not say the file is locked:\n%s", res.Text)
	}
	if len(fake.Files["id-notes-fixture"].ContentRestrictions) == 0 {
		t.Error("the file was not actually locked")
	}
}

// One decline decides an approval; an approval waits for everybody. The
// asymmetry is the thing a caller most needs told.
func TestDecliningCompletesTheApprovalAndApprovingWaits(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	fake.AddApproval("id-notes-fixture", "id-approval-wait", "person@example.com", "other@example.com")
	res, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-notes-fixture", Action: "approve", Approval: "id-approval-wait",
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !strings.Contains(res.Text, "still open") {
		t.Errorf("approving with another reviewer outstanding did not report it as open:\n%s", res.Text)
	}

	fake.AddApproval("id-budget-fixture", "id-approval-no", "person@example.com", "other@example.com")
	res, err = svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-budget-fixture", Action: "decline", Approval: "id-approval-no",
	})
	if err != nil {
		t.Fatalf("decline: %v", err)
	}
	if !strings.Contains(res.Text, "declined") {
		t.Errorf("declining did not complete the approval:\n%s", res.Text)
	}
}

func TestCommentingOnAnApprovalNeedsSomethingToSay(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddApproval("id-notes-fixture", "id-approval-1", "person@example.com")
	_, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-notes-fixture", Action: "comment", Approval: "id-approval-1",
	})
	if err == nil {
		t.Fatal("an empty comment was sent to Drive, which mails everybody for nothing")
	}
}

// Drive offers no way to remove a reviewer. Saying so beats offering an
// argument that always fails.
func TestReassignSaysThatDriveCannotRemoveAReviewer(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddApproval("id-notes-fixture", "id-approval-1", "person@example.com")
	_, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-notes-fixture", Action: "reassign", Approval: "id-approval-1",
	})
	if err == nil {
		t.Fatal("a reassign with nobody to add or replace was sent to Drive")
	}
	if !strings.Contains(err.Error(), "does not remove") {
		t.Errorf("the refusal does not say what Drive cannot do: %v", err)
	}
}

func TestApprovalsAreRefusedOnAFolder(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-projects-fixture", Action: "start", Reviewers: []string{"one@example.com"},
	})
	if err == nil {
		t.Error("an approval was started on a folder")
	}
}

func TestManageApprovalRefusesAnActionItDoesNotHave(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-notes-fixture", Action: "rubber-stamp", Approval: "id-approval-1",
	})
	if err == nil {
		t.Fatal("an action that does not exist was accepted")
	}
	if !strings.Contains(err.Error(), "start") {
		t.Errorf("the refusal does not list the actions that do exist: %v", err)
	}
}
