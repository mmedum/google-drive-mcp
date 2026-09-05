package render

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/mmedum/google-drive-mcp/internal/model"
)

// PermissionsOptions tune a permission listing.
type PermissionsOptions struct {
	// Subject heads the listing: the file card's first line, or a shared
	// drive's name.
	Subject string
	// Location is where the subject sits, empty for a shared drive.
	Location string
	// Sharing is the exposure summary, which is the sentence a person
	// reads before the table of grants.
	Sharing model.Sharing
	// SharedDrive names the drive when the subject is one, so the notes
	// can say that these grants are its membership.
	SharedDrive bool
	// CanShare is Drive's own answer to whether this account may change
	// any of it. A listing that does not say so invites a share_file that
	// is refused.
	CanShare bool
	// Note is a closing line.
	Note string
}

// Permissions renders who has access and how. The permission id is in
// every row because it is what unshare_file takes when a principal
// cannot be named — an inherited grant, or one whose address Drive
// withholds.
func Permissions(grants []model.Grant, o PermissionsOptions) string {
	var b buf
	if o.Subject != "" {
		b.line(o.Subject)
	}
	b.field("location", o.Location)
	b.field("exposure", o.Sharing.Summary())

	if len(grants) == 0 {
		if o.Sharing.Unknown {
			b.line("this account cannot read the permission list, so what is here is unknown")
		} else {
			b.line("no grants: nobody but the owner has been given access")
		}
		writePermissionNotes(&b, grants, o)
		return b.String()
	}

	var table strings.Builder
	w := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
	for _, g := range grants {
		cells := []string{model.RoleWords(g.Role), g.Label(), g.PermissionID, grantFlags(g)}
		_, _ = fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
	_ = w.Flush()
	b.sb.WriteString(table.String())
	writePermissionNotes(&b, grants, o)
	return b.String()
}

// grantFlags says what is true of one grant beyond its role: when it
// ends, where it came from, and whether it is findable by search rather
// than only by link.
func grantFlags(g model.Grant) string {
	var flags []string
	switch g.Type {
	case "anyone", "domain":
		if g.Discoverable {
			flags = append(flags, "findable by search")
		} else {
			flags = append(flags, "by link only")
		}
	}
	if g.Expires != "" {
		flags = append(flags, "expires "+g.Expires)
	}
	if g.PendingOwner {
		flags = append(flags, "ownership transfer waiting to be accepted")
	}
	if g.Inherited() {
		flags = append(flags, "inherited from "+g.InheritedFrom)
	}
	if g.Deleted {
		flags = append(flags, "the account behind it no longer exists")
	}
	return strings.Join(flags, ", ")
}

// writePermissionNotes closes the listing with what a caller has to know
// before acting on it.
func writePermissionNotes(b *buf, grants []model.Grant, o PermissionsOptions) {
	if o.SharedDrive {
		b.line("these are the shared drive's members; everything in the drive inherits them")
	}
	inherited := 0
	for _, g := range grants {
		if g.Inherited() {
			inherited++
		}
	}
	if inherited > 0 {
		b.linef("%s here %s inherited and can only be removed where it was granted, not on this file",
			model.Plural(inherited, "grant", "grants"), isAre(inherited))
	}
	if !o.CanShare {
		b.line("this account cannot change who can see this, so share_file and unshare_file will be refused on it")
	}
	if o.Note != "" {
		b.line(o.Note)
	}
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// Exposure renders how access changed, which is the point of a sharing
// result: the grant that was added is the small half, and who can now
// reach the file is the half a person actually has to check.
func Exposure(before, after model.Sharing) []Change {
	from, to := before.Summary(), after.Summary()
	if from == to {
		return nil
	}
	return []Change{{Field: "who can see it", From: from, To: to}}
}
