package drivetest

import (
	"net/http"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// AddProposal places a pending request for access to a file, the way
// somebody who was refused places one. The API offers no way to create
// one, so this is a fixture only.
func (s *Server) AddProposal(fileID, id, requester string, roles ...string) *gdrive.AccessProposal {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := &gdrive.AccessProposal{
		ProposalID: id, FileID: fileID, RequesterEmailAddress: requester,
		RecipientEmailAddress: requester, CreateTime: s.now().UTC().Format(time.RFC3339),
		RequestMessage: "please let me in",
	}
	for _, role := range roles {
		p.RolesAndViews = append(p.RolesAndViews, &gdrive.AccessProposalRoleAndView{Role: role})
	}
	s.Proposals[fileID] = append(s.Proposals[fileID], p)
	return p
}

// serveProposals routes the access proposal endpoints under one file.
func (s *Server) serveProposals(w http.ResponseWriter, r *http.Request, fileID, rest string) {
	s.mu.Lock()
	f := s.fileLocked(fileID)
	s.mu.Unlock()
	if f == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+fileID+".")
		return
	}
	// Only an approver may see or resolve a proposal, and Drive answers
	// 403 for everybody else. The fake reads canShare as the approver
	// test, which is the capability the same account needs to grant the
	// access an acceptance grants.
	if f.Capabilities != nil && !f.Capabilities.CanShare {
		s.errorJSON(w, http.StatusForbidden, "insufficientFilePermissions",
			"The user is not an approver for this file.")
		return
	}
	switch {
	case rest == "" && r.Method == http.MethodGet:
		s.handleListProposals(w, fileID)
	case strings.HasSuffix(rest, ":resolve") && r.Method == http.MethodPost:
		s.handleResolveProposal(w, r, fileID, strings.TrimSuffix(rest, ":resolve"))
	default:
		s.errorJSON(w, http.StatusNotFound, "notFound",
			"the fake does not implement "+r.Method+" on an access proposal")
	}
}

func (s *Server) handleListProposals(w http.ResponseWriter, fileID string) {
	s.mu.Lock()
	out := gdrive.AccessProposalList{AccessProposals: append([]*gdrive.AccessProposal{}, s.Proposals[fileID]...)}
	s.mu.Unlock()
	writeJSON(w, out)
}

// handleResolveProposal accepts or denies a request. Accepting grants
// the permission the proposal asked for, which is the whole reason this
// endpoint is a sharing write; either way the proposal is gone
// afterwards, and Drive answers with NO BODY AT ALL.
func (s *Server) handleResolveProposal(w http.ResponseWriter, r *http.Request, fileID, proposalID string) {
	var body gdrive.ResolveProposal
	if !s.decodeJSON(w, r, &body) {
		return
	}
	switch body.Action {
	case gdrive.ProposalAccept, gdrive.ProposalDeny:
	default:
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Invalid Value for action: "+body.Action)
		return
	}
	if body.Action == gdrive.ProposalAccept && len(body.Role) == 0 {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "The role field is required for the ACCEPT action.")
		return
	}
	s.mu.Lock()
	p := s.proposalLocked(fileID, proposalID)
	if p == nil {
		s.mu.Unlock()
		s.errorJSON(w, http.StatusNotFound, "notFound", "Access proposal not found: "+proposalID+".")
		return
	}
	if body.Action == gdrive.ProposalAccept {
		s.grantLocked(fileID, &gdrive.Permission{
			Type: "user", Role: body.Role[0], EmailAddress: p.RecipientEmailAddress,
		})
	}
	left := s.Proposals[fileID][:0]
	for _, other := range s.Proposals[fileID] {
		if other.ProposalID != proposalID {
			left = append(left, other)
		}
	}
	s.Proposals[fileID] = left
	s.mu.Unlock()
	// 200 with an empty body, as the discovery document's missing
	// response type says.
	w.WriteHeader(http.StatusOK)
}

// proposalLocked finds one proposal on a file. The caller holds the lock.
func (s *Server) proposalLocked(fileID, proposalID string) *gdrive.AccessProposal {
	for _, p := range s.Proposals[fileID] {
		if p.ProposalID == proposalID {
			return p
		}
	}
	return nil
}
