package render

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/model"
)

// FileCardOptions tune a file card.
type FileCardOptions struct {
	// Now anchors the relative times; the zero value drops them.
	Now time.Time
	// FollowedShortcut names the shortcut a read followed, so the card
	// says which thing it is describing.
	FollowedShortcut string
	// Note is an extra line at the end, e.g. what a write changed.
	Note string
}

// FileCard renders everything known about one file. It is what get_file
// returns and what every write echoes back, so a person can see the
// state they just created without a second call.
func FileCard(f *model.File, o FileCardOptions) string {
	if f == nil {
		return "(no file)\n"
	}
	var b buf
	b.linef("%s — %s", f.Name, f.Kind)
	if o.FollowedShortcut != "" {
		b.linef("(followed the shortcut %s to get here)", o.FollowedShortcut)
	}
	b.field("id", f.ID)
	b.field("location", f.Location.String())
	b.field("link", f.Link)
	if f.IsShortcut && f.ShortcutTargetID != "" {
		target := f.ShortcutTargetID
		if f.ShortcutTargetKind != "" {
			target += " (" + f.ShortcutTargetKind + ")"
		}
		b.field("points at", target)
		b.line("note: organising, sharing and trashing act on the shortcut itself, not on its target")
	}
	if f.HasSize {
		size := model.HumanSize(f.Size)
		if f.MD5 != "" {
			size += fmt.Sprintf(" (md5 %s)", f.MD5)
		}
		b.field("size", size)
	}
	b.field("created", model.HumanTime(f.Created, o.Now))
	modified := model.HumanTime(f.Modified, o.Now)
	if f.ModifiedBy != "" {
		modified += " by " + f.ModifiedBy
	}
	b.field("modified", modified)
	b.field("owner", f.Owner)
	b.field("sharing", f.Sharing.Summary())
	b.field("you can", joinOr(f.Can, ""))
	if f.Starred {
		b.field("starred", "yes")
	}
	if f.Trashed {
		trashed := "yes"
		// My Drive records neither; only a shared drive says who and when.
		if f.TrashedBy != "" {
			trashed += ", by " + f.TrashedBy
			if !f.TrashedAt.IsZero() {
				trashed += " on " + model.HumanTime(f.TrashedAt, o.Now)
			}
		}
		b.field("trashed", trashed+" — it is in the trash, not deleted, and Drive empties the trash 30 days after an item goes in")
	}
	if f.ContentLocked {
		reason := f.ContentLockedReason
		if reason == "" {
			reason = "no reason given"
		}
		b.field("content locked", reason+" — edits will be refused until the restriction is removed")
	}
	b.field("description", f.Description)
	b.field("head revision", f.HeadRevisionID)
	if len(f.ExportFormats) > 0 {
		b.field("export formats", strings.Join(f.ExportFormats, ", "))
	}
	if len(f.Properties) > 0 {
		keys := make([]string, 0, len(f.Properties))
		for k := range f.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		pairs := make([]string, 0, len(keys))
		for _, k := range keys {
			pairs = append(pairs, k+"="+f.Properties[k])
		}
		b.field("properties", strings.Join(pairs, ", "))
	}
	if len(f.Labels) > 0 {
		names := make([]string, 0, len(f.Labels))
		for _, l := range f.Labels {
			names = append(names, l.ID)
		}
		sort.Strings(names)
		b.field("labels", strings.Join(names, ", "))
	}
	if f.CopyRequiresWriterPermission {
		b.line("note: viewers and commenters cannot copy, print or download this file")
	}
	if f.BoundaryNote != "" {
		b.line("note: " + f.BoundaryNote)
	}
	if o.Note != "" {
		b.line(o.Note)
	}
	return b.String()
}
