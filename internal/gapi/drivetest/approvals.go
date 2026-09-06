package drivetest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// The approval endpoints have the same trap the comment ones do: the
// discovery document says approvals.list returns a minimal response
// without an explicit fields parameter. The fake enforces it, so a
// caller who forgets fails here rather than reading an empty list in
// production and believing there are no approvals.

// serveApprovals routes everything under one file's approvals: the
// collection, the colon-suffixed start verb, one approval, and the five
// verbs on it.
func (s *Server) serveApprovals(w http.ResponseWriter, r *http.Request, fileID, rest string) {
	if _, ok := s.Files[fileID]; !ok {
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+fileID+".")
		return
	}
	// rest is "" for the collection, "start" for the collection's own
	// verb, "{id}" for one approval, and "{id}:{verb}" for an action on
	// one.
	id, verb, hasVerb := strings.Cut(rest, ":")
	switch {
	case rest == "" && r.Method == http.MethodGet:
		s.handleListApprovals(w, r, fileID)
	case rest == "start" && r.Method == http.MethodPost:
		s.handleStartApproval(w, r, fileID)
	case hasVerb && r.Method == http.MethodPost:
		s.handleApprovalVerb(w, r, fileID, id, verb)
	case rest != "" && !hasVerb && r.Method == http.MethodGet:
		s.handleGetApproval(w, r, fileID, rest)
	default:
		s.errorJSON(w, http.StatusNotFound, "notFound",
			"the fake does not implement "+r.Method+" on approvals/"+rest)
	}
}

// requireFields refuses a request that did not name the fields it wants,
// which is what the discovery document says these endpoints need.
func (s *Server) requireFields(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Query().Get("fields") == "" {
		s.errorJSON(w, http.StatusBadRequest, "badRequest",
			"this endpoint returns a minimal response without an explicit fields parameter")
		return false
	}
	return true
}

func (s *Server) handleListApprovals(w http.ResponseWriter, r *http.Request, fileID string) {
	if !s.requireFields(w, r) {
		return
	}
	s.mu.Lock()
	all := append([]*gdrive.Approval(nil), s.Approvals[fileID]...)
	s.mu.Unlock()

	size := gdriveApprovalPage(r.URL.Query().Get("pageSize"))
	if len(all) > size {
		all = all[:size]
	}
	writeJSON(w, gdrive.ApprovalList{Items: all})
}

func gdriveApprovalPage(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 20
	}
	if n > 100 {
		return 100
	}
	return n
}

func (s *Server) handleGetApproval(w http.ResponseWriter, r *http.Request, fileID, approvalID string) {
	if !s.requireFields(w, r) {
		return
	}
	a := s.approval(fileID, approvalID)
	if a == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "Approval not found.")
		return
	}
	writeJSON(w, a)
}

// handleApprovalVerb serves start, approve, decline, cancel, comment and
// reassign.
func (s *Server) handleApprovalVerb(w http.ResponseWriter, r *http.Request, fileID, approvalID, verb string) {
	if !s.requireFields(w, r) {
		return
	}
	a := s.approval(fileID, approvalID)
	if a == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "Approval not found.")
		return
	}
	var body gdrive.ReassignApproval
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.errorJSON(w, http.StatusBadRequest, "badRequest", "Invalid request body.")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	switch verb {
	case "approve", "decline":
		if a.Status != gdriveInProgress {
			s.errorJSON(w, http.StatusBadRequest, "badRequest",
				"This approval is already complete.")
			return
		}
		s.answerLocked(a, verb)
	case "cancel":
		if a.Status != gdriveInProgress {
			s.errorJSON(w, http.StatusBadRequest, "badRequest",
				"This approval is already complete.")
			return
		}
		a.Status = "CANCELLED"
		a.CompleteTime = s.now().UTC().Format(gdriveTimeLayout)
	case "comment":
		if strings.TrimSpace(body.Message) == "" {
			s.errorJSON(w, http.StatusBadRequest, "badRequest", "A message is required.")
			return
		}
	case "reassign":
		for _, address := range append(body.AddReviewers, body.ReplaceReviewers...) {
			a.ReviewerResponses = append(a.ReviewerResponses, &gdrive.ReviewerResponse{
				Reviewer: &gdrive.User{EmailAddress: address}, Response: "NO_RESPONSE",
			})
		}
		if len(body.ReplaceReviewers) > 0 {
			a.ReviewerResponses = a.ReviewerResponses[len(a.ReviewerResponses)-len(body.ReplaceReviewers):]
		}
	default:
		s.errorJSON(w, http.StatusNotFound, "notFound", "the fake does not implement the verb "+verb)
		return
	}
	a.ModifyTime = s.now().UTC().Format(gdriveTimeLayout)
	writeJSON(w, a)
}

const (
	gdriveInProgress = "IN_PROGRESS"
	gdriveTimeLayout = "2006-01-02T15:04:05Z07:00"
)

// answerLocked records the signed-in account's answer and completes the
// approval when that settles it. One decline decides it; an approval
// waits for everybody, which is the asymmetry worth having in the fake.
func (s *Server) answerLocked(a *gdrive.Approval, verb string) {
	response := "APPROVED"
	if verb == "decline" {
		response = "DECLINED"
	}
	for _, r := range a.ReviewerResponses {
		if r.Reviewer != nil && r.Reviewer.Me {
			r.Response = response
		}
	}
	if verb == "decline" {
		a.Status = "DECLINED"
		a.CompleteTime = s.now().UTC().Format(gdriveTimeLayout)
		return
	}
	for _, r := range a.ReviewerResponses {
		if r.Response != "APPROVED" {
			return
		}
	}
	a.Status = "APPROVED"
	a.CompleteTime = s.now().UTC().Format(gdriveTimeLayout)
}

func (s *Server) handleStartApproval(w http.ResponseWriter, r *http.Request, fileID string) {
	var body gdrive.StartApproval
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.errorJSON(w, http.StatusBadRequest, "badRequest", "Invalid request body.")
		return
	}
	if len(body.ReviewerEmails) == 0 {
		s.errorJSON(w, http.StatusBadRequest, "badRequest", "reviewerEmails is required.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	ts := s.now().UTC().Format(gdriveTimeLayout)
	a := &gdrive.Approval{
		ApprovalID:   fmt.Sprintf("id-approval-fixture-%d", s.nextID),
		TargetFileID: fileID,
		Initiator:    s.me(),
		Status:       gdriveInProgress,
		CreateTime:   ts,
		ModifyTime:   ts,
		DueTime:      body.DueTime,
		// The API's default behaviour, which is the one with the
		// consequence: a content change resets the answers, and once
		// approved the file is locked.
		FileContentChangeBehavior: "RESET_APPROVAL",
	}
	for _, address := range body.ReviewerEmails {
		reviewer := &gdrive.User{EmailAddress: address}
		if address == AccountEmail {
			reviewer = s.me()
		}
		a.ReviewerResponses = append(a.ReviewerResponses,
			&gdrive.ReviewerResponse{Reviewer: reviewer, Response: "NO_RESPONSE"})
	}
	if s.Approvals == nil {
		s.Approvals = map[string][]*gdrive.Approval{}
	}
	s.Approvals[fileID] = append(s.Approvals[fileID], a)
	if body.LockFile {
		if f := s.Files[fileID]; f != nil {
			f.ContentRestrictions = append(f.ContentRestrictions, &gdrive.ContentRestriction{
				ReadOnly: true, Reason: "Locked for an approval", Type: "globalContentRestriction",
			})
		}
	}
	writeJSON(w, a)
}

// approval finds one approval by id.
func (s *Server) approval(fileID, approvalID string) *gdrive.Approval {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.Approvals[fileID] {
		if a.ApprovalID == approvalID {
			return a
		}
	}
	return nil
}

// AddApproval puts an approval on a file as a fixture, with the given
// reviewers waiting. An address matching the signed-in account is marked
// as this account, so "waiting on you" can be exercised.
func (s *Server) AddApproval(fileID, approvalID string, reviewers ...string) *gdrive.Approval {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := s.now().UTC().Format(gdriveTimeLayout)
	a := &gdrive.Approval{
		ApprovalID: approvalID, TargetFileID: fileID,
		Initiator: &gdrive.User{DisplayName: "Other Person", EmailAddress: "other@example.com"},
		Status:    gdriveInProgress, CreateTime: ts, ModifyTime: ts,
		FileContentChangeBehavior: "RESET_APPROVAL",
	}
	for _, address := range reviewers {
		reviewer := &gdrive.User{EmailAddress: address}
		if address == AccountEmail {
			reviewer = s.me()
		}
		a.ReviewerResponses = append(a.ReviewerResponses,
			&gdrive.ReviewerResponse{Reviewer: reviewer, Response: "NO_RESPONSE"})
	}
	if s.Approvals == nil {
		s.Approvals = map[string][]*gdrive.Approval{}
	}
	s.Approvals[fileID] = append(s.Approvals[fileID], a)
	return a
}
