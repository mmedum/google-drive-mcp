package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// Location is where a file sits. Names are not unique in Drive and a
// file's meaning depends on where it is, so every result carries one.
type Location struct {
	// Drive is the containing drive's name: "My Drive", or a shared
	// drive's name.
	Drive string
	// SharedDrive marks a shared drive, which is labelled as such
	// because its rules differ from My Drive's.
	SharedDrive bool
	// Folders are the folder names from the drive root down to the
	// file's parent.
	Folders []string
	// Above means there are more folders between the drive and the first
	// name shown: the walk ran out of budget or hit a folder it could not
	// read. It renders as an ellipsis in that position, because putting
	// it at the end would state a hierarchy that is not true.
	Above bool
	// SharedWithMe marks a file that lives in someone else's Drive and
	// reaches this account only through a share.
	SharedWithMe bool
	// Orphaned means the file has no parent this account can see.
	Orphaned bool
}

// Child is the location of something inside this one. A destination is
// built here rather than by appending a name to a rendered path: three
// of the forms String produces are not paths at all, and a name glued
// onto "Shared with me" reads like a folder that does not exist.
//
// A folder whose own parent is invisible does not make its contents
// orphaned — they have a folder, and it is this one — but everything
// above it is still unknown. That is what Above is for, and putting the
// gap where it belongs is the point: "My Drive/Orphan" would assert a
// parent nobody has seen, which is the one claim this type exists to
// avoid making.
func (l Location) Child(name string) Location {
	out := l
	if l.Orphaned || l.SharedWithMe {
		out.Orphaned, out.SharedWithMe, out.Above = false, false, true
	}
	out.Folders = append(append([]string(nil), l.Folders...), name)
	return out
}

// String renders the location the way results show it.
func (l Location) String() string {
	switch {
	case l.SharedWithMe:
		return "Shared with me"
	case l.Orphaned:
		return "(no folder this account can see)"
	}
	head := l.Drive
	if head == "" {
		head = "My Drive"
	}
	if l.SharedDrive {
		head += " (shared drive)"
	}
	parts := []string{head}
	if l.Above {
		parts = append(parts, "…")
	}
	parts = append(parts, l.Folders...)
	return strings.Join(parts, "/")
}

// File is the server's view of one Drive item.
type File struct {
	ID          string
	Name        string
	Kind        string
	MimeType    string
	Description string
	Link        string
	Location    Location

	IsFolder   bool
	IsShortcut bool
	// IsDriveRoot marks the top of My Drive or of a shared drive, whose
	// name the location already carries.
	IsDriveRoot bool
	// ShortcutTargetID and ShortcutTargetKind describe what a shortcut
	// points at; organising and sharing act on the shortcut itself.
	ShortcutTargetID   string
	ShortcutTargetKind string

	Size     int64
	HasSize  bool
	MD5      string
	Created  time.Time
	Modified time.Time
	// ModifiedBy is who last changed it, in plain words.
	ModifiedBy string
	Owner      string
	OwnedByMe  bool

	Starred bool
	Trashed bool
	// TrashedBy and TrashedAt are only populated in shared drives; My
	// Drive does not record them.
	TrashedBy string
	TrashedAt time.Time

	Sharing Sharing
	Can     []string

	DriveID        string
	ResourceKey    string
	HeadRevisionID string
	// ExportFormats are the formats a Workspace document can be exported
	// to; empty for a blob.
	ExportFormats []string
	Properties    map[string]string
	// Labels are the applied Workspace labels, when they were requested.
	// Their titles and field names come from the definitions where those
	// were available; without them a label is still reported, by id.
	Labels []AppliedLabel
	// WritersCanShare and CopyRequiresWriterPermission are the two
	// sharing switches a file carries.
	WritersCanShare              bool
	CopyRequiresWriterPermission bool
	// ContentLocked reports a content restriction, which makes a write
	// fail however good the caller's role is.
	ContentLocked       bool
	ContentLockedReason string
	// BoundaryNote says where a Google-native document's content is
	// edited, since it is not edited here.
	BoundaryNote string
}

// Options tune how a wire file becomes a model file.
type Options struct {
	// Location is the resolved folder path; the service computes it.
	Location Location
	// Permissions override the file's own permission list, for the
	// shared-drive case where a separate listing is needed.
	Permissions []*gdrive.Permission
	// PermissionsKnown says the list above was actually read. Without it
	// an empty list and an unreadable one are the same value, and the
	// summary would have to guess which.
	PermissionsKnown bool
	// ExportFormats are the short format names derived from exportLinks.
	ExportFormats []string
	// IsDriveRoot marks the top of a drive.
	IsDriveRoot bool
	// SharedDriveName names the shared drive the file lives in, so the
	// sharing summary can say that drive access reaches it.
	SharedDriveName string
	// LabelDefinitions, by label id, name the labels applied to the file
	// and the fields inside them. It is allowed to be empty or partial:
	// the definitions live behind a separate API and a separate scope, so
	// a label whose definition is out of reach is reported by id.
	LabelDefinitions map[string]*LabelDefinition
}

// New builds the model view of a Drive file.
func New(f *gdrive.File, o Options) *File {
	if f == nil {
		return nil
	}
	m := &File{
		ID:          f.ID,
		Name:        f.Name,
		Kind:        Kind(f),
		MimeType:    f.MimeType,
		Description: f.Description,
		Link:        f.WebViewLink,
		Location:    o.Location,
		IsFolder:    f.IsFolder(),
		IsShortcut:  f.IsShortcut(),
		IsDriveRoot: o.IsDriveRoot,
		MD5:         f.MD5Checksum,

		Starred:   f.Starred,
		Trashed:   f.Trashed,
		OwnedByMe: f.OwnedByMe,

		DriveID:        f.DriveID,
		ResourceKey:    f.ResourceKey,
		HeadRevisionID: f.HeadRevisionID,
		ExportFormats:  o.ExportFormats,
		Properties:     f.Properties,

		WritersCanShare:              f.WritersCanShare,
		CopyRequiresWriterPermission: f.CopyRequiresWriterPermission,
	}
	if f.ShortcutDetails != nil {
		m.ShortcutTargetID = f.ShortcutDetails.TargetID
		if t := f.ShortcutDetails.TargetMimeType; t != "" {
			m.ShortcutTargetKind = KindName(t)
		}
	}
	// A Google-native document's "size" is the metadata Drive keeps for
	// it, not the size of anything a person can get: a new, empty Doc
	// reports one byte, and so does a long one. Showing it invites a
	// reading it cannot support, and the export formats line already says
	// what can actually be had.
	if !f.IsWorkspaceDoc() {
		m.Size, m.HasSize = f.SizeBytes()
	}
	m.Created = parseTime(f.CreatedTime)
	m.Modified = parseTime(f.ModifiedTime)
	m.TrashedAt = parseTime(f.TrashedTime)
	m.ModifiedBy = userWords(f.LastModifyingUser)
	m.TrashedBy = userWords(f.TrashingUser)
	if len(f.Owners) > 0 {
		m.Owner = userWords(f.Owners[0])
	}
	perms, known := o.Permissions, o.PermissionsKnown
	if perms == nil && !known {
		perms, known = f.Permissions, f.Permissions != nil
	}
	m.Sharing = NewSharing(f.Shared, perms, known)
	if f.DriveID != "" {
		m.Sharing.SharedDrive = o.SharedDriveName
		if m.Sharing.SharedDrive == "" {
			m.Sharing.SharedDrive = o.Location.Drive
		}
	}
	if m.Sharing.Owner != "" && m.Owner == "" {
		m.Owner = m.Sharing.Owner
	}
	m.Can = Can(f.Capabilities)
	m.BoundaryNote = BoundaryNote(f.MimeType)
	if f.LabelInfo != nil {
		m.Labels = NewAppliedLabels(f.LabelInfo.Labels, o.LabelDefinitions)
	}
	m.ContentLocked, m.ContentLockedReason = ContentLocked(f)
	return m
}

// ContentLocked reports whether Drive has restricted the file's content,
// and why it says it did.
//
// One definition, because three places were walking this slice: the card
// built here, the note a started approval writes, and the fake. What
// "locked" means has to be the same in all of them — a card that says a
// file is editable beside a note that says it is not is the confusion
// the approval work spent a live run on.
func ContentLocked(f *gdrive.File) (locked bool, reason string) {
	if f == nil {
		return false, ""
	}
	for _, r := range f.ContentRestrictions {
		if r != nil && r.ReadOnly {
			locked, reason = true, r.Reason
		}
	}
	return locked, reason
}

// userWords names a person the way a result should: their display name,
// with the address when it adds something.
func userWords(u *gdrive.User) string {
	if u == nil {
		return ""
	}
	switch {
	case u.Me:
		if u.DisplayName != "" {
			return u.DisplayName + " (you)"
		}
		return "you"
	case u.DisplayName != "" && u.EmailAddress != "":
		return u.DisplayName + " <" + u.EmailAddress + ">"
	case u.DisplayName != "":
		return u.DisplayName
	}
	return u.EmailAddress
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// HumanSize renders a byte count the way a file manager does.
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	value := float64(n) / float64(div)
	format := "%.1f %ciB"
	if value >= 100 {
		format = "%.0f %ciB"
	}
	return fmt.Sprintf(format, value, "KMGTP"[exp])
}

// HumanTime renders an instant as a date plus how long ago it was, so
// "modified 2026-03-04 09:00Z (2 days ago)" reads without arithmetic.
func HumanTime(t, now time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	stamp := t.UTC().Format("2006-01-02 15:04Z")
	if now.IsZero() {
		return stamp
	}
	return stamp + " (" + Ago(t, now) + ")"
}

// Ago renders the distance between two instants in words.
func Ago(t, now time.Time) string {
	d := now.Sub(t)
	future := false
	if d < 0 {
		d, future = -d, true
	}
	var out string
	switch {
	case d < time.Minute:
		out = "just now"
	case d < time.Hour:
		out = Plural(int(d/time.Minute), "minute", "minutes") + " ago"
	case d < 24*time.Hour:
		out = Plural(int(d/time.Hour), "hour", "hours") + " ago"
	case d < 30*24*time.Hour:
		out = Plural(int(d/(24*time.Hour)), "day", "days") + " ago"
	case d < 365*24*time.Hour:
		out = Plural(int(d/(30*24*time.Hour)), "month", "months") + " ago"
	default:
		out = Plural(int(d/(365*24*time.Hour)), "year", "years") + " ago"
	}
	if future && out != "just now" {
		return "in " + strings.TrimSuffix(out, " ago")
	}
	return out
}

// CanShare reports whether this account may change who can see a file.
//
// Absent capabilities mean yes: Drive omits the field when it was not
// asked for, and refusing on a field that was never requested would fail
// a call Drive would have allowed. The polarity is written once here
// because every sharing gate and every hint about one has to agree —
// three call sites had it by hand, and a fourth was added by remembering
// to.
func CanShare(f *gdrive.File) bool {
	return f == nil || f.Capabilities == nil || f.Capabilities.CanShare
}

// CanComment reports whether this account may comment on a file, on the
// same reading of an absent capability.
func CanComment(f *gdrive.File) bool {
	return f == nil || f.Capabilities == nil || f.Capabilities.CanComment
}
