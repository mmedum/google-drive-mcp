package model

import (
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// Comment is the server's view of one thread on a file.
type Comment struct {
	ID       string
	By       string
	Created  time.Time
	Modified time.Time
	Text     string
	// Resolved is set by a reply, never directly: Drive has no field to
	// write it, which is why reply_comment carries the action.
	Resolved bool
	// Deleted marks a tombstone. Drive keeps the thread and strips the
	// content, so a deleted comment says that something was said and
	// removed, and nothing else.
	Deleted bool
	// Quoted is the passage the comment is pinned to, when Drive gives
	// one. Only the editor that owns the document can make a new one.
	Quoted string
	// Anchored marks a comment tied to a region of the document, whether
	// or not Drive quoted the passage.
	Anchored bool
	// AssignedTo is the address a comment was made an action item for.
	AssignedTo string
	Replies    []*Reply
}

// Reply is the server's view of one reply in a thread.
type Reply struct {
	ID       string
	By       string
	Created  time.Time
	Modified time.Time
	Text     string
	// Action is Drive's own word for what the reply did to the thread:
	// "resolve", "reopen", or empty for a reply that only said something.
	Action  string
	Deleted bool
}

// NewComment builds the model view of a thread.
func NewComment(c *gdrive.Comment) *Comment {
	if c == nil {
		return nil
	}
	out := &Comment{
		ID: c.ID, By: userWords(c.Author), Created: parseTime(c.CreatedTime),
		Modified: parseTime(c.ModifiedTime), Text: c.Content, Resolved: c.Resolved,
		Deleted: c.Deleted, Anchored: strings.TrimSpace(c.Anchor) != "",
		AssignedTo: c.AssigneeEmailAddress,
	}
	if c.QuotedFileContent != nil {
		out.Quoted = c.QuotedFileContent.Value
		out.Anchored = true
	}
	for _, r := range c.Replies {
		if reply := NewReply(r); reply != nil {
			out.Replies = append(out.Replies, reply)
		}
	}
	return out
}

// NewReply builds the model view of a reply.
func NewReply(r *gdrive.Reply) *Reply {
	if r == nil {
		return nil
	}
	return &Reply{
		ID: r.ID, By: userWords(r.Author), Created: parseTime(r.CreatedTime),
		Modified: parseTime(r.ModifiedTime), Text: r.Content, Action: r.Action, Deleted: r.Deleted,
	}
}

// Open reports whether the thread is still waiting for somebody.
func (c *Comment) Open() bool { return c != nil && !c.Resolved && !c.Deleted }

// AccessRequest is the server's view of one pending request to be let
// into a file.
type AccessRequest struct {
	ID string
	// By is the address that asked.
	By string
	// For is the address that would receive the access, which is not
	// always the one that asked.
	For string
	// Roles are what was asked for, in Drive's own words. A proposal can
	// name more than one, and this server refuses to accept such a
	// request without being told which role to grant.
	Roles   []string
	Message string
	At      time.Time
	// View is the published view a proposal belongs to, when it does.
	View string
}

// NewAccessRequest builds the model view of an access proposal.
func NewAccessRequest(p *gdrive.AccessProposal) *AccessRequest {
	if p == nil {
		return nil
	}
	out := &AccessRequest{
		ID: p.ProposalID, By: p.RequesterEmailAddress, For: p.RecipientEmailAddress,
		Message: p.RequestMessage, At: parseTime(p.CreateTime),
	}
	if out.For == "" {
		out.For = out.By
	}
	for _, rv := range p.RolesAndViews {
		if rv == nil || rv.Role == "" {
			continue
		}
		out.Roles = append(out.Roles, rv.Role)
		if rv.View != "" && out.View == "" {
			out.View = rv.View
		}
	}
	return out
}

// SoleRole returns the one role the request asked for, and whether there
// was exactly one. A proposal naming several is an ambiguity, and this
// server never picks for the caller.
func (a *AccessRequest) SoleRole() (string, bool) {
	if a == nil || len(a.Roles) != 1 {
		return "", false
	}
	return a.Roles[0], true
}
