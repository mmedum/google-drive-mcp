package model

import (
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// Activity is one thing that happened to a file, as this server reports
// it.
//
// The honest limit of this resource: the Drive Activity API identifies a
// person by a People API resource name and nothing else — no display
// name, no address. So Who below is "you" or "somebody else" or "the
// system", and never a name. Saying more would mean a third API and a
// third scope for a decoration.
type Activity struct {
	// What is the action in plain words: created, edited, renamed, moved,
	// shared, commented on, deleted, restored.
	What string
	// Detail carries what the action detail adds, where it adds
	// something: the old and new names of a rename, for instance.
	Detail string
	// Who is as much as the API will say.
	Who string
	// When is the instant, or the end of the range for a consolidated
	// activity.
	When time.Time
	// Over is set for a consolidated activity, and is where it started.
	Over time.Time
	// Items are the files and folders the activity was about, by title.
	Items []string
}

// NewActivity converts one wire activity. It returns nil for an activity
// whose action this server cannot name, rather than reporting an event
// that says nothing.
func NewActivity(a *gdrive.DriveActivity) *Activity {
	if a == nil {
		return nil
	}
	what, detail := actionWords(a.PrimaryActionDetail)
	if what == "" {
		return nil
	}
	out := &Activity{What: what, Detail: detail, Who: actorWords(a.Actors)}
	out.When = parseTime(a.Timestamp)
	if a.TimeRange != nil {
		out.Over = parseTime(a.TimeRange.StartTime)
		if end := parseTime(a.TimeRange.EndTime); !end.IsZero() {
			out.When = end
		}
	}
	for _, t := range a.Targets {
		if t == nil || t.DriveItem == nil {
			continue
		}
		if title := t.DriveItem.Title; title != "" {
			out.Items = append(out.Items, title)
		}
	}
	return out
}

// actionWords names the action. The API has no action-type field: WHICH
// member of the detail is set is the answer, so this switch is the whole
// vocabulary rather than a translation of one.
func actionWords(d *gdrive.ActionDetail) (what, detail string) {
	switch {
	case d == nil:
		return "", ""
	case d.Create != nil:
		switch {
		case d.Create.Upload != nil:
			return "uploaded", ""
		case d.Create.Copy != nil:
			return "copied", ""
		default:
			return "created", ""
		}
	case d.Edit != nil:
		return "edited", ""
	case d.Rename != nil:
		return "renamed", renameWords(d.Rename)
	case d.Move != nil:
		return "moved", ""
	case d.Delete != nil:
		if d.Delete.Type == "PERMANENT_DELETE" {
			return "deleted permanently", ""
		}
		return "trashed", ""
	case d.Restore != nil:
		return "restored", ""
	case d.PermissionChange != nil:
		return "changed who can see it", permissionWords(d.PermissionChange)
	case d.Comment != nil:
		return "commented", ""
	case d.AppliedLabelChange != nil:
		return "changed a label on it", ""
	case d.DLPChange != nil:
		return "changed its data-protection state", ""
	case d.SettingsChange != nil:
		return "changed a shared drive setting", ""
	case d.Reference != nil:
		return "referenced it elsewhere", ""
	}
	return "", ""
}

// renameWords says what a rename changed it from and to.
func renameWords(r *gdrive.ActivityRename) string {
	switch {
	case r.OldTitle != "" && r.NewTitle != "":
		return "from " + r.OldTitle + " to " + r.NewTitle
	case r.NewTitle != "":
		return "to " + r.NewTitle
	}
	return ""
}

// permissionWords counts the grants added and removed. It counts rather
// than names them: the permissions this API returns carry no address
// either, for the same reason the actor does not.
func permissionWords(p *gdrive.ActivityPermission) string {
	var parts []string
	if n := len(p.AddedPermissions); n > 0 {
		parts = append(parts, Plural(n, "grant added", "grants added"))
	}
	if n := len(p.RemovedPermissions); n > 0 {
		parts = append(parts, Plural(n, "grant removed", "grants removed"))
	}
	return strings.Join(parts, ", ")
}

// actorWords says who, as far as the API will say. It never returns a
// name, because the API never gives one.
func actorWords(actors []*gdrive.ActivityActor) string {
	for _, a := range actors {
		if a == nil {
			continue
		}
		switch {
		case a.User != nil && a.User.KnownUser != nil:
			if a.User.KnownUser.IsCurrentUser {
				return "you"
			}
			return "somebody else"
		case a.User != nil && a.User.DeletedUser != nil:
			return "an account that has since been deleted"
		case a.Administrator != nil:
			return "an administrator"
		case a.Impersonation != nil:
			return "an administrator acting as somebody"
		case a.System != nil:
			return "Drive itself"
		case a.Anonymous != nil:
			return "somebody not signed in"
		}
	}
	return "somebody"
}
