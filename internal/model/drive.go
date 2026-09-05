package model

import (
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// Drive is the server's view of one shared drive.
type Drive struct {
	ID   string
	Name string
	// Hidden means it is out of the sidebar's default view. It is a
	// display choice, not access: a hidden drive is as reachable as any
	// other.
	Hidden bool
	// Can is what the signed-in person may do here, in plain words.
	Can []string
	// Restrictions are the switches the drive carries, in plain words.
	// Empty means none of them is on.
	Restrictions []string
	// Created is the RFC 3339 time Drive reported, kept as text because
	// nothing here does arithmetic on it.
	Created string
}

// driveCapabilityWords maps a shared drive's capabilities to the verb
// this server uses for them, in the order a listing shows them.
//
// Drive returns no role on the drive resource, so this is what a
// listing can honestly say: the capabilities are Google's own answer to
// "what may this person do", and naming a role from them would be a
// guess dressed as a fact. list_permissions on the drive gives the
// membership, this account's own grant included, when the role itself is
// what is wanted.
var driveCapabilityWords = []struct {
	name string
	get  func(*gdrive.DriveCapabilities) bool
}{
	{"manage members", func(c *gdrive.DriveCapabilities) bool { return c.CanManageMembers }},
	{"rename the drive", func(c *gdrive.DriveCapabilities) bool { return c.CanRenameDrive }},
	{"delete the drive", func(c *gdrive.DriveCapabilities) bool { return c.CanDeleteDrive }},
	{"change restrictions", func(c *gdrive.DriveCapabilities) bool {
		return c.CanChangeDriveMembersOnly || c.CanChangeDomainUsersOnly || c.CanChangeCopyRequiresWriter
	}},
	{"add items", func(c *gdrive.DriveCapabilities) bool { return c.CanAddChildren }},
	{"edit", func(c *gdrive.DriveCapabilities) bool { return c.CanEdit }},
	{"comment", func(c *gdrive.DriveCapabilities) bool { return c.CanComment }},
	{"copy", func(c *gdrive.DriveCapabilities) bool { return c.CanCopy }},
	{"download", func(c *gdrive.DriveCapabilities) bool { return c.CanDownload }},
	{"share", func(c *gdrive.DriveCapabilities) bool { return c.CanShare }},
	{"list items", func(c *gdrive.DriveCapabilities) bool { return c.CanListChildren }},
	{"trash items", func(c *gdrive.DriveCapabilities) bool { return c.CanTrashChildren }},
	{"read version history", func(c *gdrive.DriveCapabilities) bool { return c.CanReadRevisions }},
}

// driveRestrictionWords say what each restriction actually stops, since
// the field names do not.
var driveRestrictionWords = []struct {
	name string
	get  func(*gdrive.DriveRestrictions) bool
}{
	{"only members of this drive can open items in it",
		func(r *gdrive.DriveRestrictions) bool { return r.DriveMembersOnly }},
	{"only people in the organisation can be given access",
		func(r *gdrive.DriveRestrictions) bool { return r.DomainUsersOnly }},
	{"readers and commenters cannot copy, print or download",
		func(r *gdrive.DriveRestrictions) bool { return r.CopyRequiresWriterPermission }},
	{"only an administrator can change these restrictions",
		func(r *gdrive.DriveRestrictions) bool { return r.AdminManagedRestrictions }},
	{"only organizers can share folders",
		func(r *gdrive.DriveRestrictions) bool { return r.SharingFoldersRequiresOrganizerPermission }},
}

// NewDrive builds the model view of a shared drive.
func NewDrive(d *gdrive.Drive) *Drive {
	if d == nil {
		return nil
	}
	out := &Drive{ID: d.ID, Name: d.Name, Hidden: d.Hidden, Created: d.CreatedTime}
	if c := d.Capabilities; c != nil {
		for _, w := range driveCapabilityWords {
			if w.get(c) {
				out.Can = append(out.Can, w.name)
			}
		}
	}
	if r := d.Restrictions; r != nil {
		for _, w := range driveRestrictionWords {
			if w.get(r) {
				out.Restrictions = append(out.Restrictions, w.name)
			}
		}
	}
	return out
}

// DriveRestrictionNames are the switches manage_drive accepts, in the
// order the tool description lists them. The names a caller passes and
// the fields they set are declared together so neither can drift.
var DriveRestrictionNames = []string{
	"members_only", "domain_users_only", "copy_requires_writer_permission",
	"admin_managed", "folder_sharing_requires_organizer",
}

// SetDriveRestriction turns one of DriveRestrictionNames on or off in a
// restrictions body, and reports whether the name was known. Building
// the patch here keeps the accepted words and the wire fields in one
// place; a second copy in the service is how a typo becomes a switch
// that silently never changes.
func SetDriveRestriction(r *gdrive.DriveRestrictions, name string, on bool) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "members_only":
		r.DriveMembersOnly = on
	case "domain_users_only":
		r.DomainUsersOnly = on
	case "copy_requires_writer_permission":
		r.CopyRequiresWriterPermission = on
	case "admin_managed":
		r.AdminManagedRestrictions = on
	case "folder_sharing_requires_organizer":
		r.SharingFoldersRequiresOrganizerPermission = on
	default:
		return false
	}
	return true
}

// DriveRestriction reads one restriction by the name a caller passes.
func DriveRestriction(r *gdrive.DriveRestrictions, name string) bool {
	if r == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "members_only":
		return r.DriveMembersOnly
	case "domain_users_only":
		return r.DomainUsersOnly
	case "copy_requires_writer_permission":
		return r.CopyRequiresWriterPermission
	case "admin_managed":
		return r.AdminManagedRestrictions
	case "folder_sharing_requires_organizer":
		return r.SharingFoldersRequiresOrganizerPermission
	}
	return false
}
