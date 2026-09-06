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

// LabelField is one field of an applied label. Drive names the date
// member `dateString`, not `date`: it is an RFC 3339 calendar date with
// no time, and the name says so. Phase 4 read the discovery document and
// found this tag saying `date`, which decoded every date-valued field to
// nothing at all — silently, because a missing member is indistinguishable
// from an unset one.
type LabelField struct {
	ID        string   `json:"id,omitempty"`
	ValueType string   `json:"valueType,omitempty"`
	Text      []string `json:"text,omitempty"`
	Selection []string `json:"selection,omitempty"`
	Integer   []string `json:"integer,omitempty"`
	Date      []string `json:"dateString,omitempty"`
	User      []*User  `json:"user,omitempty"`
}

// LabelInfo carries the labels a files.get returns. It is populated by
// the includeLabels parameter, which takes a comma-separated list of
// label ids and nothing else — see LabelList for the way to ask for all
// of them.
type LabelInfo struct {
	Labels []*Label `json:"labels,omitempty"`
}

// LabelList is one page of files.listLabels: the labels applied to a
// file, without having to know their ids in advance.
type LabelList struct {
	Labels        []*Label `json:"labels,omitempty"`
	NextPageToken string   `json:"nextPageToken,omitempty"`
}

// ModifyLabelsRequest applies, changes or removes labels on a file. The
// reference is explicit that the modifications either all succeed or all
// fail, so a partial application is not a state this server has to
// describe.
type ModifyLabelsRequest struct {
	LabelModifications []LabelModification `json:"labelModifications,omitempty"`
}

// LabelModification is one label's worth of change. RemoveLabel and
// FieldModifications are alternatives: removing a label takes its fields
// with it.
type LabelModification struct {
	LabelID            string                   `json:"labelId,omitempty"`
	RemoveLabel        bool                     `json:"removeLabel,omitempty"`
	FieldModifications []LabelFieldModification `json:"fieldModifications,omitempty"`
}

// LabelFieldModification sets or unsets one field. Every setter replaces
// the field's values rather than adding to them, which is the reference's
// wording and the reason this server's tool speaks of setting a field
// rather than adding to it.
type LabelFieldModification struct {
	FieldID            string   `json:"fieldId,omitempty"`
	SetTextValues      []string `json:"setTextValues,omitempty"`
	SetSelectionValues []string `json:"setSelectionValues,omitempty"`
	SetIntegerValues   []string `json:"setIntegerValues,omitempty"`
	SetDateValues      []string `json:"setDateValues,omitempty"`
	SetUserValues      []string `json:"setUserValues,omitempty"`
	UnsetValues        bool     `json:"unsetValues,omitempty"`
}

// ModifyLabelsResponse carries only the labels the request added or
// changed, so a removal comes back as an empty list rather than as
// evidence of itself.
type ModifyLabelsResponse struct {
	ModifiedLabels []*Label `json:"modifiedLabels,omitempty"`
}

// LabelDefinition is a label as the separate Drive Labels API defines
// it, which is where a field's id, type and permitted values live. The
// Drive API only ever reports the values applied to a file.
//
// This is a subset: the definition carries creator, publisher, display
// hints, lock status and per-revision permissions besides, none of which
// help a model decide what it may set on a file.
type LabelDefinition struct {
	// Name is `labels/{id}` or `labels/{id}@{revision}`, depending on
	// whether the request asked for published revisions only.
	Name       string `json:"name,omitempty"`
	ID         string `json:"id,omitempty"`
	RevisionID string `json:"revisionId,omitempty"`
	// LabelType is ADMIN or SHARED: who may change the definition.
	LabelType           string                     `json:"labelType,omitempty"`
	Properties          *LabelDefinitionProperties `json:"properties,omitempty"`
	Lifecycle           *LabelLifecycle            `json:"lifecycle,omitempty"`
	Fields              []*LabelFieldDefinition    `json:"fields,omitempty"`
	AppliedCapabilities *LabelAppliedCapabilities  `json:"appliedCapabilities,omitempty"`
}

// LabelDefinitionProperties is the label's own title and description.
type LabelDefinitionProperties struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// LabelLifecycle says whether a definition is published, and so whether
// it can be applied at all.
type LabelLifecycle struct {
	State                 string `json:"state,omitempty"`
	HasUnpublishedChanges bool   `json:"hasUnpublishedChanges,omitempty"`
}

// LabelAppliedCapabilities is what this user may do with the label on a
// file, as distinct from what they may do to the definition.
type LabelAppliedCapabilities struct {
	CanRead   bool `json:"canRead,omitempty"`
	CanApply  bool `json:"canApply,omitempty"`
	CanRemove bool `json:"canRemove,omitempty"`
}

// LabelFieldDefinition is one field of a definition. The value type is
// not a member: the API says which type a field is by which options
// object is present, so ValueType below is derived rather than decoded.
type LabelFieldDefinition struct {
	ID string `json:"id,omitempty"`
	// QueryKey is the term a Drive search uses to find files by this
	// field's value.
	QueryKey            string                         `json:"queryKey,omitempty"`
	Properties          *LabelFieldProperties          `json:"properties,omitempty"`
	Lifecycle           *LabelLifecycle                `json:"lifecycle,omitempty"`
	AppliedCapabilities *LabelFieldAppliedCapabilities `json:"appliedCapabilities,omitempty"`
	TextOptions         *struct{}                      `json:"textOptions,omitempty"`
	IntegerOptions      *struct{}                      `json:"integerOptions,omitempty"`
	DateOptions         *LabelDateOptions              `json:"dateOptions,omitempty"`
	SelectionOptions    *LabelSelectionOptions         `json:"selectionOptions,omitempty"`
	UserOptions         *struct{}                      `json:"userOptions,omitempty"`
}

// LabelFieldProperties is a field's display name and whether it is
// required.
type LabelFieldProperties struct {
	DisplayName string `json:"displayName,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// LabelFieldAppliedCapabilities is what this user may do with the field's
// value on a file.
type LabelFieldAppliedCapabilities struct {
	CanRead   bool `json:"canRead,omitempty"`
	CanWrite  bool `json:"canWrite,omitempty"`
	CanSearch bool `json:"canSearch,omitempty"`
}

// LabelDateOptions says how a date field is displayed. The format is the
// only part that helps a caller: the value itself is always YYYY-MM-DD.
type LabelDateOptions struct {
	DateFormatType string `json:"dateFormatType,omitempty"`
}

// LabelSelectionOptions carries the choices a selection field permits.
type LabelSelectionOptions struct {
	ListOptions *LabelListOptions `json:"listOptions,omitempty"`
	Choices     []*LabelChoice    `json:"choices,omitempty"`
}

// LabelListOptions says whether a field takes more than one value.
type LabelListOptions struct {
	MaxEntries int `json:"maxEntries,omitempty"`
}

// LabelChoice is one permitted value of a selection field. The id is
// what a modification sets; the display name is what a person reads.
type LabelChoice struct {
	ID         string                 `json:"id,omitempty"`
	Properties *LabelChoiceProperties `json:"properties,omitempty"`
	Lifecycle  *LabelLifecycle        `json:"lifecycle,omitempty"`
}

// LabelChoiceProperties is a choice's display name and description.
type LabelChoiceProperties struct {
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
}

// LabelDefinitionList is one page of the Labels API's labels.list.
type LabelDefinitionList struct {
	Labels        []*LabelDefinition `json:"labels,omitempty"`
	NextPageToken string             `json:"nextPageToken,omitempty"`
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

	// ViewedByMeTime is when the signed-in person last opened the file.
	// It is the one "output only in spirit" field the API lets a caller
	// write: viewedByMe beside it IS output only, so marking a file as
	// seen means stamping this. Drive uses it for the Recent view and for
	// the viewedByMeTime sort.
	ViewedByMeTime string `json:"viewedByMeTime,omitempty"`

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

// PermissionMeta is the body of permissions.create and
// permissions.update. It is separate from Permission for the reason
// FileMeta is separate from File: a patch means "change exactly the
// fields present", and only a pointer can tell "clear the expiry" from
// "leave it alone".
type PermissionMeta struct {
	// Type, Role and the principal are only sent on a create; an update
	// takes Role and the two pointer fields.
	Type         string `json:"type,omitempty"`
	Role         string `json:"role,omitempty"`
	EmailAddress string `json:"emailAddress,omitempty"`
	Domain       string `json:"domain,omitempty"`

	// AllowFileDiscovery decides whether a domain or anyone grant turns
	// up in search rather than only opening by link. Drive's default is
	// false, and so is this server's, but "leave it alone" on an update
	// still has to be a different request from "set it to false".
	AllowFileDiscovery *bool `json:"allowFileDiscovery,omitempty"`
	// ExpirationTime is RFC 3339, and setting it is all this field does.
	// Clearing one goes through permissions.update's own
	// removeExpiration parameter, because Drive does not read an empty
	// string here as "remove it".
	ExpirationTime string `json:"expirationTime,omitempty"`
	// PendingOwner marks a consumer-account transfer waiting to be
	// accepted. Workspace transfers do not use it.
	PendingOwner *bool `json:"pendingOwner,omitempty"`
}

// DriveMeta is the body of drives.create and drives.update.
type DriveMeta struct {
	Name         string             `json:"name,omitempty"`
	ColorRgb     string             `json:"colorRgb,omitempty"`
	Hidden       *bool              `json:"hidden,omitempty"`
	Restrictions *DriveRestrictions `json:"restrictions,omitempty"`
}

// StartPageToken is the changes.getStartPageToken response: the point in
// the changes feed that "from now on" means.
type StartPageToken struct {
	StartPageToken string `json:"startPageToken,omitempty"`
	Kind           string `json:"kind,omitempty"`
}

// Change is one entry in the changes feed. A change names either a file
// or a shared drive, and Removed means the item left this account's
// view — deleted, untrashed out of reach, or unshared — which is not the
// same as trashed.
type Change struct {
	ChangeType string `json:"changeType,omitempty"`
	Time       string `json:"time,omitempty"`
	Removed    bool   `json:"removed,omitempty"`
	FileID     string `json:"fileId,omitempty"`
	DriveID    string `json:"driveId,omitempty"`
	File       *File  `json:"file,omitempty"`
	Drive      *Drive `json:"drive,omitempty"`
}

// ChangeList is one page of changes.list. NewStartPageToken appears only
// on the last page, and it is what the next call should carry.
type ChangeList struct {
	Changes           []*Change `json:"changes"`
	NextPageToken     string    `json:"nextPageToken,omitempty"`
	NewStartPageToken string    `json:"newStartPageToken,omitempty"`
	Kind              string    `json:"kind,omitempty"`
}

// QuotedFileContent is the passage a comment is pinned to. Drive fills
// it for an anchored comment on a file it can quote from; this server
// never sets one, because pinning a comment to a passage of a Google Doc
// is a Docs API feature and this server stops at the file boundary.
type QuotedFileContent struct {
	MimeType string `json:"mimeType,omitempty"`
	Value    string `json:"value,omitempty"`
}

// Reply is one reply in a comment thread. Action is Drive's own word for
// what the reply did to the thread — "resolve" or "reopen" — and a reply
// may carry one instead of any text at all.
//
// The author's email address is deliberately absent: the reference says
// Drive does not populate it on a comment or a reply, so asking for it
// would return a field that is always empty.
type Reply struct {
	ID           string `json:"id,omitempty"`
	CreatedTime  string `json:"createdTime,omitempty"`
	ModifiedTime string `json:"modifiedTime,omitempty"`
	Author       *User  `json:"author,omitempty"`
	Content      string `json:"content,omitempty"`
	Deleted      bool   `json:"deleted,omitempty"`
	Action       string `json:"action,omitempty"`
}

// Comment is one thread on a file, with its replies in chronological
// order. Drive returns the replies inline with the comment, so a listing
// of threads needs no second call per thread.
type Comment struct {
	ID           string `json:"id,omitempty"`
	CreatedTime  string `json:"createdTime,omitempty"`
	ModifiedTime string `json:"modifiedTime,omitempty"`
	Author       *User  `json:"author,omitempty"`
	Content      string `json:"content,omitempty"`
	Deleted      bool   `json:"deleted,omitempty"`
	// Resolved is set by a reply whose action was "resolve"; there is no
	// field to write it directly.
	Resolved bool `json:"resolved,omitempty"`
	// Anchor is an opaque JSON string naming the region of the document
	// the comment sits on. This server reads it to say a comment is
	// pinned; it never writes one.
	Anchor            string             `json:"anchor,omitempty"`
	QuotedFileContent *QuotedFileContent `json:"quotedFileContent,omitempty"`
	Replies           []*Reply           `json:"replies,omitempty"`

	// The reference gives a Comment an assigneeEmailAddress as well, for
	// the action items a Doc's editor makes. It is not here, and not in
	// gapi.CommentFields, because Drive REFUSES it in a field selection:
	// asking for it answers 400 "Invalid field selection
	// assignee_email_address" and the whole call fails. A field that
	// exists and cannot be requested is a field nothing can receive.
}

// CommentList is one page of comments.list.
type CommentList struct {
	Comments      []*Comment `json:"comments"`
	NextPageToken string     `json:"nextPageToken,omitempty"`
}

// There is no ReplyList here. replies.list exists in the API and this
// server never calls it: Drive returns a comment's replies inline with
// the comment, so a listing of threads already has them. A wire type
// nothing decodes is a type that drifts from the API with nothing to
// notice.

// CommentMeta is the body of comments.create and comments.update. Only
// the content is ever sent: resolved is output only — a reply resolves a
// thread — and an anchor belongs to the API that knows where a passage
// is.
type CommentMeta struct {
	Content string `json:"content,omitempty"`
}

// ReplyMeta is the body of replies.create and replies.update. Content is
// required on a create unless Action carries one of Drive's two verbs.
type ReplyMeta struct {
	Content string `json:"content,omitempty"`
	Action  string `json:"action,omitempty"`
}

// Reply actions Drive understands. There are exactly two, and neither
// can be undone by editing the reply's text afterwards.
const (
	ReplyActionResolve = "resolve"
	ReplyActionReopen  = "reopen"
)

// AccessProposalRoleAndView is one role a requester asked for, and the
// view it belongs to. Drive makes it a list, so a proposal can ask for
// more than one.
type AccessProposalRoleAndView struct {
	Role string `json:"role,omitempty"`
	// View is populated only for a proposal that belongs to a view, and
	// "published" is the only value Drive supports.
	View string `json:"view,omitempty"`
}

// AccessProposal is somebody's pending request to be let into a file.
// The API can resolve one and list them; it cannot create one, because
// only the person who was refused can ask.
type AccessProposal struct {
	ProposalID            string `json:"proposalId,omitempty"`
	FileID                string `json:"fileId,omitempty"`
	RequesterEmailAddress string `json:"requesterEmailAddress,omitempty"`
	// RecipientEmailAddress is who would receive the access, which is not
	// always the person who asked: somebody may request access for
	// another address.
	RecipientEmailAddress string                       `json:"recipientEmailAddress,omitempty"`
	RequestMessage        string                       `json:"requestMessage,omitempty"`
	CreateTime            string                       `json:"createTime,omitempty"`
	RolesAndViews         []*AccessProposalRoleAndView `json:"rolesAndViews,omitempty"`
}

// AccessProposalList is one page of accessproposals.list.
type AccessProposalList struct {
	AccessProposals []*AccessProposal `json:"accessProposals"`
	NextPageToken   string            `json:"nextPageToken,omitempty"`
}

// Actions accessproposals.resolve takes. Drive spells them in upper
// case, unlike every other enum in this API.
const (
	ProposalAccept = "ACCEPT"
	ProposalDeny   = "DENY"
)

// ResolveProposal is the body of accessproposals.resolve. Role is a list
// and is required for ACCEPT; SendNotification carries no omitempty
// because this server always states it, and Drive's own default for it
// is not documented.
type ResolveProposal struct {
	Action           string   `json:"action"`
	Role             []string `json:"role,omitempty"`
	View             string   `json:"view,omitempty"`
	SendNotification bool     `json:"sendNotification"`
}

// Approval is a review a file is waiting on: who asked, who is to
// answer, and what they have said so far. Approvals are a Workspace
// feature and a GA part of the Drive API; whether an edition offers
// them is answered by the API rather than guessed at here.
type Approval struct {
	ApprovalID   string `json:"approvalId,omitempty"`
	TargetFileID string `json:"targetFileId,omitempty"`
	Initiator    *User  `json:"initiator,omitempty"`
	// Status is IN_PROGRESS, APPROVED, CANCELLED or DECLINED. It is
	// output only: an approval's state follows from the reviewers'
	// answers rather than being set.
	Status            string              `json:"status,omitempty"`
	ReviewerResponses []*ReviewerResponse `json:"reviewerResponses,omitempty"`
	DueTime           string              `json:"dueTime,omitempty"`
	CreateTime        string              `json:"createTime,omitempty"`
	ModifyTime        string              `json:"modifyTime,omitempty"`
	CompleteTime      string              `json:"completeTime,omitempty"`
	// FileContentChangeBehavior is RESET_APPROVAL or NO_APPROVAL_ACTION.
	// RESET_APPROVAL means a content change while the approval is in
	// progress clears the approvals given — and that once approved, the
	// file is LOCKED.
	FileContentChangeBehavior string `json:"fileContentChangeBehavior,omitempty"`
}

// ReviewerResponse is one reviewer's answer, or the absence of one.
type ReviewerResponse struct {
	Reviewer *User `json:"reviewer,omitempty"`
	// Response is NO_RESPONSE, APPROVED or DECLINED.
	Response string `json:"response,omitempty"`
}

// ApprovalList is one page of a file's approvals. The list member is
// `items`, not `approvals`: the approvals endpoints are older in shape
// than the rest of v3.
type ApprovalList struct {
	Items         []*Approval `json:"items,omitempty"`
	NextPageToken string      `json:"nextPageToken,omitempty"`
}

// StartApproval opens a review on a file.
type StartApproval struct {
	// ReviewerEmails is required: an approval with nobody to answer it
	// is not a state the API offers.
	ReviewerEmails []string `json:"reviewerEmails,omitempty"`
	Message        string   `json:"message,omitempty"`
	// LockFile locks the file's content for the duration.
	LockFile bool   `json:"lockFile,omitempty"`
	DueTime  string `json:"dueTime,omitempty"`
	// FileContentChangeBehavior decides what a content change does to
	// answers already given.
	FileContentChangeBehavior string `json:"fileContentChangeBehavior,omitempty"`
}

// ApprovalMessage is the body every other approval verb takes: approve,
// decline, cancel and comment differ in their endpoint and in nothing
// else. The message is required only for comment.
type ApprovalMessage struct {
	Message string `json:"message,omitempty"`
}

// ReassignApproval adds reviewers or replaces them. The request's own
// description is exact about the limit: "Reviewers can be added or
// replaced, but not removed" — a replacement names the person going and
// the person arriving together, and there is no way to say only the
// first.
//
// Both members are arrays of OBJECTS, not of addresses. Phase 4 sent
// bare strings here at first, which Drive answers 400 to on every call:
// the discovery document was read through a projection that dropped the
// items' $ref, and the fake decoded into the same wrong struct, so
// nothing could fail.
type ReassignApproval struct {
	AddReviewers     []AddReviewer     `json:"addReviewers,omitempty"`
	ReplaceReviewers []ReplaceReviewer `json:"replaceReviewers,omitempty"`
	Message          string            `json:"message,omitempty"`
}

// AddReviewer is one reviewer joining an approval.
type AddReviewer struct {
	AddedReviewerEmail string `json:"addedReviewerEmail,omitempty"`
}

// ReplaceReviewer swaps one reviewer for another. Both addresses are
// required: this is the only way the API removes anybody, and it removes
// them only by putting somebody else in their place.
type ReplaceReviewer struct {
	RemovedReviewerEmail string `json:"removedReviewerEmail,omitempty"`
	AddedReviewerEmail   string `json:"addedReviewerEmail,omitempty"`
}

// Drive Activity is a separate API (driveactivity.googleapis.com, v2)
// with a scope of its own. It answers "who did what to this" — with one
// large caveat this server has to carry rather than hide: it names a
// person by a People API resource name (`people/123456`), never by a
// display name or an address. Resolving one would mean a third API and a
// third scope. So an activity can say that something was done by you, or
// by somebody else, and no more than that.

// ActivityQuery asks the Drive Activity API for a page of activity.
// Exactly one of ItemName and AncestorName may be set.
type ActivityQuery struct {
	// ItemName is `items/{fileId}`: activity on that one item.
	ItemName string `json:"itemName,omitempty"`
	// AncestorName is `items/{folderId}`: activity on a folder and
	// everything under it.
	AncestorName string `json:"ancestorName,omitempty"`
	PageSize     int    `json:"pageSize,omitempty"`
	PageToken    string `json:"pageToken,omitempty"`
	// Filter is the API's own expression language over `time` and
	// `detail.action_detail_case`.
	Filter string `json:"filter,omitempty"`
}

// ActivityResponse is one page of activity.
type ActivityResponse struct {
	Activities    []*DriveActivity `json:"activities,omitempty"`
	NextPageToken string           `json:"nextPageToken,omitempty"`
}

// DriveActivity is one thing that happened.
type DriveActivity struct {
	PrimaryActionDetail *ActionDetail     `json:"primaryActionDetail,omitempty"`
	Actors              []*ActivityActor  `json:"actors,omitempty"`
	Targets             []*ActivityTarget `json:"targets,omitempty"`
	// Timestamp is set for an activity at one instant; TimeRange is set
	// for a consolidated one. Exactly one of them arrives.
	Timestamp string             `json:"timestamp,omitempty"`
	TimeRange *ActivityTimeRange `json:"timeRange,omitempty"`
}

// ActivityTimeRange is when a consolidated activity happened.
type ActivityTimeRange struct {
	StartTime string `json:"startTime,omitempty"`
	EndTime   string `json:"endTime,omitempty"`
}

// ActionDetail says what kind of thing happened. Exactly one member is
// set, and which one IS the answer: the API has no action-type field.
type ActionDetail struct {
	Create             *ActivityCreate     `json:"create,omitempty"`
	Edit               *struct{}           `json:"edit,omitempty"`
	Move               *ActivityMove       `json:"move,omitempty"`
	Rename             *ActivityRename     `json:"rename,omitempty"`
	Delete             *ActivityTyped      `json:"delete,omitempty"`
	Restore            *ActivityTyped      `json:"restore,omitempty"`
	PermissionChange   *ActivityPermission `json:"permissionChange,omitempty"`
	Comment            *ActivityComment    `json:"comment,omitempty"`
	DLPChange          *struct{}           `json:"dlpChange,omitempty"`
	Reference          *struct{}           `json:"reference,omitempty"`
	SettingsChange     *struct{}           `json:"settingsChange,omitempty"`
	AppliedLabelChange *struct{}           `json:"appliedLabelChange,omitempty"`
}

// ActivityCreate says how an item came to be.
type ActivityCreate struct {
	New    *struct{} `json:"new,omitempty"`
	Upload *struct{} `json:"upload,omitempty"`
	Copy   *struct{} `json:"copy,omitempty"`
}

// ActivityMove records the parents added and removed.
type ActivityMove struct {
	AddedParents   []*ActivityTarget `json:"addedParents,omitempty"`
	RemovedParents []*ActivityTarget `json:"removedParents,omitempty"`
}

// ActivityRename records the titles before and after.
type ActivityRename struct {
	OldTitle string `json:"oldTitle,omitempty"`
	NewTitle string `json:"newTitle,omitempty"`
}

// ActivityTyped is a delete or a restore, whose only detail is its type.
type ActivityTyped struct {
	Type string `json:"type,omitempty"`
}

// ActivityPermission records grants added and removed. The permissions
// themselves carry no address either, for the same reason as the actor.
type ActivityPermission struct {
	AddedPermissions   []map[string]any `json:"addedPermissions,omitempty"`
	RemovedPermissions []map[string]any `json:"removedPermissions,omitempty"`
}

// ActivityComment records a comment, and which kind.
type ActivityComment struct {
	Post       *struct{} `json:"post,omitempty"`
	Assignment *struct{} `json:"assignment,omitempty"`
	Suggestion *struct{} `json:"suggestion,omitempty"`
}

// ActivityActor is who did it. Only the KnownUser case carries anything,
// and what it carries is a People API resource name.
type ActivityActor struct {
	User          *ActivityUser `json:"user,omitempty"`
	Anonymous     *struct{}     `json:"anonymous,omitempty"`
	System        *struct{}     `json:"system,omitempty"`
	Administrator *struct{}     `json:"administrator,omitempty"`
	Impersonation *struct{}     `json:"impersonation,omitempty"`
}

// ActivityUser is a person, as far as this API will say.
type ActivityUser struct {
	KnownUser   *ActivityKnownUser `json:"knownUser,omitempty"`
	DeletedUser *struct{}          `json:"deletedUser,omitempty"`
	UnknownUser *struct{}          `json:"unknownUser,omitempty"`
}

// ActivityKnownUser is a person the API will identify only by a People
// API resource name, plus whether it is the signed-in account.
type ActivityKnownUser struct {
	PersonName    string `json:"personName,omitempty"`
	IsCurrentUser bool   `json:"isCurrentUser,omitempty"`
}

// ActivityTarget is what the activity was about.
type ActivityTarget struct {
	DriveItem   *ActivityDriveItem `json:"driveItem,omitempty"`
	Drive       map[string]any     `json:"drive,omitempty"`
	FileComment map[string]any     `json:"fileComment,omitempty"`
}

// ActivityDriveItem is a file or folder an activity was about. `name` is
// `items/{fileId}`, so the id has to be cut out of it.
type ActivityDriveItem struct {
	Name     string         `json:"name,omitempty"`
	Title    string         `json:"title,omitempty"`
	MimeType string         `json:"mimeType,omitempty"`
	File     *struct{}      `json:"driveFile,omitempty"`
	Folder   map[string]any `json:"driveFolder,omitempty"`
}

// Operation is a long-running operation. files.download is the only one
// this server starts: it is the only way to get the bytes of a Google
// Vid, and Drive refuses to export one at all.
//
// The discovery document types the response as a bare Any, so the shape
// below comes from the long-running-operations guide rather than from
// the document. Done is a pointer because the guide's own example of a
// pending operation has `done: null` rather than `done: false`, and the
// two would otherwise decode alike.
type Operation struct {
	Name     string             `json:"name,omitempty"`
	Done     *bool              `json:"done,omitempty"`
	Metadata *OperationMetadata `json:"metadata,omitempty"`
	Response *DownloadResponse  `json:"response,omitempty"`
	Error    *OperationError    `json:"error,omitempty"`
}

// OperationMetadata carries the resource key a link-shared file needs on
// the follow-up request.
type OperationMetadata struct {
	ResourceKey string `json:"resourceKey,omitempty"`
}

// DownloadResponse is a finished download's answer: where to fetch the
// bytes from.
type DownloadResponse struct {
	DownloadURI string `json:"downloadUri,omitempty"`
	// PartialDownloadAllowed says whether the URI takes a Range header.
	// It is true for blob content and false for an exported document.
	PartialDownloadAllowed bool `json:"partialDownloadAllowed,omitempty"`
}

// OperationError is a failed operation's reason, in google.rpc.Status
// shape rather than in Drive's usual error shape.
type OperationError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// Finished reports whether the operation has completed. The guide's
// pending example carries `done: null`, so an absent member means "still
// running" rather than "finished and false".
func (o *Operation) Finished() bool {
	return o != nil && o.Done != nil && *o.Done
}
