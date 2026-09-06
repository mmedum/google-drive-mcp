package render

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/model"
)

// cardHead is how any result introduces the file it is about: what it
// is, whether a shortcut was followed to reach it, its id and where it
// lives. Every renderer starts this way, so it is written once.
func cardHead(b *buf, f *model.File, action Action, dryRun bool, followedShortcut string) {
	switch {
	case action != "" && dryRun:
		// The lead line is the one part of a result that is always read.
		// A dry run that leads with "moved" has already misinformed
		// whoever stopped there.
		b.linef("would have %s: %s — %s", action, f.Name, f.Kind)
	case action != "":
		b.linef("%s: %s — %s", action, f.Name, f.Kind)
	default:
		b.linef("%s — %s", f.Name, f.Kind)
	}
	if followedShortcut != "" {
		b.linef("(followed the shortcut %s to get here)", followedShortcut)
	}
	b.field("id", f.ID)
	b.field("location", f.Location.String())
}

// cardState is the part of a card that says what has happened to the
// file rather than what it is: starred, in the trash, or locked.
func cardState(b *buf, f *model.File, now time.Time) {
	if f.Starred {
		b.field("starred", "yes")
	}
	if f.Trashed {
		trashed := "yes"
		// My Drive records neither; only a shared drive says who and when.
		if f.TrashedBy != "" {
			trashed += ", by " + f.TrashedBy
			if !f.TrashedAt.IsZero() {
				trashed += " on " + model.HumanTime(f.TrashedAt, now)
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
}

// cardTags renders the custom properties and Workspace labels, both
// sorted so a card of one file always reads the same way.
func cardTags(b *buf, f *model.File) {
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
	writeAppliedLabels(b, f.Labels)
}

// writeAppliedLabels puts each label on its own line with the values set
// on it. A label whose definition this account cannot read has no title
// and no field names, so it is written by id: the id is still true, and
// saying nothing would hide a label that is on the file.
func writeAppliedLabels(b *buf, labels []model.AppliedLabel) {
	for _, l := range labels {
		name := l.Title
		if name == "" {
			name = l.ID
		}
		if len(l.Fields) == 0 {
			b.field("label", name)
			continue
		}
		pairs := make([]string, 0, len(l.Fields))
		for _, f := range l.Fields {
			key := f.DisplayName
			if key == "" {
				key = f.ID
			}
			pairs = append(pairs, key+"="+strings.Join(f.Values, "; "))
		}
		b.field("label", name+" ("+strings.Join(pairs, ", ")+")")
	}
}

// orEmpty renders a value that was not set, so a before-and-after line
// never reads as though a field went from nothing to nothing.
func orEmpty(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(empty)"
	}
	return v
}

// FileCardOptions tune a file card.
type FileCardOptions struct {
	// Now anchors the relative times; the zero value drops them.
	Now time.Time
	// FollowedShortcut names the shortcut a read followed, so the card
	// says which thing it is describing.
	FollowedShortcut string
	// Note is an extra line at the end, e.g. what a write changed.
	Note string
	// Action names what a write just did, so the card leads with the
	// change rather than with the state. Empty for a plain read.
	Action Action
	// DryRun marks a card that describes what would have happened. It is
	// said once, here, rather than in whatever sentence each caller
	// happened to write.
	DryRun bool
	// Changes are the fields a write altered, before and after.
	Changes []Change
}

// FileCard renders everything known about one file. It is what get_file
// returns and what every write echoes back, so a person can see the
// state they just created without a second call.
func FileCard(f *model.File, o FileCardOptions) string {
	if f == nil {
		return "(no file)\n"
	}
	var b buf
	cardHead(&b, f, o.Action, o.DryRun, o.FollowedShortcut)
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
	cardState(&b, f, o.Now)
	b.field("description", f.Description)
	b.field("head revision", f.HeadRevisionID)
	if len(f.ExportFormats) > 0 {
		b.field("export formats", strings.Join(f.ExportFormats, ", "))
	}
	cardTags(&b, f)
	if f.CopyRequiresWriterPermission {
		b.line("note: viewers and commenters cannot copy, print or download this file")
	}
	if f.BoundaryNote != "" {
		b.line("note: " + f.BoundaryNote)
	}
	if o.DryRun {
		b.line("NOTHING WAS CHANGED: this was a dry run. Call it again without dry_run to do it.")
	}
	if len(o.Changes) > 0 {
		b.line("changed:")
		for _, c := range o.Changes {
			b.linef("  %s: %s → %s", c.Field, orEmpty(c.From), orEmpty(c.To))
		}
	}
	if o.Note != "" {
		b.line("note: " + o.Note)
	}
	return b.String()
}
