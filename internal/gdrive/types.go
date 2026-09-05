// Package gdrive holds the Drive API v3 wire types this server reads and
// writes. They are hand-written rather than generated: the generated
// client drags in gRPC, OpenTelemetry and the cloud auth stack for a
// binary that only needs JSON and streaming HTTP. The package has no
// dependencies and no behaviour beyond a few accessors, so it can be
// compared against the API reference field by field.
//
// Field names match the API exactly. Optional booleans that Drive omits
// when false are plain bools; a pointer would only matter for a patch,
// and patches are built from explicit request structs.
package gdrive

import (
	"strconv"
	"strings"
)

// MIME types Drive gives its own kinds of file.
const (
	MimeFolder   = "application/vnd.google-apps.folder"
	MimeShortcut = "application/vnd.google-apps.shortcut"
	MimeDocument = "application/vnd.google-apps.document"
	MimeSheet    = "application/vnd.google-apps.spreadsheet"
	MimeSlides   = "application/vnd.google-apps.presentation"
	MimeForm     = "application/vnd.google-apps.form"
	MimeDrawing  = "application/vnd.google-apps.drawing"
	MimeScript   = "application/vnd.google-apps.script"
	MimeSite     = "application/vnd.google-apps.site"
	MimeMap      = "application/vnd.google-apps.map"
	MimeVid      = "application/vnd.google-apps.vid"
	MimeJam      = "application/vnd.google-apps.jam"
)

// User is a Drive user reference.
type User struct {
	DisplayName  string `json:"displayName,omitempty"`
	EmailAddress string `json:"emailAddress,omitempty"`
	PhotoLink    string `json:"photoLink,omitempty"`
	Me           bool   `json:"me,omitempty"`
	PermissionID string `json:"permissionId,omitempty"`
}

// Capabilities are the signed-in person's rights on one file, as Drive
// computes them. The sharing guide asks apps to consult these rather
// than guess from a role, and every gate in this server does.
type Capabilities struct {
	CanEdit                             bool `json:"canEdit,omitempty"`
	CanComment                          bool `json:"canComment,omitempty"`
	CanShare                            bool `json:"canShare,omitempty"`
	CanCopy                             bool `json:"canCopy,omitempty"`
	CanDownload                         bool `json:"canDownload,omitempty"`
	CanRename                           bool `json:"canRename,omitempty"`
	CanTrash                            bool `json:"canTrash,omitempty"`
	CanUntrash                          bool `json:"canUntrash,omitempty"`
	CanDelete                           bool `json:"canDelete,omitempty"`
	CanListChildren                     bool `json:"canListChildren,omitempty"`
	CanAddChildren                      bool `json:"canAddChildren,omitempty"`
	CanRemoveChildren                   bool `json:"canRemoveChildren,omitempty"`
	CanTrashChildren                    bool `json:"canTrashChildren,omitempty"`
	CanDeleteChildren                   bool `json:"canDeleteChildren,omitempty"`
	CanModifyContent                    bool `json:"canModifyContent,omitempty"`
	CanReadRevisions                    bool `json:"canReadRevisions,omitempty"`
	CanMoveItemWithinDrive              bool `json:"canMoveItemWithinDrive,omitempty"`
	CanMoveItemOutOfDrive               bool `json:"canMoveItemOutOfDrive,omitempty"`
	CanChangeCopyRequiresWriterPerm     bool `json:"canChangeCopyRequiresWriterPermission,omitempty"`
	CanModifyLabels                     bool `json:"canModifyLabels,omitempty"`
	CanReadLabels                       bool `json:"canReadLabels,omitempty"`
	CanChangeSecurityUpdateEnabled      bool `json:"canChangeSecurityUpdateEnabled,omitempty"`
	CanAcceptOwnership                  bool `json:"canAcceptOwnership,omitempty"`
	CanReadDrive                        bool `json:"canReadDrive,omitempty"`
	CanChangeItemDownloadRestriction    bool `json:"canChangeItemDownloadRestriction,omitempty"`
	CanDisableInheritedPermissions      bool `json:"canDisableInheritedPermissions,omitempty"`
	CanEnableInheritedPermissions       bool `json:"canEnableInheritedPermissions,omitempty"`
	CanShareChildFiles                  bool `json:"canShareChildFiles,omitempty"`
	CanShareChildFolders                bool `json:"canShareChildFolders,omitempty"`
	CanChangeSharingFolderRestrictedFor bool `json:"canChangeSharingFolderRestrictedForWriters,omitempty"`
}

// ShortcutDetails points a shortcut at its target. The target resource
// key matters for a link-shared target under the 2021 security update.
type ShortcutDetails struct {
	TargetID          string `json:"targetId,omitempty"`
	TargetMimeType    string `json:"targetMimeType,omitempty"`
	TargetResourceKey string `json:"targetResourceKey,omitempty"`
}

// LinkShareMetadata says whether the file is reachable by link and
// whether it needs a resource key to open.
type LinkShareMetadata struct {
	SecurityUpdateEligible bool `json:"securityUpdateEligible,omitempty"`
	SecurityUpdateEnabled  bool `json:"securityUpdateEnabled,omitempty"`
}

// ContentRestriction records a lock placed on a file's content.
type ContentRestriction struct {
	ReadOnly        bool   `json:"readOnly,omitempty"`
	Reason          string `json:"reason,omitempty"`
	RestrictingUser *User  `json:"restrictingUser,omitempty"`
	RestrictionTime string `json:"restrictionTime,omitempty"`
	Type            string `json:"type,omitempty"`
}

// Label is one Workspace label applied to a file, as the Drive API
// reports it. Definitions live in the separate Drive Labels API.
type Label struct {
	ID         string                `json:"id,omitempty"`
	RevisionID string                `json:"revisionId,omitempty"`
	Fields     map[string]LabelField `json:"fields,omitempty"`
}

// LabelField is one field of an applied label.
type LabelField struct {
	ID        string   `json:"id,omitempty"`
	ValueType string   `json:"valueType,omitempty"`
	Text      []string `json:"text,omitempty"`
	Selection []string `json:"selection,omitempty"`
	Integer   []string `json:"integer,omitempty"`
	Date      []string `json:"date,omitempty"`
	User      []*User  `json:"user,omitempty"`
}

// LabelInfo carries the labels files.get returns with includeLabels.
type LabelInfo struct {
	Labels []*Label `json:"labels,omitempty"`
}

// File is a Drive file, folder or shortcut. Drive returns only the
// fields a request asked for, so a zero field means "not requested" as
// often as it means "not set"; callers ask for a fixed field list.
type File struct {
	ID                string   `json:"id,omitempty"`
	Name              string   `json:"name,omitempty"`
	MimeType          string   `json:"mimeType,omitempty"`
	Description       string   `json:"description,omitempty"`
	Parents           []string `json:"parents,omitempty"`
	Starred           bool     `json:"starred,omitempty"`
	Trashed           bool     `json:"trashed,omitempty"`
	ExplicitlyTrashed bool     `json:"explicitlyTrashed,omitempty"`
	// TrashedTime and TrashingUser exist only in shared drives.
	TrashedTime  string `json:"trashedTime,omitempty"`
	TrashingUser *User  `json:"trashingUser,omitempty"`

	CreatedTime      string `json:"createdTime,omitempty"`
	ModifiedTime     string `json:"modifiedTime,omitempty"`
	ModifiedByMeTime string `json:"modifiedByMeTime,omitempty"`
	ViewedByMeTime   string `json:"viewedByMeTime,omitempty"`
	SharedWithMeTime string `json:"sharedWithMeTime,omitempty"`

	Owners            []*User `json:"owners,omitempty"`
	LastModifyingUser *User   `json:"lastModifyingUser,omitempty"`
	SharingUser       *User   `json:"sharingUser,omitempty"`
	OwnedByMe         bool    `json:"ownedByMe,omitempty"`
	Shared            bool    `json:"shared,omitempty"`

	WebViewLink    string `json:"webViewLink,omitempty"`
	WebContentLink string `json:"webContentLink,omitempty"`
	IconLink       string `json:"iconLink,omitempty"`

	// Size is a string in the wire form: Drive sends int64 as JSON text.
	Size           string `json:"size,omitempty"`
	QuotaBytesUsed string `json:"quotaBytesUsed,omitempty"`
	MD5Checksum    string `json:"md5Checksum,omitempty"`
	SHA256Checksum string `json:"sha256Checksum,omitempty"`
	HeadRevisionID string `json:"headRevisionId,omitempty"`
	Version        string `json:"version,omitempty"`

	FileExtension     string `json:"fileExtension,omitempty"`
	FullFileExtension string `json:"fullFileExtension,omitempty"`
	OriginalFilename  string `json:"originalFilename,omitempty"`
	FolderColorRgb    string `json:"folderColorRgb,omitempty"`

	DriveID     string `json:"driveId,omitempty"`
	ResourceKey string `json:"resourceKey,omitempty"`

	WritersCanShare              bool `json:"writersCanShare,omitempty"`
	CopyRequiresWriterPermission bool `json:"copyRequiresWriterPermission,omitempty"`
	HasThumbnail                 bool `json:"hasThumbnail,omitempty"`

	Capabilities    *Capabilities      `json:"capabilities,omitempty"`
	ShortcutDetails *ShortcutDetails   `json:"shortcutDetails,omitempty"`
	LinkShare       *LinkShareMetadata `json:"linkShareMetadata,omitempty"`
	LabelInfo       *LabelInfo         `json:"labelInfo,omitempty"`

	Permissions   []*Permission     `json:"permissions,omitempty"`
	PermissionIDs []string          `json:"permissionIds,omitempty"`
	Properties    map[string]string `json:"properties,omitempty"`
	AppProperties map[string]string `json:"appProperties,omitempty"`
	ExportLinks   map[string]string `json:"exportLinks,omitempty"`

	ContentRestrictions []*ContentRestriction `json:"contentRestrictions,omitempty"`
}

// IsFolder reports whether the file is a folder.
func (f *File) IsFolder() bool { return f != nil && f.MimeType == MimeFolder }

// IsShortcut reports whether the file is a shortcut to another item.
func (f *File) IsShortcut() bool { return f != nil && f.MimeType == MimeShortcut }

// googleMimePrefix marks the types Drive owns rather than stores: they
// have no bytes of their own, and several of the API's rules apply to
// them and to nothing else.
const googleMimePrefix = "application/vnd.google-apps."

// IsGoogleMime reports whether a media type is one of Drive's own.
func IsGoogleMime(mime string) bool {
	return strings.HasPrefix(strings.TrimSpace(mime), googleMimePrefix)
}

// IsWorkspaceDoc reports whether the file is a Google-native document
// whose bytes exist only through export. A folder and a shortcut are
// Google types too, but neither is a document.
func (f *File) IsWorkspaceDoc() bool {
	return f != nil && IsGoogleMime(f.MimeType) &&
		f.MimeType != MimeFolder && f.MimeType != MimeShortcut
}

// Parent returns the file's single parent, or "" when it has none. Drive
// has allowed exactly one parent since 2020.
func (f *File) Parent() string {
	if f == nil || len(f.Parents) == 0 {
		return ""
	}
	return f.Parents[0]
}

// FileList is one page of files.list.
type FileList struct {
	Files            []*File `json:"files"`
	NextPageToken    string  `json:"nextPageToken,omitempty"`
	IncompleteSearch bool    `json:"incompleteSearch,omitempty"`
	Kind             string  `json:"kind,omitempty"`
}

// PermissionDetails explains where a shared-drive permission comes from.
// An inherited grant can only be removed at its source.
type PermissionDetails struct {
	PermissionType string `json:"permissionType,omitempty"`
	Role           string `json:"role,omitempty"`
	InheritedFrom  string `json:"inheritedFrom,omitempty"`
	Inherited      bool   `json:"inherited,omitempty"`
}

// Permission is one grant on a file or shared drive. Drive allows one
// permission per principal, so granting again updates the existing one.
type Permission struct {
	ID                 string               `json:"id,omitempty"`
	Type               string               `json:"type,omitempty"`
	Role               string               `json:"role,omitempty"`
	EmailAddress       string               `json:"emailAddress,omitempty"`
	Domain             string               `json:"domain,omitempty"`
	DisplayName        string               `json:"displayName,omitempty"`
	AllowFileDiscovery bool                 `json:"allowFileDiscovery,omitempty"`
	ExpirationTime     string               `json:"expirationTime,omitempty"`
	Deleted            bool                 `json:"deleted,omitempty"`
	PendingOwner       bool                 `json:"pendingOwner,omitempty"`
	Details            []*PermissionDetails `json:"permissionDetails,omitempty"`
}

// Inherited reports whether the grant comes from a shared-drive ancestor.
func (p *Permission) Inherited() (bool, string) {
	if p == nil {
		return false, ""
	}
	for _, d := range p.Details {
		if d.Inherited {
			return true, d.InheritedFrom
		}
	}
	return false, ""
}

// PermissionList is one page of permissions.list.
type PermissionList struct {
	Permissions   []*Permission `json:"permissions"`
	NextPageToken string        `json:"nextPageToken,omitempty"`
}

// DriveCapabilities are the signed-in person's rights on a shared drive.
type DriveCapabilities struct {
	CanAddChildren              bool `json:"canAddChildren,omitempty"`
	CanChangeDriveBackground    bool `json:"canChangeDriveBackground,omitempty"`
	CanChangeDriveMembersOnly   bool `json:"canChangeDriveMembersOnlyRestriction,omitempty"`
	CanChangeDomainUsersOnly    bool `json:"canChangeDomainUsersOnlyRestriction,omitempty"`
	CanChangeCopyRequiresWriter bool `json:"canChangeCopyRequiresWriterPermissionRestriction,omitempty"`
	CanComment                  bool `json:"canComment,omitempty"`
	CanCopy                     bool `json:"canCopy,omitempty"`
	CanDeleteDrive              bool `json:"canDeleteDrive,omitempty"`
	CanDownload                 bool `json:"canDownload,omitempty"`
	CanEdit                     bool `json:"canEdit,omitempty"`
	CanListChildren             bool `json:"canListChildren,omitempty"`
	CanManageMembers            bool `json:"canManageMembers,omitempty"`
	CanReadRevisions            bool `json:"canReadRevisions,omitempty"`
	CanRename                   bool `json:"canRename,omitempty"`
	CanRenameDrive              bool `json:"canRenameDrive,omitempty"`
	CanShare                    bool `json:"canShare,omitempty"`
	CanTrashChildren            bool `json:"canTrashChildren,omitempty"`
}

// DriveRestrictions are the four switches a shared drive carries.
type DriveRestrictions struct {
	AdminManagedRestrictions                  bool `json:"adminManagedRestrictions,omitempty"`
	CopyRequiresWriterPermission              bool `json:"copyRequiresWriterPermission,omitempty"`
	DomainUsersOnly                           bool `json:"domainUsersOnly,omitempty"`
	DriveMembersOnly                          bool `json:"driveMembersOnly,omitempty"`
	SharingFoldersRequiresOrganizerPermission bool `json:"sharingFoldersRequiresOrganizerPermission,omitempty"`
}

// Drive is a shared drive. Its root folder id is the drive id.
type Drive struct {
	ID           string             `json:"id,omitempty"`
	Name         string             `json:"name,omitempty"`
	CreatedTime  string             `json:"createdTime,omitempty"`
	Hidden       bool               `json:"hidden,omitempty"`
	ColorRgb     string             `json:"colorRgb,omitempty"`
	Capabilities *DriveCapabilities `json:"capabilities,omitempty"`
	Restrictions *DriveRestrictions `json:"restrictions,omitempty"`
	OrgUnitID    string             `json:"orgUnitId,omitempty"`
}

// DriveList is one page of drives.list.
type DriveList struct {
	Drives        []*Drive `json:"drives"`
	NextPageToken string   `json:"nextPageToken,omitempty"`
}

// StorageQuota is the account's storage as about.get reports it. Limit
// is absent on accounts with unlimited storage.
type StorageQuota struct {
	Limit             string `json:"limit,omitempty"`
	Usage             string `json:"usage,omitempty"`
	UsageInDrive      string `json:"usageInDrive,omitempty"`
	UsageInDriveTrash string `json:"usageInDriveTrash,omitempty"`
}

// About is the about.get response. CanCreateDrives is false on consumer
// accounts, which have no shared drives at all.
type About struct {
	User            *User               `json:"user,omitempty"`
	StorageQuota    *StorageQuota       `json:"storageQuota,omitempty"`
	CanCreateDrives bool                `json:"canCreateDrives,omitempty"`
	MaxUploadSize   string              `json:"maxUploadSize,omitempty"`
	ImportFormats   map[string][]string `json:"importFormats,omitempty"`
	ExportFormats   map[string][]string `json:"exportFormats,omitempty"`
	MaxImportSizes  map[string]string   `json:"maxImportSizes,omitempty"`
}

// GeneratedIDs is the files.generateIds response. Ids from it go in the
// body of create and copy, which makes those calls idempotent: a retry
// after an ambiguous failure cannot produce a second file.
type GeneratedIDs struct {
	IDs   []string `json:"ids"`
	Space string   `json:"space,omitempty"`
}

// FileMeta is the metadata body of files.create, files.update and
// files.copy. It is a separate type from File because a patch means
// "change exactly the fields present": every field Drive treats as
// optional is a pointer, so that clearing a description (an empty
// string) and leaving it alone (absent) are different requests. File
// itself is a response type, where that distinction cannot be made.
type FileMeta struct {
	// ID is a pre-generated id from files.generateIds, which makes a
	// create or a copy idempotent. Ignored by files.update.
	ID       string   `json:"id,omitempty"`
	Name     string   `json:"name,omitempty"`
	MimeType string   `json:"mimeType,omitempty"`
	Parents  []string `json:"parents,omitempty"`

	Description    *string `json:"description,omitempty"`
	Starred        *bool   `json:"starred,omitempty"`
	Trashed        *bool   `json:"trashed,omitempty"`
	FolderColorRgb *string `json:"folderColorRgb,omitempty"`

	WritersCanShare              *bool `json:"writersCanShare,omitempty"`
	CopyRequiresWriterPermission *bool `json:"copyRequiresWriterPermission,omitempty"`

	// Properties are public custom properties. A nil value deletes the
	// key, which is what the reference means by "entries with null values
	// are cleared in update and copy requests".
	Properties map[string]*string `json:"properties,omitempty"`

	// ShortcutDetails carries the target of a shortcut being created.
	ShortcutDetails *ShortcutDetails `json:"shortcutDetails,omitempty"`
}

// Revision is one version of a file's content. Drive keeps blob
// revisions for 30 days unless KeepForever pins them; Docs editors files
// keep their own history and expose it through ExportLinks.
type Revision struct {
	ID                string            `json:"id,omitempty"`
	MimeType          string            `json:"mimeType,omitempty"`
	ModifiedTime      string            `json:"modifiedTime,omitempty"`
	KeepForever       bool              `json:"keepForever,omitempty"`
	Published         bool              `json:"published,omitempty"`
	LastModifyingUser *User             `json:"lastModifyingUser,omitempty"`
	OriginalFilename  string            `json:"originalFilename,omitempty"`
	MD5Checksum       string            `json:"md5Checksum,omitempty"`
	Size              string            `json:"size,omitempty"`
	ExportLinks       map[string]string `json:"exportLinks,omitempty"`
}

// RevisionList is one page of revisions.list.
type RevisionList struct {
	Revisions     []*Revision `json:"revisions"`
	NextPageToken string      `json:"nextPageToken,omitempty"`
}

// String returns a pointer to s, for the optional fields of FileMeta.
func String(s string) *string { return &s }

// Bool returns a pointer to b, for the optional fields of FileMeta.
func Bool(b bool) *bool { return &b }

// SizeBytes reads the byte count Drive sends as JSON text, and reports
// whether the field was there at all. A folder, a shortcut and a
// Google-native document have no size, and zero is a different answer
// from "no size": every caller that renders one needs to tell them apart.
func (f *File) SizeBytes() (int64, bool) {
	if f == nil || f.Size == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(f.Size, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// MimeOnly drops the parameters from a media type, so that
// "text/plain; charset=UTF-8" compares equal to "text/plain". It lives
// here because every layer that reads a Content-Type needs it and this
// package is the one they all already import.
func MimeOnly(v string) string {
	if i := strings.IndexByte(v, ';'); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}
