package drivetest

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// AddComment adds a thread to a file and returns it.
func (s *Server) AddComment(fileID, id, text string, opts ...CommentOpt) *gdrive.Comment {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := s.now().UTC().Format(time.RFC3339)
	c := &gdrive.Comment{
		ID: id, Content: text, CreatedTime: ts, ModifiedTime: ts, Author: s.me(),
	}
	for _, o := range opts {
		o(c)
	}
	s.Comments[fileID] = append(s.Comments[fileID], c)
	return c
}

// CommentOpt customises a comment being added.
type CommentOpt func(*gdrive.Comment)

// ByOther attributes the comment to somebody else. Drive does not
// populate an author's email address on a comment, so the fake does not
// either: a renderer that shows one would be showing something Drive
// never sends.
func ByOther(name string) CommentOpt {
	return func(c *gdrive.Comment) { c.Author = &gdrive.User{DisplayName: name} }
}

// Anchored pins the comment to a quoted passage, as a comment made in
// the Docs editor is.
func Anchored(anchor, quoted string) CommentOpt {
	return func(c *gdrive.Comment) {
		c.Anchor = anchor
		c.QuotedFileContent = &gdrive.QuotedFileContent{MimeType: "text/html", Value: quoted}
	}
}

// AssignedTo marks the comment as an action item for an address.
func AssignedTo(email string) CommentOpt {
	return func(c *gdrive.Comment) { c.AssigneeEmailAddress = email }
}

// WithReply appends a reply to the thread. An action of "resolve" or
// "reopen" sets the thread's resolved state, which is the only way Drive
// lets it be set.
func WithReply(id, author, text, action string) CommentOpt {
	return func(c *gdrive.Comment) {
		r := &gdrive.Reply{ID: id, Content: text, Action: action}
		if author != "" {
			r.Author = &gdrive.User{DisplayName: author}
		}
		c.Replies = append(c.Replies, r)
		switch action {
		case gdrive.ReplyActionResolve:
			c.Resolved = true
		case gdrive.ReplyActionReopen:
			c.Resolved = false
		}
	}
}

// serveComments routes the comment and reply endpoints under one file.
func (s *Server) serveComments(w http.ResponseWriter, r *http.Request, fileID, rest string) {
	// Drive REQUIRES the fields parameter on comments.list, get, create
	// and update, and answers 400 without it. The fake enforces it for
	// the reason it enforces every other rule: a fake that accepts what
	// Drive refuses lets the bug through to production.
	if r.Method != http.MethodDelete && !strings.Contains(rest, "/replies") &&
		r.URL.Query().Get("fields") == "" {
		s.errorJSON(w, http.StatusBadRequest, "invalid",
			"The 'fields' parameter is required for this method.")
		return
	}
	s.mu.Lock()
	if s.fileLocked(fileID) == nil {
		s.mu.Unlock()
		s.errorJSON(w, http.StatusNotFound, "notFound", "File not found: "+fileID+".")
		return
	}
	s.mu.Unlock()

	commentID, replyPart, hasReply := strings.Cut(rest, "/replies")
	switch {
	case commentID == "" && r.Method == http.MethodGet:
		s.handleListComments(w, r, fileID)
	case commentID == "" && r.Method == http.MethodPost:
		s.handleCreateComment(w, r, fileID)
	case hasReply:
		s.serveReplies(w, r, fileID, commentID, strings.TrimPrefix(replyPart, "/"))
	case r.Method == http.MethodGet:
		s.handleGetComment(w, r, fileID, commentID)
	case r.Method == http.MethodPatch:
		s.handleUpdateComment(w, r, fileID, commentID)
	case r.Method == http.MethodDelete:
		s.handleDeleteComment(w, fileID, commentID)
	default:
		s.errorJSON(w, http.StatusNotFound, "notFound", "the fake does not implement "+r.Method+" on a comment")
	}
}

func (s *Server) serveReplies(w http.ResponseWriter, r *http.Request, fileID, commentID, replyID string) {
	switch {
	case replyID == "" && r.Method == http.MethodPost:
		s.handleCreateReply(w, r, fileID, commentID)
	case replyID == "" && r.Method == http.MethodGet:
		s.handleListReplies(w, fileID, commentID)
	case r.Method == http.MethodPatch:
		s.handleUpdateReply(w, r, fileID, commentID, replyID)
	case r.Method == http.MethodDelete:
		s.handleDeleteReply(w, fileID, commentID, replyID)
	default:
		s.errorJSON(w, http.StatusNotFound, "notFound", "the fake does not implement "+r.Method+" on a reply")
	}
}

func (s *Server) handleListComments(w http.ResponseWriter, r *http.Request, fileID string) {
	q := r.URL.Query()
	includeDeleted := q.Get("includeDeleted") == "true"
	since := q.Get("startModifiedTime")

	s.mu.Lock()
	var matched []*gdrive.Comment
	for _, c := range s.Comments[fileID] {
		if c.Deleted && !includeDeleted {
			continue
		}
		if since != "" && c.ModifiedTime < since {
			continue
		}
		matched = append(matched, s.viewComment(c, includeDeleted))
	}
	s.mu.Unlock()
	// Drive returns comments oldest first; a stable order is what makes
	// paging honest.
	sort.SliceStable(matched, func(i, j int) bool { return matched[i].CreatedTime < matched[j].CreatedTime })

	pageSize := 20
	if n, err := strconv.Atoi(q.Get("pageSize")); err == nil && n > 0 {
		pageSize = n
	}
	if pageSize > 100 {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Invalid Value for pageSize")
		return
	}
	start := 0
	if tok := q.Get("pageToken"); tok != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(tok, "offset-"))
		if err != nil {
			s.errorJSON(w, http.StatusBadRequest, "invalid", "Invalid page token")
			return
		}
		start = n
	}
	if start > len(matched) {
		start = len(matched)
	}
	end := min(start+pageSize, len(matched))
	page := gdrive.CommentList{Comments: append([]*gdrive.Comment{}, matched[start:end]...)}
	if end < len(matched) {
		page.NextPageToken = "offset-" + strconv.Itoa(end)
	}
	writeJSON(w, page)
}

func (s *Server) handleGetComment(w http.ResponseWriter, r *http.Request, fileID, commentID string) {
	includeDeleted := r.URL.Query().Get("includeDeleted") == "true"
	s.mu.Lock()
	c := s.commentLocked(fileID, commentID)
	var out *gdrive.Comment
	if c != nil && (!c.Deleted || includeDeleted) {
		out = s.viewComment(c, includeDeleted)
	}
	s.mu.Unlock()
	if out == nil {
		s.errorJSON(w, http.StatusNotFound, "notFound", "Comment not found: "+commentID+".")
		return
	}
	writeJSON(w, out)
}

func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request, fileID string) {
	var meta gdrive.CommentMeta
	if !s.decodeJSON(w, r, &meta) {
		return
	}
	if strings.TrimSpace(meta.Content) == "" {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Comment content is required.")
		return
	}
	s.mu.Lock()
	f := s.fileLocked(fileID)
	if f.Capabilities != nil && !f.Capabilities.CanComment {
		s.mu.Unlock()
		s.errorJSON(w, http.StatusForbidden, "insufficientFilePermissions",
			"The user does not have sufficient permissions for this file.")
		return
	}
	s.nextID++
	ts := s.now().UTC().Format(time.RFC3339)
	c := &gdrive.Comment{
		ID: fmt.Sprintf("id-comment-%d", s.nextID), Content: meta.Content,
		CreatedTime: ts, ModifiedTime: ts, Author: s.me(),
	}
	s.Comments[fileID] = append(s.Comments[fileID], c)
	out := s.viewComment(c, false)
	s.mu.Unlock()
	writeJSON(w, out)
}

func (s *Server) handleUpdateComment(w http.ResponseWriter, r *http.Request, fileID, commentID string) {
	var meta gdrive.CommentMeta
	if !s.decodeJSON(w, r, &meta) {
		return
	}
	s.mu.Lock()
	c := s.commentLocked(fileID, commentID)
	if c == nil || c.Deleted {
		s.mu.Unlock()
		s.errorJSON(w, http.StatusNotFound, "notFound", "Comment not found: "+commentID+".")
		return
	}
	if strings.TrimSpace(meta.Content) == "" {
		s.mu.Unlock()
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Comment content is required.")
		return
	}
	c.Content = meta.Content
	c.ModifiedTime = s.now().UTC().Format(time.RFC3339)
	out := s.viewComment(c, false)
	s.mu.Unlock()
	writeJSON(w, out)
}

// handleDeleteComment leaves a tombstone: Drive keeps the comment with
// deleted set and its content stripped, which is why includeDeleted
// exists at all.
func (s *Server) handleDeleteComment(w http.ResponseWriter, fileID, commentID string) {
	s.mu.Lock()
	c := s.commentLocked(fileID, commentID)
	if c == nil || c.Deleted {
		s.mu.Unlock()
		s.errorJSON(w, http.StatusNotFound, "notFound", "Comment not found: "+commentID+".")
		return
	}
	c.Deleted = true
	c.Content = ""
	c.ModifiedTime = s.now().UTC().Format(time.RFC3339)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListReplies(w http.ResponseWriter, fileID, commentID string) {
	s.mu.Lock()
	c := s.commentLocked(fileID, commentID)
	if c == nil {
		s.mu.Unlock()
		s.errorJSON(w, http.StatusNotFound, "notFound", "Comment not found: "+commentID+".")
		return
	}
	out := gdrive.ReplyList{Replies: append([]*gdrive.Reply{}, c.Replies...)}
	s.mu.Unlock()
	writeJSON(w, out)
}

func (s *Server) handleCreateReply(w http.ResponseWriter, r *http.Request, fileID, commentID string) {
	var meta gdrive.ReplyMeta
	if !s.decodeJSON(w, r, &meta) {
		return
	}
	if meta.Action != "" && meta.Action != gdrive.ReplyActionResolve && meta.Action != gdrive.ReplyActionReopen {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Invalid Value for action: "+meta.Action)
		return
	}
	// The reference makes content required unless an action carries the
	// reply, and a reply with neither says nothing at all.
	if meta.Action == "" && strings.TrimSpace(meta.Content) == "" {
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Reply content is required.")
		return
	}
	s.mu.Lock()
	c := s.commentLocked(fileID, commentID)
	if c == nil || c.Deleted {
		s.mu.Unlock()
		s.errorJSON(w, http.StatusNotFound, "notFound", "Comment not found: "+commentID+".")
		return
	}
	s.nextID++
	ts := s.now().UTC().Format(time.RFC3339)
	reply := &gdrive.Reply{
		ID: fmt.Sprintf("id-reply-%d", s.nextID), Content: meta.Content, Action: meta.Action,
		CreatedTime: ts, ModifiedTime: ts, Author: s.me(),
	}
	c.Replies = append(c.Replies, reply)
	c.ModifiedTime = ts
	switch meta.Action {
	case gdrive.ReplyActionResolve:
		c.Resolved = true
	case gdrive.ReplyActionReopen:
		c.Resolved = false
	}
	out := *reply
	s.mu.Unlock()
	writeJSON(w, out)
}

func (s *Server) handleUpdateReply(w http.ResponseWriter, r *http.Request, fileID, commentID, replyID string) {
	var meta gdrive.ReplyMeta
	if !s.decodeJSON(w, r, &meta) {
		return
	}
	s.mu.Lock()
	reply := s.replyLocked(fileID, commentID, replyID)
	if reply == nil || reply.Deleted {
		s.mu.Unlock()
		s.errorJSON(w, http.StatusNotFound, "notFound", "Reply not found: "+replyID+".")
		return
	}
	if strings.TrimSpace(meta.Content) == "" {
		s.mu.Unlock()
		s.errorJSON(w, http.StatusBadRequest, "invalid", "Reply content is required.")
		return
	}
	reply.Content = meta.Content
	reply.ModifiedTime = s.now().UTC().Format(time.RFC3339)
	out := *reply
	s.mu.Unlock()
	writeJSON(w, out)
}

func (s *Server) handleDeleteReply(w http.ResponseWriter, fileID, commentID, replyID string) {
	s.mu.Lock()
	reply := s.replyLocked(fileID, commentID, replyID)
	if reply == nil || reply.Deleted {
		s.mu.Unlock()
		s.errorJSON(w, http.StatusNotFound, "notFound", "Reply not found: "+replyID+".")
		return
	}
	reply.Deleted = true
	reply.Content = ""
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// commentLocked finds a thread on a file. The caller holds the lock.
func (s *Server) commentLocked(fileID, commentID string) *gdrive.Comment {
	for _, c := range s.Comments[fileID] {
		if c.ID == commentID {
			return c
		}
	}
	return nil
}

// replyLocked finds one reply in a thread. The caller holds the lock.
func (s *Server) replyLocked(fileID, commentID, replyID string) *gdrive.Reply {
	c := s.commentLocked(fileID, commentID)
	if c == nil {
		return nil
	}
	for _, r := range c.Replies {
		if r.ID == replyID {
			return r
		}
	}
	return nil
}

// viewComment copies a thread for a response, dropping the deleted
// replies a caller did not ask for. The caller holds the lock.
func (s *Server) viewComment(c *gdrive.Comment, includeDeleted bool) *gdrive.Comment {
	out := *c
	out.Replies = nil
	for _, r := range c.Replies {
		if r.Deleted && !includeDeleted {
			continue
		}
		copied := *r
		out.Replies = append(out.Replies, &copied)
	}
	return &out
}
