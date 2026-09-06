package gapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// ApprovalFields is what an approval is read with.
//
// approvals.list has the same trap the comment endpoints do, and the
// discovery document says so in as many words: "By default, this method
// returns a minimal response that may not include the items array. To
// retrieve approval details, you must explicitly specify the fields you
// want." A caller who omits it gets a successful, empty-looking answer —
// which is worse than an error, because it reads as "no approvals".
// Phase 3 met the same shape on comments and it cost a live run to find;
// this time the document was read first.
const ApprovalFields = "approvalId,targetFileId,status,createTime,modifyTime,completeTime," +
	"dueTime,fileContentChangeBehavior,initiator(displayName,emailAddress,me)," +
	"reviewerResponses(response,reviewer(displayName,emailAddress,me))"

// Drive's page sizes for approvals.list.
const (
	DefaultApprovalPageSize = 20
	MaxApprovalPageSize     = 100
)

// ListApprovalsOptions tune one page of a file's approvals.
type ListApprovalsOptions struct {
	PageSize  int
	PageToken string
}

// ListApprovals returns one page of the approvals on a file.
func (c *Client) ListApprovals(ctx context.Context, fileID string, o ListApprovalsOptions) (*gdrive.ApprovalList, error) {
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	v := url.Values{}
	v.Set("fields", "nextPageToken,items("+ApprovalFields+")")
	size := o.PageSize
	switch {
	case size <= 0:
		size = DefaultApprovalPageSize
	case size > MaxApprovalPageSize:
		size = MaxApprovalPageSize
	}
	v.Set("pageSize", strconv.Itoa(size))
	if o.PageToken != "" {
		v.Set("pageToken", o.PageToken)
	}
	u := c.base + "/files/" + segment + "/approvals?" + v.Encode()
	body, err := c.do(ctx, request{method: http.MethodGet, url: u, resourceIDs: []string{fileID}})
	if err != nil {
		return nil, err
	}
	var list gdrive.ApprovalList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("%w: decode approvals: %w", ErrUnexpected, err)
	}
	return &list, nil
}

// GetApproval reads one approval.
func (c *Client) GetApproval(ctx context.Context, fileID, approvalID string) (*gdrive.Approval, error) {
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	u := c.base + "/files/" + segment + "/approvals/" + url.PathEscape(approvalID) +
		"?fields=" + url.QueryEscape(ApprovalFields)
	body, err := c.do(ctx, request{method: http.MethodGet, url: u, resourceIDs: []string{fileID}})
	if err != nil {
		return nil, err
	}
	return decodeApproval(body)
}

// StartApproval opens a review on a file.
//
// It is NOT marked idempotent. A second attempt after an ambiguous
// failure would start a second review of the same file, and every
// reviewer would be mailed twice — there is no request id to collapse
// them, and no field that would make Drive refuse the duplicate. A
// caller who is unsure whether the first one landed can list the file's
// approvals and see.
func (c *Client) StartApproval(ctx context.Context, fileID string, body *gdrive.StartApproval) (*gdrive.Approval, error) {
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	// start is the one verb addressed on the collection rather than on an
	// approval: files/{id}/approvals:start.
	return c.postApproval(ctx, fileID, c.base+"/files/"+segment+"/approvals:start", body)
}

// ApproveApproval records this account's approval.
func (c *Client) ApproveApproval(ctx context.Context, fileID, approvalID string, body *gdrive.ApprovalMessage) (*gdrive.Approval, error) {
	return c.approvalVerb(ctx, fileID, approvalID, "approve", body)
}

// DeclineApproval records this account's refusal, which completes the
// approval: one decline decides it.
func (c *Client) DeclineApproval(ctx context.Context, fileID, approvalID string, body *gdrive.ApprovalMessage) (*gdrive.Approval, error) {
	return c.approvalVerb(ctx, fileID, approvalID, "decline", body)
}

// CancelApproval withdraws an approval that is still in progress.
func (c *Client) CancelApproval(ctx context.Context, fileID, approvalID string, body *gdrive.ApprovalMessage) (*gdrive.Approval, error) {
	return c.approvalVerb(ctx, fileID, approvalID, "cancel", body)
}

// CommentApproval adds a message to an approval, mailing the initiator
// and every reviewer.
func (c *Client) CommentApproval(ctx context.Context, fileID, approvalID string, body *gdrive.ApprovalMessage) (*gdrive.Approval, error) {
	return c.approvalVerb(ctx, fileID, approvalID, "comment", body)
}

// ReassignApproval adds reviewers or replaces them.
func (c *Client) ReassignApproval(ctx context.Context, fileID, approvalID string, body *gdrive.ReassignApproval) (*gdrive.Approval, error) {
	return c.approvalVerb(ctx, fileID, approvalID, "reassign", body)
}

// approvalVerb sends one of the colon-suffixed approval actions. They
// differ in their path and their body and in nothing else, including
// their answer: every one of them returns the whole approval as it
// stands afterwards, which is what the caller wants to report.
//
// None is marked idempotent. approve and decline record one reviewer's
// answer and would be harmless twice, but comment and reassign both mail
// people, and start makes a second review — so the safe default is the
// one that holds for all of them, and a retry is the caller's decision
// with the state in front of them.
func (c *Client) approvalVerb(ctx context.Context, fileID, approvalID, verb string, body any) (*gdrive.Approval, error) {
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	u := c.base + "/files/" + segment + "/approvals/" + url.PathEscape(approvalID) + ":" + verb
	return c.postApproval(ctx, fileID, u, body)
}

// postApproval sends one approval action and decodes the approval it
// answers with. Every verb answers with the whole approval as it stands
// afterwards, which is the thing worth reporting.
func (c *Client) postApproval(ctx context.Context, fileID, u string, body any) (*gdrive.Approval, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%w: encode approval request: %w", ErrUnexpected, err)
	}
	out, err := c.do(ctx, request{
		method: http.MethodPost,
		url:    u + "?fields=" + url.QueryEscape(ApprovalFields),
		body:   payload, resourceIDs: []string{fileID},
	})
	if err != nil {
		return nil, err
	}
	return decodeApproval(out)
}

func decodeApproval(body []byte) (*gdrive.Approval, error) {
	var a gdrive.Approval
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("%w: decode approval: %w", ErrUnexpected, err)
	}
	return &a, nil
}
