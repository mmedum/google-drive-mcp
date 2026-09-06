package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/render"
)

// Approvals are a review on a file: somebody asks named people to
// approve it, and Drive records who said what.
//
// Two things about them shape the tools. They MAIL people — every verb
// notifies, and there is no notify flag to turn that off, unlike
// sharing. And an approval can LOCK the file: lock_file does it for the
// duration, and the default behaviour on a content change resets the
// answers and locks the file once approved. Both are said in the tool
// descriptions and in the results, because neither is what "start an
// approval" sounds like it does.

// Approval actions manage_approval accepts.
const (
	ApprovalStart    = "start"
	ApprovalApprove  = "approve"
	ApprovalDecline  = "decline"
	ApprovalCancel   = "cancel"
	ApprovalComment  = "comment"
	ApprovalReassign = "reassign"
)

// ApprovalActions lists what manage_approval accepts, so the tool
// description offers the words the code takes.
func ApprovalActions() []string {
	return []string{ApprovalStart, ApprovalApprove, ApprovalDecline,
		ApprovalCancel, ApprovalComment, ApprovalReassign}
}

// ListApprovalsInput selects a file's approvals.
type ListApprovalsInput struct {
	File      string
	PageSize  int
	PageToken string
}

// ListApprovals reports the reviews on a file.
func (s *Service) ListApprovals(ctx context.Context, in ListApprovalsInput) (string, error) {
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return "", err
	}
	f := res.File
	size := in.PageSize
	if size > gapi.MaxApprovalPageSize {
		return "", Errorf(ClassInvalid, "page_size %d is above Drive's ceiling of %d for approvals",
			size, gapi.MaxApprovalPageSize)
	}
	page, err := s.api.ListApprovals(ctx, f.ID, gapi.ListApprovalsOptions{
		PageSize: size, PageToken: in.PageToken,
	})
	if err != nil {
		return "", s.approvalError(err, f, "reading the approvals on")
	}
	approvals := make([]*model.Approval, 0, len(page.Items))
	waiting := 0
	for _, a := range page.Items {
		converted := model.NewApproval(a)
		if converted == nil {
			continue
		}
		if converted.WaitingOnMe() {
			waiting++
		}
		approvals = append(approvals, converted)
	}
	return render.Approvals(approvals, render.ApprovalsOptions{
		Subject:       approvalSubject(f, len(approvals), waiting),
		Location:      s.Location(ctx, f).String(),
		Now:           s.now(),
		NextPageToken: page.NextPageToken,
	}), nil
}

// approvalSubject heads the listing with the count that decides whether
// there is anything to do.
func approvalSubject(f *gdrive.File, total, waiting int) string {
	head := fmt.Sprintf("%s — %s: %s", f.Name, model.Kind(f), model.Plural(total, "approval", "approvals"))
	if waiting > 0 {
		return fmt.Sprintf("%s, %d waiting on you", head, waiting)
	}
	return head
}

// ManageApprovalInput is one action on one approval.
type ManageApprovalInput struct {
	File     string
	Approval string
	Action   string
	// Reviewers are the addresses to ask, for start, and to add, for
	// reassign.
	Reviewers []string
	// ReplaceReviewers swaps the reviewers out rather than adding to
	// them, for reassign.
	ReplaceReviewers []string
	Message          string
	// LockFile locks the file's content while the approval is open.
	LockFile bool
	// Due is when the approval is wanted by, as a date or a timestamp.
	Due string
}

// ManageApproval starts a review, answers one, withdraws one, comments
// on one, or changes who is being asked.
func (s *Service) ManageApproval(ctx context.Context, in ManageApprovalInput) (*Result, error) {
	if err := s.writable("manage_approval"); err != nil {
		return nil, err
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: true, Fresh: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	if f.IsFolder() {
		return nil, Errorf(ClassUnsupported,
			"approvals are on files, and %s is a folder", f.Name)
	}

	approval, note, err := s.approvalAction(ctx, f, action, in)
	if err != nil {
		return nil, err
	}
	converted := model.NewApproval(approval)
	after, err := s.Resolve(ctx, f.ID, ResolveOptions{FollowShortcut: false, Fresh: true})
	if err != nil {
		// The action landed; failing here would report a failure that did
		// not happen. Report what is known instead.
		after = res
	}
	s.forget(f, false)
	return s.report(ctx, after, outcome{
		Action: approvalOutcome(action),
		Note:   note + " " + approvalState(converted),
	}), nil
}

// approvalAction performs one action and says what it did. The message
// it returns is written here rather than in the renderer because what
// happened is a fact about the call, not about the resource.
func (s *Service) approvalAction(ctx context.Context, f *gdrive.File, action string,
	in ManageApprovalInput) (*gdrive.Approval, string, error) {
	switch action {
	case ApprovalStart:
		return s.startApproval(ctx, f, in)
	case ApprovalApprove, ApprovalDecline, ApprovalCancel, ApprovalComment:
		return s.answerApproval(ctx, f, action, in)
	case ApprovalReassign:
		return s.reassignApproval(ctx, f, in)
	case "":
		return nil, "", Errorf(ClassInvalid, "action is required: one of %s",
			strings.Join(ApprovalActions(), ", "))
	}
	return nil, "", Errorf(ClassInvalid, "action must be one of %s; got %q",
		strings.Join(ApprovalActions(), ", "), in.Action)
}

// startApproval opens a review.
func (s *Service) startApproval(ctx context.Context, f *gdrive.File, in ManageApprovalInput) (*gdrive.Approval, string, error) {
	reviewers := addresses(in.Reviewers)
	if len(reviewers) == 0 {
		return nil, "", Errorf(ClassInvalid,
			"reviewers is required to start an approval: an approval with nobody to answer it is not "+
				"a state Drive offers")
	}
	if f.Capabilities == nil || !f.Capabilities.CanEdit {
		return nil, "", Errorf(ClassForbidden,
			"you cannot start an approval on %s: that needs write access to the file", f.Name)
	}
	body := &gdrive.StartApproval{
		ReviewerEmails: reviewers, Message: in.Message, LockFile: in.LockFile,
	}
	if due := strings.TrimSpace(in.Due); due != "" {
		stamp, err := parseSearchDate(due)
		if err != nil {
			return nil, "", Errorf(ClassInvalid, "due: %v", err)
		}
		body.DueTime = stamp
	}
	started, err := s.api.StartApproval(ctx, f.ID, body)
	if err != nil {
		return nil, "", s.approvalError(err, f, "starting an approval on")
	}
	// The id is in the note because every other verb needs it, and this
	// is the only place it is ever handed out: there is no listing a
	// caller can find it in before the approval exists.
	note := fmt.Sprintf("Approval %s started on %s and %s been mailed about it.",
		started.ApprovalID, f.Name, model.Plural(len(reviewers), "one reviewer has", "reviewers have"))
	if in.LockFile {
		note += " The file is LOCKED while the approval is open: nobody can change its content, " +
			"including you."
	}
	return started, note, nil
}

// answerApproval records this account's answer, withdraws the approval,
// or adds a message to it.
func (s *Service) answerApproval(ctx context.Context, f *gdrive.File, action string,
	in ManageApprovalInput) (*gdrive.Approval, string, error) {
	id := strings.TrimSpace(in.Approval)
	if id == "" {
		return nil, "", Errorf(ClassInvalid,
			"approval is required for %s: the id list_approvals shows", action)
	}
	message := strings.TrimSpace(in.Message)
	if action == ApprovalComment && message == "" {
		return nil, "", Errorf(ClassInvalid,
			"message is required to comment on an approval; there is nothing else for the call to say")
	}
	body := &gdrive.ApprovalMessage{Message: message}
	var (
		out  *gdrive.Approval
		err  error
		note string
	)
	switch action {
	case ApprovalApprove:
		out, err = s.api.ApproveApproval(ctx, f.ID, id, body)
		note = "Your approval is recorded."
	case ApprovalDecline:
		out, err = s.api.DeclineApproval(ctx, f.ID, id, body)
		// Worth stating: one decline ends it, unlike an approval, which
		// waits for everybody.
		note = "You declined, which completes the approval: one refusal decides it, where an approval " +
			"waits for every reviewer."
	case ApprovalCancel:
		out, err = s.api.CancelApproval(ctx, f.ID, id, body)
		note = "The approval is withdrawn. Everybody who was asked has been told."
	case ApprovalComment:
		out, err = s.api.CommentApproval(ctx, f.ID, id, body)
		note = "Your message is on the approval, and the person who asked and every reviewer have been mailed it."
	}
	if err != nil {
		return nil, "", s.approvalError(err, f, action+" the approval on")
	}
	return out, note, nil
}

// reassignApproval changes who is being asked. The API adds and replaces
// but does not remove, and the tool says so rather than offering a
// removal that fails.
func (s *Service) reassignApproval(ctx context.Context, f *gdrive.File, in ManageApprovalInput) (*gdrive.Approval, string, error) {
	id := strings.TrimSpace(in.Approval)
	if id == "" {
		return nil, "", Errorf(ClassInvalid,
			"approval is required for reassign: the id list_approvals shows")
	}
	add, replace := addresses(in.Reviewers), addresses(in.ReplaceReviewers)
	if len(add) == 0 && len(replace) == 0 {
		return nil, "", Errorf(ClassInvalid,
			"reassign needs reviewers to add or replace_reviewers to swap in. Drive does not remove a "+
				"reviewer at all: cancel the approval and start another one instead")
	}
	out, err := s.api.ReassignApproval(ctx, f.ID, id, &gdrive.ReassignApproval{
		AddReviewers: add, ReplaceReviewers: replace, Message: in.Message,
	})
	if err != nil {
		return nil, "", s.approvalError(err, f, "reassigning the approval on")
	}
	note := "The reviewers changed and the new ones have been mailed."
	return out, note, nil
}

// approvalState says where the approval stands now, so a result reports
// the resource rather than only the action.
func approvalState(a *model.Approval) string {
	if a == nil {
		return ""
	}
	switch a.Status {
	case model.ApprovalApproved:
		return "The approval is complete: approved."
	case model.ApprovalDeclined:
		return "The approval is complete: declined."
	case model.ApprovalCancelled:
		return "The approval is cancelled."
	case model.ApprovalInProgress:
		return fmt.Sprintf("It is still open, waiting on %s.",
			model.Plural(a.Outstanding(), "1 reviewer", "reviewers"))
	}
	return ""
}

// approvalOutcome maps an action onto the vocabulary a write result uses.
func approvalOutcome(action string) render.Action {
	switch action {
	case ApprovalStart:
		return render.ActionCreated
	case ApprovalDecline:
		return render.ActionDenied
	case ApprovalComment:
		return render.ActionCommented
	case ApprovalCancel:
		return render.ActionUnshared
	default:
		return render.ActionUpdated
	}
}

// addresses trims and drops the empties from a list of email addresses.
func addresses(in []string) []string {
	out := make([]string, 0, len(in))
	for _, a := range in {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// approvalError names the one thing a caller cannot tell from the
// status: that this Workspace edition may not offer approvals at all.
// The endpoint is GA in the API and answers for any account; whether the
// feature is available is a property of the edition, and Drive says so
// with a refusal rather than with an absence.
func (s *Service) approvalError(err error, f *gdrive.File, doing string) error {
	switch gapi.Class(err) {
	case ClassNotFound, ClassUnsupported:
		return &Error{Class: ClassUnsupported, Message: fmt.Sprintf(
			"%s %s failed. Approvals are a Workspace feature and not every edition has them; "+
				"this can also mean the approval id is wrong. Google said: %s",
			doing, f.Name, gapi.Message(err)), Err: err}
	case ClassForbidden:
		return &Error{Class: ClassForbidden, Message: fmt.Sprintf(
			"%s %s was refused. Answering an approval needs to be one of its reviewers, and starting "+
				"or cancelling one needs write access to the file. Google said: %s",
			doing, f.Name, gapi.Message(err)), Err: err}
	}
	return wrap(err, doing+" "+f.Name)
}
