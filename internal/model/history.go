package model

import (
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// Revision is the server's view of one version of a file's content.
type Revision struct {
	ID       string
	Modified time.Time
	// By is who made it, in plain words.
	By       string
	Size     int64
	HasSize  bool
	MD5      string
	MimeType string
	// KeepForever pins the revision so Drive does not discard it. Blob
	// revisions go after thirty days without it.
	KeepForever bool
	// Head marks the revision the file's content is at now, which is the
	// one Drive will not let anybody delete.
	Head bool
	// OriginalFilename is the name the bytes were uploaded under, which
	// can differ from the file's name today.
	OriginalFilename string
}

// NewRevision builds the model view of a revision. headID is the file's
// current head, so the list can mark it: Drive does not say which entry
// it is, and deleting it is the one thing that is always refused.
func NewRevision(r *gdrive.Revision, headID string) *Revision {
	if r == nil {
		return nil
	}
	out := &Revision{
		ID: r.ID, Modified: parseTime(r.ModifiedTime), By: userWords(r.LastModifyingUser),
		MD5: r.MD5Checksum, MimeType: r.MimeType, KeepForever: r.KeepForever,
		Head: headID != "" && r.ID == headID, OriginalFilename: r.OriginalFilename,
	}
	if r.Size != "" {
		f := &gdrive.File{Size: r.Size}
		out.Size, out.HasSize = f.SizeBytes()
	}
	return out
}

// Change is the server's view of one entry in the changes feed.
type Change struct {
	// Kind is "file" or "drive": Drive's own changeType.
	Kind string
	At   time.Time
	// Removed means the item left this account's view — deleted,
	// permanently, or unshared. It is not the same as trashed, and the
	// two must not read the same way.
	Removed bool
	// ID is the file or drive the change is about, which is all a removal
	// carries: there is nothing left to describe.
	ID string
	// Name is the item's name when the change still had one to give.
	Name string
	// ItemKind is the file's kind in plain words, empty for a drive.
	ItemKind string
	// Location is where the file sits now, when it could be worked out.
	Location string
	// Trashed marks a file that is in the trash, which is a change worth
	// telling apart from a removal because it can be undone.
	Trashed bool
}

// NewChange builds the model view of one change. location is worked out
// by the service, which is the only layer that can climb a parent chain.
func NewChange(c *gdrive.Change, location string) *Change {
	if c == nil {
		return nil
	}
	out := &Change{Kind: c.ChangeType, At: parseTime(c.Time), Removed: c.Removed, ID: c.FileID}
	if out.Kind == "" {
		out.Kind = "file"
	}
	if c.DriveID != "" && c.FileID == "" {
		out.ID = c.DriveID
	}
	switch {
	case c.File != nil:
		out.Name = c.File.Name
		out.ItemKind = Kind(c.File)
		out.Trashed = c.File.Trashed
		out.Location = location
	case c.Drive != nil:
		out.Name = c.Drive.Name
	}
	return out
}
