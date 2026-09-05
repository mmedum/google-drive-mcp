package render

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/mmedum/google-drive-mcp/internal/model"
)

// DrivesOptions tune a shared-drive listing.
type DrivesOptions struct {
	Title string
	// HiddenShown says hidden drives are in the list, so their marker
	// means something rather than looking like an oddity.
	HiddenShown bool
	// Note is a closing line.
	Note string
	// Empty is what to say when there is nothing.
	Empty string
}

// Drives renders the shared drives an account can see. The id is in
// every row because it is what every other tool takes; the name is not
// unique and never was.
func Drives(drives []*model.Drive, o DrivesOptions) string {
	var b buf
	if o.Title != "" {
		b.line(o.Title)
	}
	if len(drives) == 0 {
		b.line(orDefault(o.Empty, "(no shared drives)"))
		if o.Note != "" {
			b.line(o.Note)
		}
		return b.String()
	}
	var table strings.Builder
	w := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
	for _, d := range drives {
		if d == nil {
			continue
		}
		name := d.Name
		if d.Hidden {
			name += " (hidden)"
		}
		_, _ = fmt.Fprintln(w, strings.Join([]string{name, d.ID, "you can: " + joinOr(d.Can, "nothing here")}, "\t"))
	}
	_ = w.Flush()
	b.sb.WriteString(table.String())

	for _, d := range drives {
		if d != nil && len(d.Restrictions) > 0 {
			b.linef("%s is restricted: %s", d.Name, strings.Join(d.Restrictions, "; "))
		}
	}
	if o.HiddenShown {
		b.line("a hidden drive is only out of the sidebar's default view; it is as reachable as any other")
	}
	if o.Note != "" {
		b.line(o.Note)
	}
	return b.String()
}

// DriveCard renders one shared drive, which is what manage_drive echoes
// back so a change can be seen without a second call.
func DriveCard(d *model.Drive, o DriveCardOptions) string {
	if d == nil {
		return "(no shared drive)\n"
	}
	var b buf
	switch {
	case o.Action != "" && o.DryRun:
		b.linef("would have %s the shared drive: %s", o.Action, d.Name)
	case o.Action != "":
		b.linef("%s the shared drive: %s", o.Action, d.Name)
	default:
		b.linef("%s — shared drive", d.Name)
	}
	b.field("id", d.ID)
	if d.Hidden {
		b.field("hidden", "yes — out of the sidebar's default view, but as reachable as any other")
	}
	b.field("you can", joinOr(d.Can, "nothing here"))
	b.field("restrictions", joinOr(d.Restrictions, "none"))
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

// DriveCardOptions tune a shared drive's card.
type DriveCardOptions struct {
	Action  Action
	DryRun  bool
	Changes []Change
	Note    string
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
