package render

import (
	"fmt"
	"strconv"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
)

// AccountOptions carry the settings a person needs to see alongside the
// account: which tools this server registered and where files may go.
type AccountOptions struct {
	ReadOnly    bool
	Destructive bool
	// Sharing is the configured value, "all" or "off".
	Sharing string
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
func Account(about *gdrive.About, o AccountOptions) string {
	var b buf
	if about == nil || about.User == nil {
		b.line("not signed in")
		return b.String()
	}
	name := about.User.DisplayName
	if name == "" {
		name = "(no display name)"
	}
	b.linef("%s <%s>", name, about.User.EmailAddress)
	b.field("storage", storageLine(about.StorageQuota))

	if about.CanCreateDrives {
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
		b.field("sharing", "GDRIVE_SHARING=off: no tool here can change who can see a file.")
	default:
		b.field("sharing", "an anyone-with-the-link grant needs allow_anyone: true on the call. "+
			"What may actually be shared is decided by your organisation's own policy, which Google enforces on every call.")
	}
	if o.Destructive {
		b.field("destructive tools", "enabled (GDRIVE_ENABLE_DESTRUCTIVE=true): the tools that remove something with no way back are registered")
	} else {
		b.field("destructive tools", "disabled: nothing registered here removes anything permanently. Set GDRIVE_ENABLE_DESTRUCTIVE=true to change that.")
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

func storageLine(q *gdrive.StorageQuota) string {
	if q == nil {
		return ""
	}
	usage := parseBytes(q.Usage)
	inDrive := parseBytes(q.UsageInDrive)
	inTrash := parseBytes(q.UsageInDriveTrash)
	if q.Limit == "" {
		return fmt.Sprintf("%s used, no limit on this account (%s in Drive, %s in the trash)",
			model.HumanSize(usage), model.HumanSize(inDrive), model.HumanSize(inTrash))
	}
	limit := parseBytes(q.Limit)
	pct := 0.0
	if limit > 0 {
		pct = 100 * float64(usage) / float64(limit)
	}
	return fmt.Sprintf("%s of %s used (%.0f%%; %s in Drive, %s in the trash)",
		model.HumanSize(usage), model.HumanSize(limit), pct, model.HumanSize(inDrive), model.HumanSize(inTrash))
}

// parseBytes reads the string form Drive uses for int64 counts.
func parseBytes(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
