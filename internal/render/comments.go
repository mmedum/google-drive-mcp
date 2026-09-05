package render

import (
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/model"
)

// CommentsOptions tune a listing of comment threads.
type CommentsOptions struct {
	// Subject heads the listing: the file the threads are on.
	Subject string
	// Location is where that file sits.
	Location string
	Now      time.Time
	// CanComment is Drive's own answer to whether this account may add
	// to the conversation. A listing that does not say so invites an
	// add_comment that is refused.
	CanComment bool
	// Workspace marks a Google document, whose comments people make in
	// the editor and pin to a passage.
	Workspace bool
	// NextPageToken means this page did not exhaust the threads.
	NextPageToken string
	// IncludedDeleted records that the caller asked for tombstones, so
	// an empty result can say which question it answered.
	IncludedDeleted bool
	Note            string
}

// Comments renders a file's threads oldest first, each with its replies
// indented under it. Open and resolved are said in words rather than
// left to a column of flags: whether a thread still wants an answer is
// the reason to read the list at all.
func Comments(threads []*model.Comment, o CommentsOptions) string {
	var b buf
	if o.Subject != "" {
		b.line(o.Subject)
	}
	b.field("location", o.Location)
	if len(threads) == 0 {
		if o.IncludedDeleted {
			b.line("no comments: nobody has commented on this file, and none has been deleted either")
		} else {
			b.line("no comments: nobody has commented on this file")
		}
		writeCommentNotes(&b, o)
		return b.String()
	}
	for i, c := range threads {
		if c == nil {
			continue
		}
		if i > 0 {
			b.line("")
		}
		writeThread(&b, c, o.Now)
	}
	writeCommentNotes(&b, o)
	return b.String()
}

func writeThread(b *buf, c *model.Comment, now time.Time) {
	b.linef("%s  %s  %s", c.ID, threadState(c), byWhen(c.By, c.Created, now))
	if c.Quoted != "" {
		b.linef("  on: %s", oneLine(c.Quoted))
	} else if c.Anchored {
		b.line("  on: a passage of the document")
	}
	if c.Deleted {
		b.line("  (deleted; Drive keeps the thread and drops what it said)")
	} else {
		writeQuotedText(b, c.Text, "  ")
	}
	for _, r := range c.Replies {
		if r == nil {
			continue
		}
		b.linef("  %s  %s%s", r.ID, byWhen(r.By, r.Created, now), replyAction(r))
		switch {
		case r.Deleted:
			b.line("    (deleted)")
		case strings.TrimSpace(r.Text) == "" && r.Action != "":
			// A reply that only resolved or reopened the thread has no
			// words, and the line above has already said what it did.
			// The live run printed an empty pair of quotes here, which
			// reads as somebody having said nothing on purpose.
		default:
			writeQuotedText(b, r.Text, "    ")
		}
	}
}

// threadState is the fact that decides whether a thread needs anybody's
// attention.
func threadState(c *model.Comment) string {
	switch {
	case c.Deleted:
		return "deleted"
	case c.Resolved:
		return "resolved"
	default:
		return "open"
	}
}

func replyAction(r *model.Reply) string {
	switch r.Action {
	case "resolve":
		return "  (resolved the thread)"
	case "reopen":
		return "  (reopened the thread)"
	default:
		return ""
	}
}

func byWhen(by string, at, now time.Time) string {
	when := model.HumanTime(at, now)
	if by == "" {
		return when
	}
	return by + ", " + when
}

// writeQuotedText indents somebody's words so they cannot be mistaken
// for this server's. Comment text is written by whoever can reach the
// file, so it arrives as data and is laid out as data.
func writeQuotedText(b *buf, text, indent string) {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		b.line(indent + `""`)
		return
	}
	for _, line := range strings.Split(text, "\n") {
		b.line(indent + "| " + strings.TrimRight(line, " \t"))
	}
}

// oneLine flattens a quoted passage onto one line, bounded, so a comment
// pinned to a long paragraph does not bury the comment itself.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const limit = 120
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}

func writeCommentNotes(b *buf, o CommentsOptions) {
	if o.NextPageToken != "" {
		b.linef("more threads: call again with page_token %q", o.NextPageToken)
	}
	if o.Workspace {
		b.line("this is a Google document: a comment pinned to a passage was made in the editor, and " +
			"add_comment here makes an unpinned one, because pinning belongs to the Docs API")
	}
	if !o.CanComment {
		b.line("this account cannot comment on this file, so add_comment and reply_comment will be refused")
	}
	if o.Note != "" {
		b.line(o.Note)
	}
}

// AccessRequestsOptions tune a listing of pending access requests.
type AccessRequestsOptions struct {
	Subject  string
	Location string
	Now      time.Time
	// CanShare is whether this account may grant what a request asks
	// for. Only an approver can list these at all, so a false here is
	// worth saying loudly.
	CanShare bool
	// SharingOff records that the deployment turned the sharing tools
	// off, so the list is readable and nothing can act on it.
	SharingOff bool
	Note       string
}

// AccessRequests renders who is waiting to be let in and what they asked
// for.
func AccessRequests(reqs []*model.AccessRequest, o AccessRequestsOptions) string {
	var b buf
	if o.Subject != "" {
		b.line(o.Subject)
	}
	b.field("location", o.Location)
	if len(reqs) == 0 {
		b.line("no pending requests: nobody is waiting for access to this file")
		writeRequestNotes(&b, o)
		return b.String()
	}
	for i, r := range reqs {
		if r == nil {
			continue
		}
		if i > 0 {
			b.line("")
		}
		b.linef("%s  %s asked %s", r.ID, r.By, model.HumanTime(r.At, o.Now))
		if r.For != "" && r.For != r.By {
			b.linef("  for: %s", r.For)
		}
		b.linef("  wants: %s", requestedRoles(r))
		if r.View != "" {
			b.linef("  view: %s", r.View)
		}
		if strings.TrimSpace(r.Message) != "" {
			writeQuotedText(&b, r.Message, "  ")
		}
	}
	writeRequestNotes(&b, o)
	return b.String()
}

func requestedRoles(r *model.AccessRequest) string {
	if len(r.Roles) == 0 {
		return "access, without naming a role"
	}
	words := make([]string, 0, len(r.Roles))
	for _, role := range r.Roles {
		words = append(words, model.RoleWords(role))
	}
	return strings.Join(words, " or ")
}

func writeRequestNotes(b *buf, o AccessRequestsOptions) {
	switch {
	case o.SharingOff:
		b.line("this deployment has sharing switched off, so these can be read here and answered only in Drive")
	case !o.CanShare:
		b.line("this account cannot change who may see this file, so resolve_access_request will be refused")
	}
	if o.Note != "" {
		b.line(o.Note)
	}
}
