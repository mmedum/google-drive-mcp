package gapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

func TestAccessProposalsAreListedAndResolved(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	s.AddProposal("id-budget-fixture", "id-request-1", "alice@example.com", "writer")
	s.AddProposal("id-budget-fixture", "id-request-2", "bob@example.com", "reader", "commenter")
	c := drivetest.Client(t, s)

	proposals, err := c.ListAccessProposals(t.Context(), "id-budget-fixture")
	if err != nil {
		t.Fatalf("ListAccessProposals: %v", err)
	}
	if len(proposals) != 2 {
		t.Fatalf("listed %d proposals, want 2", len(proposals))
	}
	// A proposal can ask for more than one role, which is the shape the
	// service refuses to choose between.
	if len(proposals[1].RolesAndViews) != 2 {
		t.Errorf("second proposal = %+v, want the two roles it asked for", proposals[1].RolesAndViews)
	}

	// Accepting grants the permission and answers with nothing at all,
	// so the only way to know what it did is to go and look.
	if err := c.ResolveAccessProposal(t.Context(), "id-budget-fixture", "id-request-1", &gdrive.ResolveProposal{
		Action: gdrive.ProposalAccept, Role: []string{"writer"},
	}); err != nil {
		t.Fatalf("ResolveAccessProposal accept: %v", err)
	}
	perms, err := c.ListPermissions(t.Context(), "id-budget-fixture")
	if err != nil {
		t.Fatalf("ListPermissions: %v", err)
	}
	granted := false
	for _, p := range perms {
		if p.EmailAddress == "alice@example.com" && p.Role == "writer" {
			granted = true
		}
	}
	if !granted {
		t.Errorf("accepting a request granted nothing: %+v", perms)
	}

	if err := c.ResolveAccessProposal(t.Context(), "id-budget-fixture", "id-request-2", &gdrive.ResolveProposal{
		Action: gdrive.ProposalDeny,
	}); err != nil {
		t.Fatalf("ResolveAccessProposal deny: %v", err)
	}
	left, err := c.ListAccessProposals(t.Context(), "id-budget-fixture")
	if err != nil {
		t.Fatalf("ListAccessProposals after resolving: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("%d proposals are still pending after both were answered", len(left))
	}
}

// TestAcceptingWithoutARoleIsRefused holds the reference's own rule: the
// role field is required for ACCEPT, and a request that omits it is a
// 400 rather than a grant of some default.
func TestAcceptingWithoutARoleIsRefused(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	s.AddProposal("id-budget-fixture", "id-request-1", "alice@example.com", "writer")
	c := drivetest.Client(t, s)
	err := c.ResolveAccessProposal(t.Context(), "id-budget-fixture", "id-request-1",
		&gdrive.ResolveProposal{Action: gdrive.ProposalAccept})
	if err == nil {
		t.Fatal("an acceptance with no role was accepted")
	}
	if gapi.Class(err) != gapi.ClassInvalid {
		t.Errorf("class = %q, want invalid", gapi.Class(err))
	}
}

// TestResolveIsNeverRepeated is the sharing rule of §11 at the one
// endpoint phase 3 adds to it: a resolve that fails ambiguously may
// already have granted the access, so it is reported rather than sent
// again.
func TestResolveIsNeverRepeated(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	s.AddProposal("id-budget-fixture", "id-request-1", "alice@example.com", "writer")
	s.Fail = drivetest.FailTimes(1, "accessproposals", drivetest.Failure{
		Status: http.StatusInternalServerError, Reason: "backendError", Message: "backend error",
	})
	c := drivetest.Client(t, s)
	if err := c.ResolveAccessProposal(t.Context(), "id-budget-fixture", "id-request-1",
		&gdrive.ResolveProposal{Action: gdrive.ProposalAccept, Role: []string{"writer"}}); err == nil {
		t.Fatal("a resolve was retried through a 500 and reported success")
	}
	posts := 0
	for _, r := range s.Requested() {
		if r.Method == http.MethodPost && strings.Contains(r.Path, "accessproposals") {
			posts++
		}
	}
	if posts != 1 {
		t.Errorf("the resolve was attempted %d times; a sharing POST may be attempted once", posts)
	}
}

// TestOnlyAnApproverSeesRequests is Drive's documented refusal, which is
// the error this listing gets most often.
func TestOnlyAnApproverSeesRequests(t *testing.T) {
	s := drivetest.New()
	defer s.Close()
	drivetest.SmallTree(s)
	s.Files["id-budget-fixture"].Capabilities = &gdrive.Capabilities{CanEdit: true}
	c := drivetest.Client(t, s)
	_, err := c.ListAccessProposals(t.Context(), "id-budget-fixture")
	if err == nil {
		t.Fatal("a non-approver listed the access requests")
	}
	if gapi.Class(err) != gapi.ClassForbidden {
		t.Errorf("class = %q, want forbidden", gapi.Class(err))
	}
}
