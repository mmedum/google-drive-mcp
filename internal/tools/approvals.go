package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/v3/internal/render"
	"github.com/mmedum/google-drive-mcp/v3/internal/service"
)

// ApprovalsInput selects a file's approvals.
type ApprovalsInput struct {
	File      string `json:"file" jsonschema:"the file to check: an id, any Drive URL, a path from My Drive, or a shared-drive path. A name or path matching more than one item is refused with the candidates listed."`
	PageSize  int    `json:"page_size,omitempty" jsonschema:"how many approvals to return, default 20, maximum 100"`
	PageToken string `json:"page_token,omitempty" jsonschema:"the token a previous call returned, to get the next page"`
}

// ManageApprovalInput is one action on one approval.
type ManageApprovalInput struct {
	File             string   `json:"file" jsonschema:"the file the approval is on: an id, any Drive URL, a path from My Drive, or a shared-drive path"`
	Action           string   `json:"action" jsonschema:"start a new approval, decline one you are a reviewer of, cancel one you started, comment on one, or reassign it to different reviewers. approve_file approves."`
	Approval         string   `json:"approval,omitempty" jsonschema:"the approval to act on, by the id list_approvals shows. Required for everything except start."`
	Reviewers        []string `json:"reviewers,omitempty" jsonschema:"email addresses. With start, the people being asked to approve — at least one is required. With reassign, the people to ADD to the reviewers."`
	ReplaceReviewers []string `json:"replace_reviewers,omitempty" jsonschema:"with reassign, swaps as \"going@example.com=arriving@example.com\". Drive will not simply remove a reviewer: a replacement, which names who takes their place, is the only way somebody leaves an approval."`
	Message          string   `json:"message,omitempty" jsonschema:"a message that goes into the notification and into the approval's log. Required for comment, optional elsewhere."`
	LockFile         bool     `json:"lock_file,omitempty" jsonschema:"with start, ask Drive to lock the file's content while the approval is open. Drive does not always apply it, and the result says whether it did. Separately, an approval started with on_content_change reset_approval locks the file once it is APPROVED"`
	Due              string   `json:"due,omitempty" jsonschema:"with start, when the approval is wanted by, as a date like 2026-01-31 or a full RFC 3339 timestamp"`
	OnContentChange  string   `json:"on_content_change,omitempty" jsonschema:"with start, what a change to the file's content does to the approval: reset_approval (the default) clears answers already given while it is open and LOCKS the file once it is approved; no_action leaves the answers as they are and does not lock the file"`
}

// ApproveFileInput is this account's approval of one review.
type ApproveFileInput struct {
	File     string `json:"file" jsonschema:"the file the approval is on: an id, any Drive URL, a path from My Drive, or a shared-drive path"`
	Approval string `json:"approval" jsonschema:"the approval to approve, by the id list_approvals shows"`
	Message  string `json:"message,omitempty" jsonschema:"a message that goes into the notification and into the approval's log"`
}

// registerApprovals adds the approval tools. list_approvals is a read and
// is always registered; manage_approval is not, in read-only mode.
//
// They are NOT behind GDRIVE_SHARING, and the difference is worth
// stating: an approval grants nobody access to anything. It mails people
// who already have it. What it can do is lock the file, which is a
// restriction rather than an exposure, and the tool says so where it
// matters.
//
// Both descriptions used to promise that lock_file locks the file. A
// live run found it does not — no content restriction, and the next
// content change went through — while APPROVING locks it exactly as
// described. A schema is read before the call rather than after, so an
// argument that overpromises there is worse than a result that does.
func registerApprovals(s *mcp.Server, d Deps) []string {
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_approvals",
		Description: "The approvals on a file: who asked for a review, who has to answer, what they have " +
			"said, and whether it is waiting on you. Approvals are a Workspace feature and not every edition " +
			"has them; where they are not available Drive refuses the call rather than returning an empty list.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ApprovalsInput) (*mcp.CallToolResult, any, error) {
		out, err := d.Service.ListApprovals(ctx, service.ListApprovalsInput{
			File: in.File, PageSize: in.PageSize, PageToken: in.PageToken,
		})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(out), nil, nil
	})
	if d.Config.ReadOnly {
		return []string{"list_approvals"}
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "manage_approval",
		Description: "Ask people to review a file, decline a review you were asked for, withdraw one, " +
			"comment on one, or change who is being asked. Approving is approve_file. Actions: " +
			strings.Join(service.ApprovalActions(), ", ") + ". " +
			"EVERY ACTION MAILS SOMEBODY — the reviewers, or the person who asked — and there is no way to " +
			"turn that off, unlike sharing. An approval grants nobody access; what it can do is LOCK the " +
			"file: once it is APPROVED, unless it was started with on_content_change no_action, and lock_file " +
			"asks for it at the start although Drive does not always apply that — the result says which. A " +
			"locked file cannot be edited by anyone, and the lock an approval leaves does not come off. " +
			"Declining completes the approval on its own, " +
			"where approving waits for every reviewer. A reviewer can be added, or replaced by somebody " +
			"else, but never simply removed.",
		Annotations: changing,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ManageApprovalInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.ManageApproval(ctx, service.ManageApprovalInput{
			File: in.File, Approval: in.Approval, Action: in.Action,
			Reviewers: in.Reviewers, ReplaceReviewers: in.ReplaceReviewers,
			Message: in.Message, LockFile: in.LockFile, Due: in.Due, OnContentChange: in.OnContentChange,
		}))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "approve_file",
		Description: "Approve a review of a file that you were asked for: your sign-off, recorded under your " +
			"name and mailed to the person who asked and the other reviewers. Drive has no call that takes an " +
			"approval back. Once every reviewer has approved, an approval started with on_content_change " +
			"reset_approval LOCKS THE FILE, and that lock does not come off: nobody can edit it after. " +
			"list_approvals shows the approval's id and whether it is waiting on you.",
		Annotations: final,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ApproveFileInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.ApproveFile(ctx, service.ApproveFileInput{
			File: in.File, Approval: in.Approval, Message: in.Message,
		}))
	})
	return []string{"list_approvals", "manage_approval", "approve_file"}
}
