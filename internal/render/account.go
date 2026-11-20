package render

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mmedum/google-drive-mcp/v3/internal/model"
)

// AccountOptions carry the settings a person needs to see alongside the
// account: which tools this server registered and where files may go.
type AccountOptions struct {
	ReadOnly    bool
	Destructive bool
	// Sharing is the configured value, "all" or "off".
	Sharing string
	// Personal is a personal Google account, which has no organization:
	// every address is outside it, so share_file grants nobody.
	Personal bool
	// Inside are the domains share_file counts as inside the organization.
	Inside []string
	// LocalDir is where transfers land; empty means transfers are off.
	LocalDir string
	// MaxDownload caps one download, in bytes.
	MaxDownload int64
	Labels      bool
	// Tools are the names the server actually registered. They are the
	// authority on what exists: describing the surface from the settings
	// alone would name tools a phase has not built yet.
	Tools []string
	// Drives are the shared drives the account can see, by name.
	Drives []string
	// DrivesKnown says whether the drive list was fetched at all.
	DrivesKnown bool
}

// Account renders who is signed in and what this server will let the
// model do on their behalf.
func Account(a *model.Account, o AccountOptions) string {
	var b buf
	if a == nil {
		b.line("not signed in")
		return b.String()
	}
	name := a.Name
	if name == "" {
		name = "(no display name)"
	}
	b.linef("%s <%s>", name, a.Address)
	b.field("storage", storageLine(a.Storage))

	if a.CanCreateDrives {
		b.field("shared drives", "this account can create them")
	} else {
		b.field("shared drives", "this account cannot create them (shared drives are a Google Workspace feature)")
	}
	if o.DrivesKnown {
		b.field("drives you can see", joinOr(o.Drives, "none"))
	}

	mode := "read and write"
	if o.ReadOnly {
		mode = "read-only: only the read tools are registered, and the login asked for read-only scopes"
	}
	b.field("this server", mode)

	b.field("tools registered", joinOr(o.Tools, "none"))

	switch o.Sharing {
	case "off":
		b.field("sharing", "GDRIVE_SHARING=off: no tool here can widen who can see a file.")
	default:
		domains := o.Inside
		if len(domains) == 0 {
			_, own, _ := strings.Cut(a.Address, "@")
			domains = []string{own}
		}
		inside := "share_file grants named people and groups inside this account's organization, which is " +
			"addresses at " + strings.Join(domains, ", ")
		if o.Personal {
			inside = "this is taken as a personal Google account, which has no organization, so every address " +
				"is outside it and share_file grants nobody; set GDRIVE_ORGANIZATION_DOMAINS if it belongs to one"
		}
		wide := "a link for anyone, a whole domain, someone outside the organization and a new owner need " +
			"GDRIVE_ENABLE_DESTRUCTIVE=true"
		if o.Destructive {
			wide = "share_by_link, share_outside_organization and transfer_ownership grant a link for anyone, a " +
				"whole domain, someone outside the organization and a new owner"
		}
		b.field("sharing", inside+"; "+wide+". What may actually be shared is decided by Google, under your "+
			"organization's own policy where there is one.")
	}
	if o.Destructive {
		b.field("destructive tools", "enabled (GDRIVE_ENABLE_DESTRUCTIVE=true): the tools that remove something with no way back are registered")
	} else {
		off := "disabled: nothing registered here removes anything permanently"
		if slices.Contains(o.Tools, "extract_text") {
			// The one exception, and a documented one: the temporary Doc
			// that OCR needs is the server's own, made and removed in the
			// same call.
			off += ", except the temporary copy extract_text makes to read a PDF or an image, which it deletes " +
				"for good unless keep_copy is set"
		}
		b.field("destructive tools", off+". Set GDRIVE_ENABLE_DESTRUCTIVE=true to change that.")
	}
	if o.LocalDir == "" {
		b.field("file transfer", "off: no local directory is set, so nothing can be downloaded or uploaded. Inline text still works both ways.")
	} else {
		b.field("file transfer", fmt.Sprintf("%s (downloads land there, uploads are read from there; at most %s per download)",
			o.LocalDir, model.HumanSize(o.MaxDownload)))
	}
	if o.Labels {
		b.field("labels", "enabled")
	}
	return b.String()
}

func storageLine(q *model.Storage) string {
	if q == nil {
		return ""
	}
	if !q.Limited {
		return fmt.Sprintf("%s used, no limit on this account (%s in Drive, %s in the trash)",
			model.HumanSize(q.Used), model.HumanSize(q.InDrive), model.HumanSize(q.InTrash))
	}
	return fmt.Sprintf("%s of %s used (%s; %s in Drive, %s in the trash)",
		model.HumanSize(q.Used), model.HumanSize(q.Limit), percentUsed(q.Used, q.Limit),
		model.HumanSize(q.InDrive), model.HumanSize(q.InTrash))
}

// percentUsed renders a share of the quota. A large allowance makes a
// real amount round to "0%", which reads as a broken number rather than
// as plenty of room.
func percentUsed(usage, limit int64) string {
	if limit <= 0 {
		return "unknown"
	}
	pct := 100 * float64(usage) / float64(limit)
	switch {
	case pct > 0 && pct < 0.1:
		return "under 0.1%"
	case pct < 10:
		return fmt.Sprintf("%.1f%%", pct)
	default:
		return fmt.Sprintf("%.0f%%", pct)
	}
}
