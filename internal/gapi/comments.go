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

// ReplyFields is what a reply is read with. The author's email address
// is left out because the reference says Drive does not populate it on a
// comment or a reply: asking would add a field that is always empty and
// an address to redact for nothing.
const ReplyFields = "id,createdTime,modifiedTime,author(displayName,me),content,deleted,action"

// CommentFields is what a comment is read with, replies included. Drive
// returns the whole thread inline, so a listing of threads costs one
// call rather than one per thread.
// assigneeEmailAddress is deliberately absent. The discovery document
// lists it as a property of Comment, but Drive REFUSES it in a field
// selection — "Invalid field selection assignee_email_address" — so
// asking for it fails the whole call. Found by the live run, which is
// the only place it could have been found: the reference says the field
// exists, and it does; what does not work is asking for it.
const CommentFields = "id,createdTime,modifiedTime,author(displayName,me),content,deleted,resolved," +
	"anchor,quotedFileContent,replies(" + ReplyFields + ")"

// maxCommentPageSize is Drive's ceiling for comments.list and
// replies.list. It coerces anything larger, and the fake refuses it.
const maxCommentPageSize = 100

// The comment endpoints are the one corner of this API that REQUIRES the
// fields parameter: comments.list, get, create and update all answer 400
// without one. Every call below sets it, and drivetest enforces the same
// rule so a caller that forgets fails here rather than in production.
//
// They also take no supportsAllDrives: the discovery document gives the
// comment and reply methods no such parameter, and a comment on a
// shared-drive file is reached without it.

// ListCommentsOptions tune one page of a file's comments.
type ListCommentsOptions struct {
	PageSize  int
	PageToken string
	// IncludeDeleted keeps the tombstones. A deleted comment comes back
	// with its content stripped, so it says that something was said and
	// removed, and nothing more.
	IncludeDeleted bool
	// StartModifiedTime limits the page to threads touched since an
	// RFC 3339 instant.
	StartModifiedTime string
}

// ListComments returns one page of a file's comment threads.
func (c *Client) ListComments(ctx context.Context, fileID string, o ListCommentsOptions) (*gdrive.CommentList, error) {
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	v := url.Values{}
	v.Set("fields", "nextPageToken,comments("+CommentFields+")")
	size := o.PageSize
	switch {
	case size <= 0:
		size = 20
	case size > maxCommentPageSize:
		size = maxCommentPageSize
	}
	v.Set("pageSize", strconv.Itoa(size))
	v.Set("includeDeleted", boolText(o.IncludeDeleted))
	if o.PageToken != "" {
		v.Set("pageToken", o.PageToken)
	}
	if o.StartModifiedTime != "" {
		v.Set("startModifiedTime", o.StartModifiedTime)
	}
	u := c.base + "/files/" + segment + "/comments?" + v.Encode()
	body, err := c.do(ctx, request{kind: kindRead, method: http.MethodGet, url: u, resourceIDs: []string{fileID}})
	if err != nil {
		return nil, err
	}
	var list gdrive.CommentList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("%w: decode comments: %w", ErrUnexpected, err)
	}
	return &list, nil
}

// GetComment reads one thread with its replies.
func (c *Client) GetComment(ctx context.Context, fileID, commentID string, includeDeleted bool) (*gdrive.Comment, error) {
	u, err := c.commentURL(fileID, commentID, url.Values{
		"fields":         []string{CommentFields},
		"includeDeleted": []string{boolText(includeDeleted)},
	})
	if err != nil {
		return nil, err
	}
	body, err := c.do(ctx, request{kind: kindRead, method: http.MethodGet, url: u, resourceIDs: []string{fileID}})
	if err != nil {
		return nil, err
	}
	return decodeComment(body)
}

// CreateComment adds a thread to a file.
//
// It is a POST that carries no pre-generated id — Drive offers none for
// a comment — so it is not repeatable and is not marked as such. A
// second attempt after an ambiguous failure would post the remark twice,
// which is visible to everybody the file is shared with.
func (c *Client) CreateComment(ctx context.Context, fileID string, meta *gdrive.CommentMeta) (*gdrive.Comment, error) {
	payload, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
	u := c.base + "/files/" + segment + "/comments?fields=" + url.QueryEscape(CommentFields)
	body, err := c.do(ctx, request{kind: kindWrite, method: http.MethodPost, url: u,
		body: payload, resourceIDs: []string{fileID}})
	if err != nil {
		return nil, err
	}
	return decodeComment(body)
}

// UpdateComment changes a thread's text. A PATCH, so a retry is safe.
func (c *Client) UpdateComment(ctx context.Context, fileID, commentID string, meta *gdrive.CommentMeta) (*gdrive.Comment, error) {
	payload, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	u, err := c.commentURL(fileID, commentID, url.Values{"fields": []string{CommentFields}})
	if err != nil {
		return nil, err
	}
	body, err := c.do(ctx, request{kind: kindWrite, method: http.MethodPatch, url: u,
		body: payload, resourceIDs: []string{fileID}})
	if err != nil {
		return nil, err
	}
	return decodeComment(body)
}

// DeleteComment removes a thread and every reply in it. Drive answers
// 204 with no body, and the thread stays visible to includeDeleted as a
// tombstone with no content.
func (c *Client) DeleteComment(ctx context.Context, fileID, commentID string) error {
	u, err := c.commentURL(fileID, commentID, nil)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, request{kind: kindWrite, method: http.MethodDelete, url: u,
		resourceIDs: []string{fileID}})
	return err
}

// CreateReply adds a reply to a thread, or resolves or reopens it. The
// same reasoning as CreateComment: a POST with no id of its own, so it
// is never repeated.
func (c *Client) CreateReply(ctx context.Context, fileID, commentID string, meta *gdrive.ReplyMeta) (*gdrive.Reply, error) {
	payload, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	u, err := c.repliesURL(fileID, commentID, url.Values{"fields": []string{ReplyFields}})
	if err != nil {
		return nil, err
	}
	body, err := c.do(ctx, request{kind: kindWrite, method: http.MethodPost, url: u,
		body: payload, resourceIDs: []string{fileID}})
	if err != nil {
		return nil, err
	}
	return decodeReply(body)
}

// UpdateReply changes a reply's text.
func (c *Client) UpdateReply(ctx context.Context, fileID, commentID, replyID string, meta *gdrive.ReplyMeta) (*gdrive.Reply, error) {
	payload, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	u, err := c.replyURL(fileID, commentID, replyID, url.Values{"fields": []string{ReplyFields}})
	if err != nil {
		return nil, err
	}
	body, err := c.do(ctx, request{kind: kindWrite, method: http.MethodPatch, url: u,
		body: payload, resourceIDs: []string{fileID}})
	if err != nil {
		return nil, err
	}
	return decodeReply(body)
}

// DeleteReply removes one reply from a thread.
func (c *Client) DeleteReply(ctx context.Context, fileID, commentID, replyID string) error {
	u, err := c.replyURL(fileID, commentID, replyID, nil)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, request{kind: kindWrite, method: http.MethodDelete, url: u,
		resourceIDs: []string{fileID}})
	return err
}

// commentURL builds /files/{file}/comments/{comment}, escaping every
// caller-supplied segment. The file id goes through fileSegment, which
// refuses the words Drive uses as endpoints under /files; the ids below
// it need no such guard, because Drive puts no sibling endpoints beside
// a comment id or a reply id.
func (c *Client) commentURL(fileID, commentID string, v url.Values) (string, error) {
	segment, err := fileSegment(fileID)
	if err != nil {
		return "", err
	}
	if commentID == "" {
		return "", fmt.Errorf("%w: a comment id is required", ErrInvalid)
	}
	return withQuery(c.base+"/files/"+segment+"/comments/"+url.PathEscape(commentID), v), nil
}

// repliesURL builds the collection under one comment.
func (c *Client) repliesURL(fileID, commentID string, v url.Values) (string, error) {
	u, err := c.commentURL(fileID, commentID, nil)
	if err != nil {
		return "", err
	}
	return withQuery(u+"/replies", v), nil
}

// replyURL builds /files/{file}/comments/{comment}/replies/{reply}.
func (c *Client) replyURL(fileID, commentID, replyID string, v url.Values) (string, error) {
	u, err := c.commentURL(fileID, commentID, nil)
	if err != nil {
		return "", err
	}
	if replyID == "" {
		return "", fmt.Errorf("%w: a reply id is required", ErrInvalid)
	}
	return withQuery(u+"/replies/"+url.PathEscape(replyID), v), nil
}

func withQuery(u string, v url.Values) string {
	if len(v) == 0 {
		return u
	}
	return u + "?" + v.Encode()
}

func decodeComment(body []byte) (*gdrive.Comment, error) {
	var out gdrive.Comment
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: decode comment: %w", ErrUnexpected, err)
	}
	return &out, nil
}

func decodeReply(body []byte) (*gdrive.Reply, error) {
	var out gdrive.Reply
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: decode reply: %w", ErrUnexpected, err)
	}
	return &out, nil
}
