package service_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
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

// TestLockFileReportsWhatDriveDidRatherThanWhatWasAsked.
//
// lock_file is the consequence nobody expects from "start an approval",
// and it was reported straight from the argument: pass it and the result
// said the file was LOCKED and nobody could change its content. A live
// run put that sentence directly under a card carrying no restriction at
// all, and then changed the content on the next call. The sentence is
// read off the file now, so it is right whether or not Drive locks.
func TestLockFileReportsWhatDriveDidRatherThanWhatWasAsked(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	res, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-notes-fixture", Action: "start",
		Reviewers: []string{"one@example.com"}, LockFile: true,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(fake.Files["id-notes-fixture"].ContentRestrictions) != 0 {
		t.Fatal("the fake locked on lock_file, which Drive did not do")
	}
	if !strings.Contains(res.Text, "Drive reports no content restriction") {
		t.Errorf("an unapplied lock was not reported as unapplied:\n%s", res.Text)
	}
	if strings.Contains(res.Text, "nobody can change its content") {
		t.Errorf("the result promised a lock that is not on the file:\n%s", res.Text)
	}
}

// TestAnAppliedLockIsReportedAsOne is the other half: where Drive really
// does restrict the content on lock_file, the sentence has to be the
// flat one. One account is not every edition, and the server has to be
// right in both worlds.
func TestAnAppliedLockIsReportedAsOne(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.LockOnApprovalStart = true
	res, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-notes-fixture", Action: "start",
		Reviewers: []string{"one@example.com"}, LockFile: true,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(res.Text, "nobody can change its content") {
		t.Errorf("a real lock was not reported as one:\n%s", res.Text)
	}
}

// TestALockThatWasAlreadyThereIsNotCredited keeps the sentence from
// claiming a cause it cannot know. A file restricted before the call —
// an earlier approval that was approved, or an administrator — must not
// have that lock attributed to the approval just started, which is the
// same "assert rather than observe" mistake one level down.
func TestALockThatWasAlreadyThereIsNotCredited(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Files["id-notes-fixture"].ContentRestrictions = []*gdrive.ContentRestriction{
		{ReadOnly: true, Reason: "Locked by somebody else"},
	}
	res, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-notes-fixture", Action: "start",
		Reviewers: []string{"one@example.com"}, LockFile: true,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(res.Text, "as it already was before this call") {
		t.Errorf("a pre-existing lock was credited to this approval:\n%s", res.Text)
	}
}

// TestApprovingLocksTheFile is the state §17a said needed a shared drive
// to reach, and the live run reached it: an approved file comes back
// carrying a content restriction, and the next content change is refused.
func TestApprovingLocksTheFile(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddApproval("id-budget-fixture", "id-approval-yes", "person@example.com")
	res, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-budget-fixture", Action: "approve", Approval: "id-approval-yes",
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if len(fake.Files["id-budget-fixture"].ContentRestrictions) == 0 {
		t.Fatal("approving did not lock the file")
	}
	if !strings.Contains(res.Text, "content locked") {
		t.Errorf("the card does not report the lock approving applied:\n%s", res.Text)
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

// Drive removes a reviewer only by naming their replacement, which the
// request's own description says in as many words: "Reviewers can be
// added or replaced, but not removed."
func TestReassignAddsAndReplacesReviewers(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddApproval("id-notes-fixture", "id-approval-1", "first@example.com")

	if _, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-notes-fixture", Action: "reassign", Approval: "id-approval-1",
		Reviewers: []string{"second@example.com"},
	}); err != nil {
		t.Fatalf("adding a reviewer: %v", err)
	}
	if _, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-notes-fixture", Action: "reassign", Approval: "id-approval-1",
		ReplaceReviewers: []string{"first@example.com=third@example.com"},
	}); err != nil {
		t.Fatalf("replacing a reviewer: %v", err)
	}
	got := map[string]bool{}
	for _, r := range fake.Approvals["id-notes-fixture"][0].ReviewerResponses {
		got[r.Reviewer.EmailAddress] = true
	}
	if got["first@example.com"] {
		t.Error("the replaced reviewer is still on the approval")
	}
	for _, want := range []string{"second@example.com", "third@example.com"} {
		if !got[want] {
			t.Errorf("%s is not on the approval: %v", want, got)
		}
	}
}

// A replacement needs both halves. A flat address could not say whether
// it was the person going or the person arriving, and Drive answers 400
// to a body missing either.
func TestReassignRefusesAHalfWrittenReplacement(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddApproval("id-notes-fixture", "id-approval-1", "first@example.com")
	for _, pair := range []string{"first@example.com", "first@example.com=", "=third@example.com"} {
		_, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
			File: "id-notes-fixture", Action: "reassign", Approval: "id-approval-1",
			ReplaceReviewers: []string{pair},
		})
		if err == nil {
			t.Errorf("%q was accepted as a replacement", pair)
			continue
		}
		if !strings.Contains(err.Error(), "Both halves are required") {
			t.Errorf("the refusal for %q does not say what is missing: %v", pair, err)
		}
	}
}

func TestReassignNeedsSomebodyToAddOrReplace(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddApproval("id-notes-fixture", "id-approval-1", "person@example.com")
	_, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-notes-fixture", Action: "reassign", Approval: "id-approval-1",
	})
	if err == nil {
		t.Fatal("a reassign with nobody to add or replace was sent to Drive")
	}
	if !strings.Contains(err.Error(), "will not simply REMOVE") {
		t.Errorf("the refusal does not say what Drive will not do: %v", err)
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

// TestAnsweringAFinishedApprovalSaysThatIsPossible holds the message to
// the case the live run actually produced.
//
// The driver cancelled an approval and then answered it. Drive refuses
// that with a bare Permission denied — the same answer it gives someone
// who is not a reviewer and someone with no write access — so the
// message has to offer all three. It named only the other two, and the
// account it was telling to check the reviewer list was on it, which
// leaves a reader with nothing to try.
func TestAnsweringAFinishedApprovalSaysThatIsPossible(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddApproval("id-notes-fixture", "id-approval-gone", "person@example.com")
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if strings.Contains(r.URL.Path, "/approvals/") && r.Method == http.MethodPost {
			return &drivetest.Failure{Status: http.StatusForbidden,
				Reason: "insufficientFilePermissions", Message: "Permission denied."}
		}
		return nil
	}

	_, err := svc.ManageApproval(t.Context(), service.ManageApprovalInput{
		File: "id-notes-fixture", Action: "approve", Approval: "id-approval-gone",
	})
	if err == nil {
		t.Fatal("answering a refused approval succeeded")
	}
	msg := err.Error()
	for _, want := range []string{"already approved, declined or cancelled", "list_approvals"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, msg)
		}
	}
}

// TestARefusedListingDoesNotBlameAFinishedApproval keeps the message
// above to the calls it can be true of.
//
// approvalError serves all four call sites. A listing and a start name
// no approval, so neither can be refused for one being finished — and
// telling a caller whose list_approvals just failed that "list_approvals
// says which of those it is" points at the call that failed.
func TestARefusedListingDoesNotBlameAFinishedApproval(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if strings.Contains(r.URL.Path, "/approvals") {
			return &drivetest.Failure{Status: http.StatusForbidden,
				Reason: "insufficientFilePermissions", Message: "Permission denied."}
		}
		return nil
	}

	_, err := svc.ListApprovals(t.Context(), service.ListApprovalsInput{File: "id-notes-fixture"})
	if err == nil {
		t.Fatal("a refused listing succeeded")
	}
	msg := err.Error()
	if strings.Contains(msg, "already approved, declined or cancelled") {
		t.Errorf("a listing was blamed on a finished approval:\n%s", msg)
	}
	if strings.Contains(msg, "list_approvals says") {
		t.Errorf("a failed listing was told to call list_approvals:\n%s", msg)
	}
}
