package gapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
	u := c.base + "/about?fields=" + url.QueryEscape(
		"user(displayName,emailAddress,permissionId),storageQuota,canCreateDrives,maxUploadSize,importFormats")
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
	segment, err := fileSegment(id)
	if err != nil {
		return nil, err
	}
	u := c.base + "/files/" + segment + "?" + q.Encode()
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
	// kindRead, and kindRead now grants a retry, so this is the one call
	// worth justifying: it is the only request here that is not a plain
	// read, because it allocates. Repeating it is still safe — a second
	// attempt hands back different ids and the unused ones cost nothing,
	// since an id becomes a file only when a create carries it. A later
	// call that allocates something with a cost must not borrow the
	// label.
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
	segment, err := fileSegment(fileID)
	if err != nil {
		return nil, err
	}
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
		u := c.base + "/files/" + segment + "/permissions?" + v.Encode()
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

// WriteOptions are the parameters a metadata write shares.
type WriteOptions struct {
	// Fields overrides FileFields on the response.
	Fields string
	// OCRLanguage hints the language of text extracted from an image or
	// a PDF being converted to a document.
	OCRLanguage string
	// KeepRevisionForever pins the revision the call creates.
	KeepRevisionForever bool
	// ResourceIDs carry resource keys for the ids this call names.
	ResourceIDs []string
}

// carriesID reports whether a create body holds a pre-generated id,
// which is the one thing that makes a POST to Drive safe to repeat:
// Drive refuses the second attempt as a duplicate rather than making a
// second file. Drive will not accept one for its own formats
// (AcceptsGeneratedID), so those creates are not repeatable and are not
// marked as such.
func carriesID(meta *gdrive.FileMeta) bool {
	return meta != nil && meta.ID != ""
}

// withFile puts the file being written at the head of the ids whose
// resource keys ride along on the call.
func (o WriteOptions) withFile(id string) []string {
	return append([]string{id}, o.ResourceIDs...)
}

func (o WriteOptions) values() url.Values {
	v := url.Values{}
	fields := o.Fields
	if fields == "" {
		fields = FileFields
	}
	v.Set("fields", fields)
	v.Set("supportsAllDrives", "true")
	if o.OCRLanguage != "" {
		v.Set("ocrLanguage", o.OCRLanguage)
	}
	if o.KeepRevisionForever {
		v.Set("keepRevisionForever", "true")
	}
	return v
}

// CreateFile creates a file from metadata alone: a folder, a shortcut,
// or an empty Workspace document. Content goes through the upload paths
// instead. A pre-generated id in the body makes the call idempotent, so
// a retry after an ambiguous failure cannot leave two files behind.
func (c *Client) CreateFile(ctx context.Context, meta *gdrive.FileMeta, o WriteOptions) (*gdrive.File, error) {
	return c.writeFile(ctx, http.MethodPost, c.base+"/files?"+o.values().Encode(), meta, o.ResourceIDs)
}

// writeFile sends one metadata write and decodes the file it answers
// with. Create, update and copy differ in method and URL and in nothing
// else, so the marshalling, the request and the resource-key bookkeeping
// are written once.
func (c *Client) writeFile(ctx context.Context, method, u string, meta *gdrive.FileMeta, ids []string) (*gdrive.File, error) {
	payload, err := json.Marshal(orEmptyMeta(meta))
	if err != nil {
		return nil, err
	}
	body, err := c.do(ctx, request{kind: kindWrite, method: method, url: u,
		body: payload, resourceIDs: ids, idempotent: carriesID(meta)})
	if err != nil {
		return nil, err
	}
	f, err := decodeFile(body)
	if err != nil {
		return nil, err
	}
	c.rememberKeys(f)
	return f, nil
}

// UpdateOptions add the move parameters to a metadata patch. Drive has
// allowed one parent since 2020, so a move is a patch that names the
// parent to add and the one to remove.
type UpdateOptions struct {
	WriteOptions
	AddParents    string
	RemoveParents string
}

// UpdateFile patches one file's metadata. Only the fields present in
// meta change, which is what makes the call idempotent and safe to
// retry.
func (c *Client) UpdateFile(ctx context.Context, id string, meta *gdrive.FileMeta, o UpdateOptions) (*gdrive.File, error) {
	v := o.values()
	if o.AddParents != "" {
		v.Set("addParents", o.AddParents)
	}
	if o.RemoveParents != "" {
		v.Set("removeParents", o.RemoveParents)
	}
	segment, err := fileSegment(id)
	if err != nil {
		return nil, err
	}
	u := c.base + "/files/" + segment + "?" + v.Encode()
	return c.writeFile(ctx, http.MethodPatch, u, meta, o.withFile(id))
}

// CopyFile copies one file. A body mimeType different from the source's
// asks Drive to convert as it copies, which is how a PDF or an image
// becomes a Google Doc with its text extracted. Folders cannot be
// copied; Drive refuses them.
func (c *Client) CopyFile(ctx context.Context, id string, meta *gdrive.FileMeta, o WriteOptions) (*gdrive.File, error) {
	segment, err := fileSegment(id)
	if err != nil {
		return nil, err
	}
	u := c.base + "/files/" + segment + "/copy?" + o.values().Encode()
	return c.writeFile(ctx, http.MethodPost, u, meta, o.withFile(id))
}

// AcceptsGeneratedID reports whether a file of this type may be created
// with an id from files.generateIds.
//
// Four answers are confirmed live, 2026-09-05, and they do not follow
// one rule that Google states anywhere:
//
//   - a file with bytes of its own takes an id;
//   - a folder takes one;
//   - the Docs Editors formats refuse it — "Generated IDs are not
//     supported for Docs Editors formats";
//   - a shortcut refuses it too, with a different message — "The
//     provided file ID is not usable".
//
// So this names what is known to work rather than enumerating the
// refusals, and every Google-native type nobody has tried — Sites, Maps,
// Vids, Jamboards, Apps Script — falls on the safe side by default.
// Withholding an id costs idempotency, which the duplicate-name guard
// then has to catch; sending one where Drive refuses it costs the whole
// call, as it did twice.
func AcceptsGeneratedID(mime string) bool {
	mime = strings.TrimSpace(mime)
	return mime == gdrive.MimeFolder || !gdrive.IsGoogleMime(mime)
}

// reservedFileSegments are the path segments Drive uses under /files for
// something other than a file id. A caller-supplied id that spells one
// of them addresses a different method than the caller asked for, and
// url.PathEscape does not stop it: every character in them is legal in a
// path segment, so there is nothing for it to escape.
//
// The worst case is not hypothetical arithmetic. "trash" is
// files.emptyTrash, so DeleteFile("trash") would build
// DELETE /files/trash and destroy an entire trash instead of one file —
// a bounded destructive call silently becoming an unbounded one. The
// resolve step in internal/service happens to catch it today, because
// Drive answers 404 for GET /files/trash, but that is protection by
// accident at three layers' distance from the URL being built. It
// belongs here, where the segment is made.
var reservedFileSegments = map[string]bool{"trash": true, "generateIds": true}

// fileSegment turns a file id into a path segment, refusing one that
// would address a different endpoint.
func fileSegment(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("%w: a file id is required", ErrInvalid)
	}
	if reservedFileSegments[id] {
		return "", fmt.Errorf("%w: %q is not a file id; Drive uses it as an endpoint of its own under /files, "+
			"and a request built with it would call that endpoint instead", ErrInvalid, id)
	}
	return url.PathEscape(id), nil
}
