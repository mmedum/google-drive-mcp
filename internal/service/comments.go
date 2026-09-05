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

// Comment page sizes. Drive coerces anything above its own ceiling; this
// server refuses it instead, so a caller who asked for 500 learns that
// they did not get 500.
const (
	DefaultCommentPageSize = 20
	MaxCommentPageSize     = 100
)

// ListCommentsInput selects a file's threads.
type ListCommentsInput struct {
	File string
	// IncludeDeleted keeps the tombstones Drive leaves behind. They carry
	// no content, only the fact that something was said and removed.
	IncludeDeleted bool
	// Since limits the page to threads modified after an RFC 3339 instant.
	Since     string
	PageSize  int
	PageToken string
}

// ListComments returns a file's comment threads, each with its replies.
// Drive carries the replies inline, so a thread costs no second call.
func (s *Service) ListComments(ctx context.Context, in ListCommentsInput) (string, error) {
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return "", err
	}
	f := res.File
	if f.IsFolder() {
		return "", Errorf(ClassInvalid, "%s is a folder, and Drive has no comments on a folder.", f.Name)
	}
	size := in.PageSize
	switch {
	case size <= 0:
		size = DefaultCommentPageSize
	case size > MaxCommentPageSize:
		return "", Errorf(ClassInvalid, "page_size %d is above Drive's ceiling of %d for comments",
			size, MaxCommentPageSize)
	}
	since := ""
	if raw := strings.TrimSpace(in.Since); raw != "" {
		// The same date forms a search accepts, turned into the RFC 3339
		// Drive wants: one spelling of a date across the whole surface.
		if since, err = parseSearchDate(raw); err != nil {
			return "", Errorf(ClassInvalid, "since: %v", err)
		}
	}
	page, err := s.api.ListComments(ctx, f.ID, gapi.ListCommentsOptions{
		PageSize: size, PageToken: in.PageToken, IncludeDeleted: in.IncludeDeleted,
		StartModifiedTime: since,
	})
	if err != nil {
		return "", s.commentListError(err, f, in.PageToken)
	}
	threads := make([]*model.Comment, 0, len(page.Comments))
	open := 0
	for _, c := range page.Comments {
		thread := model.NewComment(c)
		if thread == nil {
			continue
		}
		if thread.Open() {
			open++
		}
		threads = append(threads, thread)
	}
	return render.Comments(threads, render.CommentsOptions{
		Subject:         commentSubject(f, len(threads), open),
		Location:        s.Location(ctx, f).String(),
		Now:             s.now(),
		CanComment:      f.Capabilities == nil || f.Capabilities.CanComment,
		Workspace:       f.IsWorkspaceDoc(),
		NextPageToken:   page.NextPageToken,
		IncludedDeleted: in.IncludeDeleted,
	}), nil
}

// commentSubject heads the listing with the count that decides whether
// there is anything to do: how many threads are still open.
func commentSubject(f *gdrive.File, threads, open int) string {
	head := fmt.Sprintf("%s — %s: %s", f.Name, model.Kind(f), model.Plural(threads, "comment thread", "comment threads"))
	if threads == 0 {
		return head
	}
	switch open {
	case 0:
		return head + ", none of them open"
	case threads:
		return head + ", all open"
	default:
		return fmt.Sprintf("%s, %d open", head, open)
	}
}

// commentListError names the one failure the caller can fix: a stale or
// foreign page token.
func (s *Service) commentListError(err error, f *gdrive.File, token string) error {
	if token != "" && (gapi.Class(err) == ClassInvalid || gapi.Class(err) == ClassNotFound) {
		return &Error{Class: ClassInvalid, Message: fmt.Sprintf(
			"Drive will not read the page token %q for the comments on %s. A comment page token is valid for "+
				"a few hours; call list_comments again with none to start from the first page. Google said: %s",
			token, f.Name, gapi.Message(err)), Err: err}
	}
	return wrap(err, "reading the comments on "+f.Name)
}

// AddCommentInput is a new thread on a file.
type AddCommentInput struct {
	File    string
	Content string
}

// AddComment starts a thread. The comment is unanchored: pinning one to
// a passage of a Google Doc means knowing where that passage is, which
// is the Docs API's job and not this server's.
func (s *Service) AddComment(ctx context.Context, in AddCommentInput) (*Result, error) {
	if err := s.writable("add_comment"); err != nil {
		return nil, err
	}
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return nil, Errorf(ClassInvalid, "content is required: a comment with nothing in it says nothing")
	}
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	if err := s.commentable(f); err != nil {
		return nil, err
	}
	created, err := s.api.CreateComment(ctx, f.ID, &gdrive.CommentMeta{Content: content})
	if err != nil {
		return nil, s.commentWriteError(err, f, "commenting on")
	}
	return s.report(ctx, res, outcome{Action: render.ActionCommented, Note: fmt.Sprintf(
		"comment %s added to %s. Everybody who can see the file can see it; reply_comment resolves or "+
			"reopens the thread.", created.ID, f.Name)}), nil
}

// Actions reply_comment accepts.
const (
	CommentReply   = "reply"
	CommentResolve = "resolve"
	CommentReopen  = "reopen"
	CommentEdit    = "edit"
)

// CommentActions lists what reply_comment accepts.
func CommentActions() []string {
	return []string{CommentReply, CommentResolve, CommentReopen, CommentEdit}
}

// ReplyCommentInput acts on one thread.
type ReplyCommentInput struct {
	File    string
	Comment string
	Action  string
	Content string
	// Reply names an existing reply, which only edit needs: editing with
	// one changes that reply, and editing without one changes the comment
	// that opened the thread.
	Reply string
}

// ReplyComment answers a thread, resolves it, reopens it, or edits
// something already said in it.
func (s *Service) ReplyComment(ctx context.Context, in ReplyCommentInput) (*Result, error) {
	if err := s.writable("reply_comment"); err != nil {
		return nil, err
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	if action == "" {
		action = CommentReply
	}
	commentID := strings.TrimSpace(in.Comment)
	if commentID == "" {
		return nil, Errorf(ClassInvalid, "comment is required: list_comments shows the thread ids")
	}
	content := strings.TrimSpace(in.Content)
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	if err := s.commentable(f); err != nil {
		return nil, err
	}
	switch action {
	case CommentReply, CommentResolve, CommentReopen:
		return s.replyTo(ctx, res, commentID, action, content)
	case CommentEdit:
		return s.editComment(ctx, res, commentID, strings.TrimSpace(in.Reply), content)
	default:
		return nil, Errorf(ClassInvalid, "action %q is not one of %s", in.Action,
			strings.Join(CommentActions(), ", "))
	}
}

// replyTo adds a reply, optionally one that resolves or reopens the
// thread. Drive expresses both as a reply with an action, so resolving
// is always visible to everybody as a reply rather than as a silent
// state change.
func (s *Service) replyTo(ctx context.Context, res *Resolved, commentID, action, content string) (*Result, error) {
	f := res.File
	if action == CommentReply && content == "" {
		return nil, Errorf(ClassInvalid, "content is required to reply. To close the thread without saying "+
			"anything, use action resolve.")
	}
	before, err := s.api.GetComment(ctx, f.ID, commentID, false)
	if err != nil {
		return nil, s.commentError(err, f, commentID)
	}
	meta := &gdrive.ReplyMeta{Content: content}
	switch action {
	case CommentResolve:
		if before.Resolved {
			return s.report(ctx, res, outcome{Action: render.ActionUnchanged, Note: fmt.Sprintf(
				"comment %s on %s is already resolved.", commentID, f.Name)}), nil
		}
		meta.Action = gdrive.ReplyActionResolve
	case CommentReopen:
		if !before.Resolved {
			return s.report(ctx, res, outcome{Action: render.ActionUnchanged, Note: fmt.Sprintf(
				"comment %s on %s is already open.", commentID, f.Name)}), nil
		}
		meta.Action = gdrive.ReplyActionReopen
	}
	reply, err := s.api.CreateReply(ctx, f.ID, commentID, meta)
	if err != nil {
		return nil, s.commentWriteError(err, f, "replying to a comment on")
	}
	out := outcome{Action: render.ActionReplied, Note: fmt.Sprintf(
		"reply %s added to comment %s on %s.", reply.ID, commentID, f.Name)}
	switch action {
	case CommentResolve:
		out.Action = render.ActionResolved
		out.Note = fmt.Sprintf("comment %s on %s is resolved; reply %s records it, and everybody who can "+
			"see the file can see that.", commentID, f.Name, reply.ID)
		out.Changes = []render.Change{{Field: "comment " + commentID, From: "open", To: "resolved"}}
	case CommentReopen:
		out.Action = render.ActionReopened
		out.Note = fmt.Sprintf("comment %s on %s is open again; reply %s records it.", commentID, f.Name, reply.ID)
		out.Changes = []render.Change{{Field: "comment " + commentID, From: "resolved", To: "open"}}
	}
	return s.report(ctx, res, out), nil
}

// editComment changes the text of something already said. With a reply
// id it edits that reply; without one it edits the comment that opened
// the thread, which is the only other thing in it that has text.
func (s *Service) editComment(ctx context.Context, res *Resolved, commentID, replyID, content string) (*Result, error) {
	f := res.File
	if content == "" {
		return nil, Errorf(ClassInvalid, "content is required to edit: Drive has no way to blank a comment, "+
			"and an edit to nothing would leave the thread saying nothing while still being there.")
	}
	if replyID != "" {
		updated, err := s.api.UpdateReply(ctx, f.ID, commentID, replyID, &gdrive.ReplyMeta{Content: content})
		if err != nil {
			return nil, s.commentError(err, f, commentID)
		}
		return s.report(ctx, res, outcome{Action: render.ActionUpdated, Note: fmt.Sprintf(
			"reply %s on comment %s of %s now reads as given. Drive keeps no history of what it said "+
				"before.", updated.ID, commentID, f.Name)}), nil
	}
	updated, err := s.api.UpdateComment(ctx, f.ID, commentID, &gdrive.CommentMeta{Content: content})
	if err != nil {
		return nil, s.commentError(err, f, commentID)
	}
	return s.report(ctx, res, outcome{Action: render.ActionUpdated, Note: fmt.Sprintf(
		"comment %s on %s now reads as given. Drive keeps no history of what it said before.",
		updated.ID, f.Name)}), nil
}

// DeleteCommentInput removes a thread or one reply, for good.
type DeleteCommentInput struct {
	File    string
	Comment string
	// Reply removes one reply instead of the whole thread.
	Reply   string
	Confirm bool
}

// DeleteComment removes a thread or a reply. It is gated twice — the
// tool is registered only with GDRIVE_ENABLE_DESTRUCTIVE=true, and the
// call needs confirm — because there is no trash for a comment: Drive
// keeps a tombstone with the words removed, and nothing brings them back.
func (s *Service) DeleteComment(ctx context.Context, in DeleteCommentInput) (*Result, error) {
	if err := s.destructive("delete_comment"); err != nil {
		return nil, err
	}
	commentID := strings.TrimSpace(in.Comment)
	if commentID == "" {
		return nil, Errorf(ClassInvalid, "comment is required: list_comments shows the thread ids")
	}
	replyID := strings.TrimSpace(in.Reply)
	what := "comment " + commentID
	if replyID != "" {
		what = "reply " + replyID + " on comment " + commentID
	}
	if err := s.confirmed(in.Confirm, "delete_comment", what); err != nil {
		return nil, err
	}
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return nil, err
	}
	f := res.File
	if replyID != "" {
		if err := s.api.DeleteReply(ctx, f.ID, commentID, replyID); err != nil {
			return nil, s.commentError(err, f, commentID)
		}
	} else if err := s.api.DeleteComment(ctx, f.ID, commentID); err != nil {
		return nil, s.commentError(err, f, commentID)
	}
	return s.report(ctx, res, outcome{Action: render.ActionDeleted, Note: fmt.Sprintf(
		"%s on %s is deleted. Drive keeps the thread with its words removed, which is what list_comments "+
			"with include_deleted shows; there is no way back.", what, f.Name)}), nil
}

// commentable refuses before the call what Drive would refuse after it.
// capabilities.canComment is Drive's own answer, and a reader on a file
// shared read-only has it false.
func (s *Service) commentable(f *gdrive.File) error {
	if f.IsFolder() {
		return Errorf(ClassInvalid, "%s is a folder, and Drive has no comments on a folder.", f.Name)
	}
	if f.Capabilities != nil && !f.Capabilities.CanComment {
		return Errorf(ClassForbidden, "this account cannot comment on %s. Commenting needs at least the "+
			"commenter role; get_file shows what this account may do with it.", f.Name)
	}
	return nil
}

// commentError explains a refusal about one thread, since Drive's 404
// does not say which of the two things it could not find.
func (s *Service) commentError(err error, f *gdrive.File, commentID string) error {
	if gapi.Class(err) == ClassNotFound {
		return &Error{Class: ClassNotFound, Message: fmt.Sprintf(
			"%s has no comment %s that this account can see. list_comments shows the ids; a deleted thread "+
				"is only there with include_deleted, and it can no longer be replied to.", f.Name, commentID), Err: err}
	}
	return wrap(err, fmt.Sprintf("working on comment %s of %s", commentID, f.Name))
}

// commentWriteError adds what was being attempted to a refusal.
func (s *Service) commentWriteError(err error, f *gdrive.File, what string) error {
	if gapi.Class(err) == ClassForbidden {
		return &Error{Class: ClassForbidden, Message: fmt.Sprintf(
			"Google refused %s %s: %s. Commenting needs at least the commenter role, and a file whose "+
				"owner turned commenting off refuses it whatever the role.", what, f.Name, gapi.Message(err)), Err: err}
	}
	return wrap(err, what+" "+f.Name)
}
