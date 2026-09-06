package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/render"
	"github.com/mmedum/google-drive-mcp/internal/service"
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
	Action           string   `json:"action" jsonschema:"start a new approval, approve or decline one you are a reviewer of, cancel one you started, comment on one, or reassign it to different reviewers"`
	Approval         string   `json:"approval,omitempty" jsonschema:"the approval to act on, by the id list_approvals shows. Required for everything except start."`
	Reviewers        []string `json:"reviewers,omitempty" jsonschema:"email addresses. With start, the people being asked to approve — at least one is required. With reassign, the people to ADD to the reviewers."`
	ReplaceReviewers []string `json:"replace_reviewers,omitempty" jsonschema:"with reassign, email addresses that replace the current reviewers rather than joining them"`
	Message          string   `json:"message,omitempty" jsonschema:"a message that goes into the notification and into the approval's log. Required for comment, optional elsewhere."`
	LockFile         bool     `json:"lock_file,omitempty" jsonschema:"with start, lock the file's content while the approval is open so nobody can change it, including you"`
	Due              string   `json:"due,omitempty" jsonschema:"with start, when the approval is wanted by, as a date like 2026-01-31 or a full RFC 3339 timestamp"`
}

// registerApprovals adds the approval pair. list_approvals is a read and
// is always registered; manage_approval is not, in read-only mode.
//
// They are NOT behind GDRIVE_SHARING, and the difference is worth
// stating: an approval grants nobody access to anything. It mails people
// who already have it. What it can do is lock the file, which is a
// restriction rather than an exposure, and the tool says so where it
// matters.
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
		Description: "Ask people to review a file, answer a review you were asked for, withdraw one, " +
			"comment on one, or change who is being asked. Actions: " +
			strings.Join(service.ApprovalActions(), ", ") + ". " +
			"EVERY ACTION MAILS SOMEBODY — the reviewers, or the person who asked — and there is no way to " +
			"turn that off, unlike sharing. An approval grants nobody access; what it can do is LOCK the " +
			"file, either straight away with lock_file or once it is approved, and a locked file cannot be " +
			"edited by anyone until the approval is finished. Declining completes the approval on its own, " +
			"where approving waits for every reviewer. Drive cannot remove a reviewer: cancel and start again.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ManageApprovalInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.ManageApproval(ctx, service.ManageApprovalInput{
			File: in.File, Approval: in.Approval, Action: in.Action,
			Reviewers: in.Reviewers, ReplaceReviewers: in.ReplaceReviewers,
			Message: in.Message, LockFile: in.LockFile, Due: in.Due,
		}))
	})
	return []string{"list_approvals", "manage_approval"}
}
