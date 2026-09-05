package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/config"
	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/render"
)

// ListAccessRequestsInput selects a file's pending requests.
type ListAccessRequestsInput struct {
	File string
}

// ListAccessRequests shows who is waiting to be let into a file. Only an
// approver can see them; Drive answers 403 for anybody else, and the API
// offers no way to make one — that happens when somebody is refused.
func (s *Service) ListAccessRequests(ctx context.Context, in ListAccessRequestsInput) (string, error) {
	// A shortcut is a file of its own with its own access, so requests
	// are read on the thing named rather than on what it points at.
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: false})
	if err != nil {
		return "", err
	}
	f := res.File
	proposals, err := s.api.ListAccessProposals(ctx, f.ID)
	if err != nil {
		return "", s.proposalListError(err, f)
	}
	reqs := make([]*model.AccessRequest, 0, len(proposals))
	for _, p := range proposals {
		if r := model.NewAccessRequest(p); r != nil {
			reqs = append(reqs, r)
		}
	}
	return render.AccessRequests(reqs, render.AccessRequestsOptions{
		Subject: fmt.Sprintf("%s — %s: %s", f.Name, model.Kind(f),
			model.Plural(len(reqs), "pending access request", "pending access requests")),
		Location:   s.Location(ctx, f).String(),
		Now:        s.now(),
		CanShare:   f.Capabilities == nil || f.Capabilities.CanShare,
		SharingOff: s.opts.Sharing == config.SharingOff,
	}), nil
}

// proposalListError explains the refusal this listing gets most: Drive
// shows access proposals to approvers only.
func (s *Service) proposalListError(err error, f *gdrive.File) error {
	if gapi.Class(err) == ClassForbidden {
		return &Error{Class: ClassForbidden, Message: fmt.Sprintf(
			"only somebody who can approve access to %s may see who has asked for it, and this account "+
				"cannot. Google said: %s", f.Name, gapi.Message(err)), Err: err}
	}
	return wrap(err, "reading who has asked for access to "+f.Name)
}

// Actions resolve_access_request accepts, in this server's spelling.
// Drive's own are ACCEPT and DENY, in upper case.
const (
	RequestAccept = "accept"
	RequestDeny   = "deny"
)

// RequestActions lists what resolve_access_request accepts.
func RequestActions() []string { return []string{RequestAccept, RequestDeny} }

// ResolveAccessRequestInput answers one pending request.
type ResolveAccessRequestInput struct {
	File    string
	Request string
	Action  string
	// Role is what to grant on an acceptance. Left out, the role the
	// requester asked for is granted — but only when they asked for
	// exactly one, because picking between several is a choice this
	// server does not make for the caller.
	Role string
	// Notify mails the requester. Drive does not document its own
	// default here, so this server always states it, and states false.
	Notify bool
	DryRun bool
}

// ResolveAccessRequest accepts or denies somebody's request for access.
//
// Accepting grants a permission, so this is a sharing write: it goes
// through the same gates as share_file — GDRIVE_SHARING=off removes it,
// capabilities.canShare is checked first, and the result shows who could
// see the file before and who can see it after.
func (s *Service) ResolveAccessRequest(ctx context.Context, in ResolveAccessRequestInput) (*Result, error) {
	if err := s.sharable("resolve_access_request"); err != nil {
		return nil, err
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	if action != RequestAccept && action != RequestDeny {
		return nil, Errorf(ClassInvalid, "action is required: one of %s", strings.Join(RequestActions(), ", "))
	}
	requestID := strings.TrimSpace(in.Request)
	if requestID == "" {
		return nil, Errorf(ClassInvalid, "request is required: list_access_requests shows the ids")
	}
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: false, Fresh: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	if f.Capabilities != nil && !f.Capabilities.CanShare {
		return nil, Errorf(ClassForbidden, "this account cannot change who may see %s, so it cannot answer "+
			"a request for access to it either.", f.Name)
	}

	// The proposal is read first for three reasons: to fail on an id that
	// is not there before anything is granted, to know the role the
	// person asked for, and to carry the view a published-view proposal
	// belongs to. Drive's resolve answers with no body at all, so
	// whatever is not read here cannot be reported afterwards.
	proposals, err := s.api.ListAccessProposals(ctx, f.ID)
	if err != nil {
		return nil, s.proposalListError(err, f)
	}
	var request *model.AccessRequest
	for _, p := range proposals {
		if r := model.NewAccessRequest(p); r != nil && r.ID == requestID {
			request = r
			break
		}
	}
	if request == nil {
		return nil, Errorf(ClassNotFound, "%s has no pending access request %s. list_access_requests shows "+
			"the ones that are still waiting; one that was already answered is gone.", f.Name, requestID)
	}

	role := ""
	if action == RequestAccept {
		if role, err = s.roleForRequest(request, in.Role); err != nil {
			return nil, err
		}
	} else if strings.TrimSpace(in.Role) != "" {
		return nil, Errorf(ClassInvalid, "role means nothing when denying a request; leave it out, or pass "+
			"action accept to grant that role.")
	}

	before := s.sharingNow(ctx, f)
	if in.DryRun {
		return s.requestResult(ctx, res, request, action, role, before, before, in.Notify, true), nil
	}

	body := &gdrive.ResolveProposal{
		Action: gdrive.ProposalDeny, View: request.View, SendNotification: in.Notify,
	}
	if action == RequestAccept {
		body.Action = gdrive.ProposalAccept
		body.Role = []string{role}
	}
	if err := s.api.ResolveAccessProposal(ctx, f.ID, requestID, body); err != nil {
		return nil, s.resolveRequestError(err, f, request, action)
	}
	s.forget(f, false)
	after := before
	if action == RequestAccept {
		// Only an acceptance changes who can reach the file, and reading
		// it back is the only way to know what it changed to: the resolve
		// call answers with nothing.
		res, after = s.rereadAfterSharing(ctx, res)
	}
	return s.requestResult(ctx, res, request, action, role, before, after, in.Notify, false), nil
}

// roleForRequest decides what an acceptance grants: the role the caller
// named, or the one the requester asked for when they asked for exactly
// one. A proposal naming several roles is an ambiguity, and this server
// never picks between candidates.
func (s *Service) roleForRequest(request *model.AccessRequest, asked string) (string, error) {
	if named := strings.TrimSpace(asked); named != "" {
		role, err := parseRole(named)
		if err != nil {
			return "", err
		}
		switch role {
		case model.RoleOwner:
			return "", Errorf(ClassUnsupported, "an access request cannot hand over ownership. share_file "+
				"with role owner and transfer_ownership: true is the only way to do that, and it is its own "+
				"decision.")
		case model.RoleReader, model.RoleCommenter, model.RoleWriter:
			return role, nil
		default:
			return "", Errorf(ClassInvalid, "an access request can be accepted as reader, commenter or "+
				"writer; %s is not one of them.", role)
		}
	}
	if role, ok := request.SoleRole(); ok {
		return role, nil
	}
	if len(request.Roles) == 0 {
		return "", Errorf(ClassInvalid, "%s asked for access without naming a role, so this call has to "+
			"name one: pass role as reader, commenter or writer.", request.By)
	}
	return "", Errorf(ClassAmbiguous, "%s asked for %s, and accepting has to grant exactly one of them. "+
		"Pass role to say which.", request.By, strings.Join(request.Roles, " or "))
}

// requestResult renders what answering the request did, with the
// exposure before and after for the same reason share_file carries it:
// the grant is the small half of the answer.
func (s *Service) requestResult(ctx context.Context, res *Resolved, request *model.AccessRequest,
	action, role string, before, after model.Sharing, notify, dryRun bool,
) *Result {
	out := outcome{DryRun: dryRun}
	if action == RequestAccept {
		out.Action = render.ActionShared
		out.Changes = append([]render.Change{{
			Field: "access for " + request.For, From: "asked for " + strings.Join(request.Roles, " or "),
			To: model.RoleWords(role),
		}}, render.Exposure(before, after)...)
		out.Note = s.requestNote(request, role, notify)
	} else {
		out.Action = render.ActionDenied
		out.Changes = []render.Change{{Field: "request " + request.ID + " from " + request.By,
			From: "waiting", To: "denied"}}
		out.Note = fmt.Sprintf("%s was refused and the request is gone; nobody gained access. %s",
			request.By, mailWords(notify, request.By))
	}
	result := s.report(ctx, res, out)
	result.JSON.SharingBefore = before.Summary()
	result.JSON.SharingAfter = after.Summary()
	return result
}

func (s *Service) requestNote(request *model.AccessRequest, role string, notify bool) string {
	parts := []string{fmt.Sprintf("%s is now %s of this file", request.For, model.RoleWords(role))}
	if request.For != request.By {
		parts = append(parts, "the access went to "+request.For+", which is the address "+request.By+
			" asked for it on behalf of")
	}
	parts = append(parts, strings.TrimSuffix(mailWords(notify, request.By), "."))
	return joinSentences(parts)
}

// mailWords says whether Google was asked to write to the requester. It
// is stated either way, because "no mail was sent" is the half a caller
// would otherwise have to assume.
func mailWords(notify bool, who string) string {
	if notify {
		return "Google was asked to mail " + who + " about it."
	}
	return "No mail was sent; pass notify: true if they should hear about it."
}

// resolveRequestError explains a refusal of the resolve call itself.
func (s *Service) resolveRequestError(err error, f *gdrive.File, request *model.AccessRequest, action string) error {
	switch gapi.Class(err) {
	case ClassNotFound:
		return &Error{Class: ClassNotFound, Message: fmt.Sprintf(
			"the request %s on %s is no longer there; somebody else may have answered it. "+
				"list_access_requests shows what is still waiting.", request.ID, f.Name), Err: err}
	case ClassBlocked:
		return &Error{Class: ClassBlocked, Message: fmt.Sprintf(
			"your organisation's sharing policy does not allow giving %s access to %s, so the request "+
				"cannot be accepted here. Google said: %s", request.For, f.Name, gapi.Message(err)), Err: err}
	}
	return wrap(err, fmt.Sprintf("%sing the access request from %s on %s", action, request.By, f.Name))
}
