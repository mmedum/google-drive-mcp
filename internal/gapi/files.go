package gapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// FileFields is what a single-file read asks Drive for. It is one fixed
// list so a file card never depends on which call produced the file, and
// files.get costs the same 5 units whatever it returns.
const FileFields = "id,name,mimeType,description,parents,starred,trashed,explicitlyTrashed," +
	"trashedTime,trashingUser(displayName,emailAddress),createdTime,modifiedTime,modifiedByMeTime," +
	"viewedByMeTime,sharedWithMeTime,owners(displayName,emailAddress,me),lastModifyingUser(displayName,emailAddress,me)," +
	"sharingUser(displayName,emailAddress),ownedByMe,shared,webViewLink,size,quotaBytesUsed,md5Checksum," +
	"sha256Checksum,headRevisionId,version,fileExtension,originalFilename,folderColorRgb,driveId,resourceKey," +
	"writersCanShare,copyRequiresWriterPermission,capabilities,shortcutDetails,linkShareMetadata," +
	"permissions,permissionIds,properties,appProperties,exportLinks,contentRestrictions"

// ListFileFields is the leaner per-file list for search and folder
// listings: a page of 100 files carries no capabilities or permissions,
// because a listing shows kind, name, id, location and modification.
const ListFileFields = "id,name,mimeType,parents,starred,trashed,createdTime,modifiedTime," +
	"owners(displayName,emailAddress,me),lastModifyingUser(displayName,emailAddress,me),shared," +
	"size,driveId,resourceKey,shortcutDetails,webViewLink"

// MaxPageSize is Drive's hard limit for a listing page.
const MaxPageSize = 1000

// About returns the signed-in account, its storage and whether it can
// create shared drives. The cheapest authenticated call there is.
func (c *Client) About(ctx context.Context) (*gdrive.About, error) {
	u := c.base + "/about?fields=" + url.QueryEscape("user(displayName,emailAddress,permissionId),storageQuota,canCreateDrives,maxUploadSize")
	body, err := c.do(ctx, request{kind: kindRead, method: http.MethodGet, url: u})
	if err != nil {
		return nil, err
	}
	var a gdrive.About
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("%w: decode about: %w", ErrUnexpected, err)
	}
	if a.User == nil {
		return nil, fmt.Errorf("%w: about response without a user", ErrUnexpected)
	}
	return &a, nil
}

// GetFileOptions tune a single-file read.
type GetFileOptions struct {
	// Fields overrides FileFields.
	Fields string
	// IncludeLabels asks for labelInfo, which needs the labels scopes.
	IncludeLabels bool
	// ResourceKey is a key learned from a URL; it is remembered for the
	// process before the call goes out.
	ResourceKey string
}

// GetFile reads one file's metadata. Every request carries
// supportsAllDrives, so a shared-drive item is never invisible.
func (c *Client) GetFile(ctx context.Context, id string, o GetFileOptions) (*gdrive.File, error) {
	if o.ResourceKey != "" {
		c.RememberResourceKey(id, o.ResourceKey)
	}
	fields := o.Fields
	if fields == "" {
		fields = FileFields
	}
	if o.IncludeLabels {
		fields += ",labelInfo"
	}
	q := url.Values{}
	q.Set("fields", fields)
	q.Set("supportsAllDrives", "true")
	if o.IncludeLabels {
		q.Set("includeLabels", "*")
	}
	u := c.base + "/files/" + url.PathEscape(id) + "?" + q.Encode()
	body, err := c.do(ctx, request{kind: kindRead, method: http.MethodGet, url: u, resourceIDs: []string{id}})
	if err != nil {
		return nil, err
	}
	var f gdrive.File
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("%w: decode file: %w", ErrUnexpected, err)
	}
	c.rememberKeys(&f)
	return &f, nil
}

// Corpora values for files.list.
const (
	CorporaAllDrives = "allDrives"
	CorporaUser      = "user"
	CorporaDrive     = "drive"
)

// ListQuery is one page of files.list. The caller builds and escapes Q;
// internal/service owns the query syntax so the escaping rules live in
// one place.
type ListQuery struct {
	Q         string
	PageSize  int
	PageToken string
	OrderBy   string
	// DriveID limits the listing to one shared drive and implies
	// corpora=drive.
	DriveID string
	// Corpora defaults to allDrives, or to drive when DriveID is set.
	Corpora string
	Fields  string
	// Spaces defaults to "drive"; appDataFolder is deliberately not used.
	Spaces string
	// ResourceIDs carry resource keys for parents named in Q.
	ResourceIDs []string
}

// ListFiles returns one page of files. Listings default to every drive
// the account can see, because a server that hides shared drives is the
// single most common complaint about the alternatives.
func (c *Client) ListFiles(ctx context.Context, lq ListQuery) (*gdrive.FileList, error) {
	v := url.Values{}
	if lq.Q != "" {
		v.Set("q", lq.Q)
	}
	size := lq.PageSize
	switch {
	case size <= 0:
		size = 100
	case size > MaxPageSize:
		size = MaxPageSize
	}
	v.Set("pageSize", strconv.Itoa(size))
	if lq.PageToken != "" {
		v.Set("pageToken", lq.PageToken)
	}
	if lq.OrderBy != "" {
		v.Set("orderBy", lq.OrderBy)
	}
	fields := lq.Fields
	if fields == "" {
		fields = "nextPageToken,incompleteSearch,files(" + ListFileFields + ")"
	}
	v.Set("fields", fields)
	v.Set("supportsAllDrives", "true")
	v.Set("includeItemsFromAllDrives", "true")
	corpora := lq.Corpora
	if lq.DriveID != "" {
		corpora = CorporaDrive
		v.Set("driveId", lq.DriveID)
	}
	if corpora == "" {
		corpora = CorporaAllDrives
	}
	v.Set("corpora", corpora)
	if lq.Spaces != "" {
		v.Set("spaces", lq.Spaces)
	}
	u := c.base + "/files?" + v.Encode()
	body, err := c.do(ctx, request{kind: kindRead, method: http.MethodGet, url: u, resourceIDs: lq.ResourceIDs})
	if err != nil {
		return nil, err
	}
	var list gdrive.FileList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("%w: decode file list: %w", ErrUnexpected, err)
	}
	for _, f := range list.Files {
		c.rememberKeys(f)
	}
	return &list, nil
}

// GenerateIDs asks Drive for ids that files.create and files.copy accept
// in the request body. Carrying one makes a create idempotent: a retry
// after an ambiguous failure cannot produce a second file.
func (c *Client) GenerateIDs(ctx context.Context, count int) ([]string, error) {
	if count <= 0 {
		count = 1
	}
	if count > 1000 {
		count = 1000
	}
	v := url.Values{}
	v.Set("count", strconv.Itoa(count))
	v.Set("space", "drive")
	v.Set("type", "files")
	u := c.base + "/files/generateIds?" + v.Encode()
	body, err := c.do(ctx, request{kind: kindRead, method: http.MethodGet, url: u})
	if err != nil {
		return nil, err
	}
	var got gdrive.GeneratedIDs
	if err := json.Unmarshal(body, &got); err != nil {
		return nil, fmt.Errorf("%w: decode generated ids: %w", ErrUnexpected, err)
	}
	if len(got.IDs) == 0 {
		return nil, fmt.Errorf("%w: generateIds returned no ids", ErrUnexpected)
	}
	return got.IDs, nil
}

// PermissionFields is what a permission listing asks for.
const PermissionFields = "id,type,role,emailAddress,domain,displayName,allowFileDiscovery," +
	"expirationTime,deleted,pendingOwner,permissionDetails"

// ListPermissions returns every grant on a file or shared drive, paging
// to the end. A file's permissions are few; a shared drive's are the
// membership list.
func (c *Client) ListPermissions(ctx context.Context, fileID string) ([]*gdrive.Permission, error) {
	var out []*gdrive.Permission
	pageToken := ""
	for {
		v := url.Values{}
		v.Set("fields", "nextPageToken,permissions("+PermissionFields+")")
		v.Set("supportsAllDrives", "true")
		v.Set("pageSize", "100")
		if pageToken != "" {
			v.Set("pageToken", pageToken)
		}
		u := c.base + "/files/" + url.PathEscape(fileID) + "/permissions?" + v.Encode()
		body, err := c.do(ctx, request{kind: kindRead, method: http.MethodGet, url: u, resourceIDs: []string{fileID}})
		if err != nil {
			return nil, err
		}
		var page gdrive.PermissionList
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("%w: decode permissions: %w", ErrUnexpected, err)
		}
		out = append(out, page.Permissions...)
		if page.NextPageToken == "" || len(page.Permissions) == 0 {
			return out, nil
		}
		pageToken = page.NextPageToken
	}
}

// rememberKeys records every resource key a response carried, so a later
// call for that id sends the header without the caller thinking about it.
func (c *Client) rememberKeys(f *gdrive.File) {
	if f == nil {
		return
	}
	c.RememberResourceKey(f.ID, f.ResourceKey)
	if f.ShortcutDetails != nil {
		c.RememberResourceKey(f.ShortcutDetails.TargetID, f.ShortcutDetails.TargetResourceKey)
	}
}

// ExportFormats turns the exportLinks map Drive returns into the short
// format names the tools speak, without exposing the links themselves.
func ExportFormats(f *gdrive.File) []string {
	if f == nil || len(f.ExportLinks) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for mime := range f.ExportLinks {
		if name := ExportFormatName(mime); name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// exportMimeToName maps the export MIME types Drive offers to the short
// names used in tool arguments and output.
var exportMimeToName = map[string]string{
	"application/pdf": "pdf",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   "docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "xlsx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
	"application/vnd.oasis.opendocument.text":                                   "odt",
	"application/vnd.oasis.opendocument.spreadsheet":                            "ods",
	"application/vnd.oasis.opendocument.presentation":                           "odp",
	"application/rtf":           "rtf",
	"text/plain":                "txt",
	"text/html":                 "html",
	"text/markdown":             "md",
	"text/csv":                  "csv",
	"text/tab-separated-values": "tsv",
	"application/zip":           "zip",
	"application/epub+zip":      "epub",
	"image/jpeg":                "jpg",
	"image/png":                 "png",
	"image/svg+xml":             "svg",
	"application/vnd.google-apps.script+json": "json",
}

// ExportFormatName returns the short name for an export MIME type, or "".
func ExportFormatName(mime string) string {
	return exportMimeToName[strings.TrimSpace(mime)]
}
