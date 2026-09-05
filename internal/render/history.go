package render

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/model"
)

// RevisionsOptions tune a revision listing.
type RevisionsOptions struct {
	// Subject heads the listing: the file this history belongs to.
	Subject string
	Now     time.Time
	// Workspace marks a Google-native document, whose history Drive
	// reports differently and warns is not always complete.
	Workspace bool
	// Note is a closing line.
	Note string
}

// Revisions renders a file's version history, newest first. Google's own
// caveat about an incomplete list for a busy Docs editors file is
// repeated rather than hidden: a history that looks complete and is not
// is worse than one that says so.
func Revisions(revs []*model.Revision, o RevisionsOptions) string {
	var b buf
	if o.Subject != "" {
		b.line(o.Subject)
	}
	if len(revs) == 0 {
		b.line("no revisions: Drive is keeping no earlier version of this file")
		writeRevisionNotes(&b, o)
		return b.String()
	}
	var table strings.Builder
	w := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
	for _, r := range revs {
		when := model.HumanTime(r.Modified, o.Now)
		if r.By != "" {
			when += " by " + r.By
		}
		size := "no size of its own"
		if r.HasSize {
			size = model.HumanSize(r.Size)
		}
		_, _ = fmt.Fprintln(w, strings.Join([]string{r.ID, when, size, revisionFlags(r)}, "\t"))
	}
	_ = w.Flush()
	b.sb.WriteString(table.String())
	writeRevisionNotes(&b, o)
	return b.String()
}

func revisionFlags(r *model.Revision) string {
	var flags []string
	if r.Head {
		flags = append(flags, "current")
	}
	if r.KeepForever {
		flags = append(flags, "kept forever")
	}
	if r.OriginalFilename != "" {
		flags = append(flags, "uploaded as "+r.OriginalFilename)
	}
	return strings.Join(flags, ", ")
}

func writeRevisionNotes(b *buf, o RevisionsOptions) {
	if o.Workspace {
		b.line("this is a Google document, and Drive says its revision list can be incomplete: " +
			"a busy Doc keeps more history in the editor than it reports here")
	} else {
		b.line("Drive discards a revision 30 days after it stops being current unless it is kept forever; " +
			"manage_revision pins one, and download_file with revision fetches its content")
	}
	if o.Note != "" {
		b.line(o.Note)
	}
}

// ChangesOptions tune a rendering of the changes feed.
type ChangesOptions struct {
	Title string
	Now   time.Time
	// NewStartToken is the token to carry into the next call. It is the
	// whole point of the feed, so it is never omitted.
	NewStartToken string
	// NextPageToken means this page did not exhaust the feed.
	NextPageToken string
	// Scope names the shared drive the feed was limited to.
	Scope string
	Note  string
}

// Changes renders one page of the changes feed. A removal and a trashing
// are said differently, because one can be undone and the other cannot.
func Changes(changes []*model.Change, o ChangesOptions) string {
	var b buf
	if o.Title != "" {
		b.line(o.Title)
	}
	if len(changes) == 0 {
		b.line("nothing has changed since that token")
	} else {
		var table strings.Builder
		w := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
		for _, c := range changes {
			name := c.Name
			if name == "" {
				name = "(no longer readable)"
			}
			cells := []string{changeWords(c), name, c.ID, c.Location, model.HumanTime(c.At, o.Now)}
			_, _ = fmt.Fprintln(w, strings.Join(cells, "\t"))
		}
		_ = w.Flush()
		b.sb.WriteString(table.String())
	}
	if o.NextPageToken != "" {
		b.linef("more changes on this page token: call again with page_token %q", o.NextPageToken)
	}
	if o.NewStartToken != "" {
		b.linef("call again later with page_token %q to see what changes after this point", o.NewStartToken)
	}
	if o.Note != "" {
		b.line(o.Note)
	}
	return b.String()
}

// changeWords says what happened in the terms that decide what to do
// about it.
func changeWords(c *model.Change) string {
	if c.Kind == "drive" {
		if c.Removed {
			return "shared drive gone"
		}
		return "shared drive changed"
	}
	switch {
	case c.Removed:
		return "gone (deleted or no longer shared with you)"
	case c.Trashed:
		return "trashed (restore_file brings it back)"
	default:
		return "changed"
	}
}
