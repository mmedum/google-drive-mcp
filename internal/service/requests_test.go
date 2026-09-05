package service_test

import (
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/config"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

func TestListAccessRequestsShowsWhoIsWaitingAndForWhat(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddProposal("id-budget-fixture", "id-request-1", "alice@example.com", "writer")

	out, err := svc.ListAccessRequests(t.Context(), service.ListAccessRequestsInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("ListAccessRequests: %v", err)
	}
	for _, want := range []string{"1 pending access request", "id-request-1", "alice@example.com", "can edit"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not carry %q:\n%s", want, out)
		}
	}
}

func TestAccessRequestsAreReadableWithSharingOffAndUnanswerable(t *testing.T) {
	svc, fake := setup(t, service.Options{Sharing: config.SharingOff})
	fake.AddProposal("id-budget-fixture", "id-request-1", "alice@example.com", "writer")

	out, err := svc.ListAccessRequests(t.Context(), service.ListAccessRequestsInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("ListAccessRequests: %v", err)
	}
	if !strings.Contains(out, "sharing switched off") {
		t.Errorf("the listing does not say the deployment cannot answer these:\n%s", out)
	}
	_, err = svc.ResolveAccessRequest(t.Context(), service.ResolveAccessRequestInput{
		File: "id-budget-fixture", Request: "id-request-1", Action: "accept",
	})
	if err == nil {
		t.Fatal("a request was accepted with GDRIVE_SHARING=off")
	}
	if !strings.Contains(err.Error(), "GDRIVE_SHARING=off") {
		t.Errorf("the refusal does not name the setting: %v", err)
	}
	if len(fake.Permissions["id-budget-fixture"]) != 0 {
		t.Error("the refusal still granted something")
	}
}

func TestAcceptingGrantsTheRoleAskedForAndReportsExposure(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddProposal("id-budget-fixture", "id-request-1", "alice@example.com", "commenter")

	got, err := svc.ResolveAccessRequest(t.Context(), service.ResolveAccessRequestInput{
		File: "id-budget-fixture", Request: "id-request-1", Action: "accept",
	})
	if err != nil {
		t.Fatalf("ResolveAccessRequest: %v", err)
	}
	perms := fake.Permissions["id-budget-fixture"]
	if len(perms) != 1 || perms[0].EmailAddress != "alice@example.com" || perms[0].Role != "commenter" {
		t.Fatalf("permissions = %+v", perms)
	}
	if got.JSON.Action != "shared" {
		t.Errorf("action = %q, want shared", got.JSON.Action)
	}
	// The resolve call answers with no body at all, so the exposure after
	// can only come from reading it back. That it is there is the proof
	// the read happened.
	if got.JSON.SharingAfter == "" || got.JSON.SharingAfter == got.JSON.SharingBefore {
		t.Errorf("before %q, after %q", got.JSON.SharingBefore, got.JSON.SharingAfter)
	}
	if !strings.Contains(got.Text, "No mail was sent") {
		t.Errorf("the result does not say whether anybody was mailed:\n%s", got.Text)
	}
	if len(fake.Proposals["id-budget-fixture"]) != 0 {
		t.Error("the request is still pending after being accepted")
	}
}

func TestARequestNamingSeveralRolesIsRefusedRatherThanGuessedAt(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddProposal("id-budget-fixture", "id-request-1", "alice@example.com", "reader", "writer")

	_, err := svc.ResolveAccessRequest(t.Context(), service.ResolveAccessRequestInput{
		File: "id-budget-fixture", Request: "id-request-1", Action: "accept",
	})
	if err == nil {
		t.Fatal("a request asking for two roles was accepted as one of them")
	}
	if !strings.Contains(err.Error(), "[ambiguous]") {
		t.Errorf("class = %v", err)
	}
	if len(fake.Permissions["id-budget-fixture"]) != 0 {
		t.Error("the refusal still granted something")
	}

	// Naming the role settles it.
	if _, err := svc.ResolveAccessRequest(t.Context(), service.ResolveAccessRequestInput{
		File: "id-budget-fixture", Request: "id-request-1", Action: "accept", Role: "reader",
	}); err != nil {
		t.Fatalf("ResolveAccessRequest with a role: %v", err)
	}
	perms := fake.Permissions["id-budget-fixture"]
	if len(perms) != 1 || perms[0].Role != "reader" {
		t.Errorf("permissions = %+v", perms)
	}
}

func TestDenyingRefusesTheRequestAndGrantsNothing(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddProposal("id-budget-fixture", "id-request-1", "alice@example.com", "writer")

	got, err := svc.ResolveAccessRequest(t.Context(), service.ResolveAccessRequestInput{
		File: "id-budget-fixture", Request: "id-request-1", Action: "deny",
	})
	if err != nil {
		t.Fatalf("ResolveAccessRequest: %v", err)
	}
	if got.JSON.Action != "denied" {
		t.Errorf("action = %q, want denied", got.JSON.Action)
	}
	if len(fake.Permissions["id-budget-fixture"]) != 0 {
		t.Error("denying a request granted access")
	}
	if len(fake.Proposals["id-budget-fixture"]) != 0 {
		t.Error("the request is still pending after being denied")
	}
}

func TestADryRunAnswersNobody(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddProposal("id-budget-fixture", "id-request-1", "alice@example.com", "writer")

	got, err := svc.ResolveAccessRequest(t.Context(), service.ResolveAccessRequestInput{
		File: "id-budget-fixture", Request: "id-request-1", Action: "accept", DryRun: true,
	})
	if err != nil {
		t.Fatalf("ResolveAccessRequest: %v", err)
	}
	if !got.JSON.DryRun {
		t.Error("the result does not say it was a dry run")
	}
	if len(fake.Permissions["id-budget-fixture"]) != 0 || len(fake.Proposals["id-budget-fixture"]) != 1 {
		t.Error("a dry run changed something")
	}
}

func TestAnAccessRequestCannotHandOverOwnership(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddProposal("id-budget-fixture", "id-request-1", "alice@example.com", "writer")

	_, err := svc.ResolveAccessRequest(t.Context(), service.ResolveAccessRequestInput{
		File: "id-budget-fixture", Request: "id-request-1", Action: "accept", Role: "owner",
	})
	if err == nil {
		t.Fatal("accepting a request handed the file over")
	}
	if !strings.Contains(err.Error(), "share_file") {
		t.Errorf("the refusal does not name the tool that does transfer ownership: %v", err)
	}
}

func TestAnswerNeedsARequestThatIsStillWaiting(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.ResolveAccessRequest(t.Context(), service.ResolveAccessRequestInput{
		File: "id-budget-fixture", Request: "id-nothing", Action: "accept",
	})
	if err == nil {
		t.Fatal("an id that is not pending was accepted")
	}
	if !strings.Contains(err.Error(), "list_access_requests") {
		t.Errorf("the refusal does not say where the ids come from: %v", err)
	}
}

func TestOnlyAnApproverIsToldWhoIsWaiting(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddProposal("id-budget-fixture", "id-request-1", "alice@example.com", "writer")
	fake.Files["id-budget-fixture"].Capabilities = &gdrive.Capabilities{CanEdit: true}

	_, err := svc.ListAccessRequests(t.Context(), service.ListAccessRequestsInput{File: "id-budget-fixture"})
	if err == nil {
		t.Fatal("a non-approver was shown the access requests")
	}
	if !strings.Contains(err.Error(), "[forbidden]") {
		t.Errorf("class = %v", err)
	}
	_, err = svc.ResolveAccessRequest(t.Context(), service.ResolveAccessRequestInput{
		File: "id-budget-fixture", Request: "id-request-1", Action: "accept",
	})
	if err == nil {
		t.Fatal("a non-approver answered an access request")
	}
}
