// Package drivetest is an in-memory Google Drive behind httptest, so
// every layer above internal/gapi can be tested without a network, an
// account, or a fixture that came from someone's real Drive. Everything
// in it is synthetic.
//
// It implements the parts of the API this server calls, with the rules
// that actually bite: one parent per file, trashing a folder cascades,
// one permission per principal, inherited shared-drive grants, and the
// prefix semantics of `name contains` (see query.go). Failures can be
// injected per request so retry and backoff paths are exercised too.
package drivetest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// AccountEmail is the synthetic signed-in account. Nothing here refers
// to a real person, organisation or domain.
const AccountEmail = "person@example.com"

// AccountName is that account's display name.
const AccountName = "Test Person"

// RootFolderID is the id of My Drive's root in the fake.
const RootFolderID = "id-root-my-drive"

// Failure is an injected error response.
type Failure struct {
	// Status is the HTTP status to answer with.
	Status int
	// Reason is Google's machine-readable reason, e.g. rateLimitExceeded.
	Reason string
	// Message is Google's human-readable message.
	Message string
	// RetryAfter, when set, is sent as the Retry-After header.
	RetryAfter string
	// Hijack cuts the connection instead of answering, the way a dropped
	// network does.
	Hijack bool
}

// Recorded is one request the fake served.
type Recorded struct {
	Method string
	Path   string
	Query  url.Values
	// ResourceKeys is the X-Goog-Drive-Resource-Keys header, if any.
	ResourceKeys string
}

// Server is the fake Drive.
type Server struct {
	*httptest.Server

	mu sync.Mutex

	// RootID is the id of My Drive's root folder.
	RootID string
	// Files is every file, folder and shortcut by id.
	Files map[string]*gdrive.File
	// Content is the searchable body text of a file, for fullText.
	Content map[string]string
	// Permissions are the grants on a file or shared drive id.
	Permissions map[string][]*gdrive.Permission
	// Drives are the shared drives the account can see.
	Drives map[string]*gdrive.Drive
	// About is what about.get answers.
	About *gdrive.About

	// Fail is consulted before every request; a non-nil result is served
	// instead of the real answer.
	Fail func(r *http.Request) *Failure
	// Requests records everything served, for assertions.
	Requests []Recorded

	// nextID numbers generated ids.
	nextID int
	// now is the clock the fake stamps times with.
	now func() time.Time
}

// New starts a fake Drive holding an empty My Drive.
func New() *Server {
	s := &Server{
		RootID:      RootFolderID,
		Files:       map[string]*gdrive.File{},
		Content:     map[string]string{},
		Permissions: map[string][]*gdrive.Permission{},
		Drives:      map[string]*gdrive.Drive{},
		now:         time.Now,
	}
	s.About = &gdrive.About{
		User: &gdrive.User{DisplayName: AccountName, EmailAddress: AccountEmail, Me: true, PermissionID: "id-permission-self"},
		StorageQuota: &gdrive.StorageQuota{
			Limit: "16106127360", Usage: "4294967296", UsageInDrive: "3221225472", UsageInDriveTrash: "1073741824",
		},
		CanCreateDrives: true,
		MaxUploadSize:   "5497558138880",
	}
	s.Files[s.RootID] = &gdrive.File{
		ID: s.RootID, Name: "My Drive", MimeType: gdrive.MimeFolder,
		Capabilities: &gdrive.Capabilities{CanListChildren: true, CanAddChildren: true, CanEdit: true},
		OwnedByMe:    true, Owners: []*gdrive.User{s.me()},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Close shuts the fake down.
func (s *Server) Close() { s.Server.Close() }

// BaseURL is the value to pass as gapi.Options.BaseURL.
func (s *Server) BaseURL() string { return s.URL + "/drive/v3" }

func (s *Server) me() *gdrive.User {
	return &gdrive.User{DisplayName: AccountName, EmailAddress: AccountEmail, Me: true}
}

// Now returns the fake's clock time.
func (s *Server) Now() time.Time { return s.now() }

// SetNow fixes the clock so timestamps in goldens are stable.
func (s *Server) SetNow(f func() time.Time) { s.now = f }

// FileOpt customises a file being added.
type FileOpt func(*gdrive.File)

// Trashed marks the file as trashed.
func Trashed() FileOpt { return func(f *gdrive.File) { f.Trashed = true; f.ExplicitlyTrashed = true } }

// Starred marks the file as starred.
func Starred() FileOpt { return func(f *gdrive.File) { f.Starred = true } }

// Size sets a blob's byte count and checksum.
func Size(n int64, md5 string) FileOpt {
	return func(f *gdrive.File) {
		f.Size = strconv.FormatInt(n, 10)
		f.QuotaBytesUsed = f.Size
		f.MD5Checksum = md5
	}
}

// Owner replaces the owner with someone else, so "owned by me" is false.
func Owner(name, email string) FileOpt {
	return func(f *gdrive.File) {
		f.Owners = []*gdrive.User{{DisplayName: name, EmailAddress: email}}
		f.OwnedByMe = false
	}
}

// Described sets the description.
func Described(d string) FileOpt { return func(f *gdrive.File) { f.Description = d } }

// InDrive puts the file in a shared drive.
func InDrive(driveID string) FileOpt { return func(f *gdrive.File) { f.DriveID = driveID } }

// WithResourceKey gives the file a resource key, as a link-shared file
// under the 2021 security update has.
func WithResourceKey(key string) FileOpt { return func(f *gdrive.File) { f.ResourceKey = key } }

// WithCapabilities replaces the computed capabilities.
func WithCapabilities(c gdrive.Capabilities) FileOpt {
	return func(f *gdrive.File) { f.Capabilities = &c }
}

// Modified sets the modification time and who made it.
func Modified(ts string, name, email string) FileOpt {
	return func(f *gdrive.File) {
		f.ModifiedTime = ts
		f.LastModifyingUser = &gdrive.User{DisplayName: name, EmailAddress: email}
	}
}

// AddFolder adds a folder and returns it.
func (s *Server) AddFolder(id, name, parent string, opts ...FileOpt) *gdrive.File {
	return s.AddFile(id, name, gdrive.MimeFolder, parent, opts...)
}

// AddFile adds a file, folder or shortcut and returns it.
func (s *Server) AddFile(id, name, mime, parent string, opts ...FileOpt) *gdrive.File {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := s.now().UTC().Format(time.RFC3339)
	f := &gdrive.File{
		ID: id, Name: name, MimeType: mime,
		CreatedTime: ts, ModifiedTime: ts,
		Owners: []*gdrive.User{s.me()}, LastModifyingUser: s.me(), OwnedByMe: true,
		WebViewLink:  webViewLink(id, mime),
		Capabilities: defaultCapabilities(mime),
	}
	if parent != "" {
		f.Parents = []string{parent}
	}
	for _, o := range opts {
		o(f)
	}
	s.Files[id] = f
	return f
}

// AddShortcut adds a shortcut pointing at target.
func (s *Server) AddShortcut(id, name, parent, targetID string) *gdrive.File {
	f := s.AddFile(id, name, gdrive.MimeShortcut, parent)
	s.mu.Lock()
	defer s.mu.Unlock()
	target := s.Files[targetID]
	sd := &gdrive.ShortcutDetails{TargetID: targetID}
	if target != nil {
		sd.TargetMimeType = target.MimeType
		sd.TargetResourceKey = target.ResourceKey
	}
	f.ShortcutDetails = sd
	return f
}

// AddDrive adds a shared drive and its root folder.
func (s *Server) AddDrive(id, name string) *gdrive.Drive {
	d := &gdrive.Drive{
		ID: id, Name: name, CreatedTime: s.now().UTC().Format(time.RFC3339),
		Capabilities: &gdrive.DriveCapabilities{
			CanListChildren: true, CanAddChildren: true, CanEdit: true,
			CanManageMembers: true, CanShare: true, CanRenameDrive: true,
		},
		Restrictions: &gdrive.DriveRestrictions{},
	}
	s.mu.Lock()
	s.Drives[id] = d
	// A shared drive's root folder id is the drive id.
	s.Files[id] = &gdrive.File{
		ID: id, Name: name, MimeType: gdrive.MimeFolder, DriveID: id,
		Capabilities: &gdrive.Capabilities{CanListChildren: true, CanAddChildren: true, CanEdit: true, CanShare: true},
	}
	s.mu.Unlock()
	return d
}

// Grant adds or replaces a permission, keeping Drive's one-per-principal
// rule.
func (s *Server) Grant(fileID string, p *gdrive.Permission) *gdrive.Permission {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.ID == "" {
		s.nextID++
		p.ID = fmt.Sprintf("id-permission-%d", s.nextID)
	}
	existing := s.Permissions[fileID]
	for i, e := range existing {
		if samePrincipal(e, p) {
			existing[i] = p
			return p
		}
	}
	s.Permissions[fileID] = append(existing, p)
	if f := s.Files[fileID]; f != nil && p.Type != "" {
		f.Shared = true
	}
	return p
}

func samePrincipal(a, b *gdrive.Permission) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case "user", "group":
		return strings.EqualFold(a.EmailAddress, b.EmailAddress)
	case "domain":
		return strings.EqualFold(a.Domain, b.Domain)
	default:
		return true
	}
}

// Requested returns the recorded requests and clears the log.
func (s *Server) Requested() []Recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.Requests
	s.Requests = nil
	return out
}

// Count returns how many recorded requests hit a path substring.
func (s *Server) Count(pathContains string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.Requests {
		if strings.Contains(r.Path, pathContains) {
			n++
		}
	}
	return n
}

// FailTimes returns a Fail function that injects f for the first n
// requests matching path, then lets the real handler answer.
func FailTimes(n int, pathContains string, f Failure) func(*http.Request) *Failure {
	var mu sync.Mutex
	left := n
	return func(r *http.Request) *Failure {
		if !strings.Contains(r.URL.Path, pathContains) {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		if left <= 0 {
			return nil
		}
		left--
		out := f
		return &out
	}
}

func webViewLink(id, mime string) string {
	switch mime {
	case gdrive.MimeFolder:
		return "https://drive.google.com/drive/folders/" + id
	case gdrive.MimeDocument:
		return "https://docs.google.com/document/d/" + id + "/edit"
	case gdrive.MimeSheet:
		return "https://docs.google.com/spreadsheets/d/" + id + "/edit"
	case gdrive.MimeSlides:
		return "https://docs.google.com/presentation/d/" + id + "/edit"
	default:
		return "https://drive.google.com/file/d/" + id + "/view"
	}
}

func defaultCapabilities(mime string) *gdrive.Capabilities {
	c := &gdrive.Capabilities{
		CanEdit: true, CanComment: true, CanShare: true, CanCopy: true, CanDownload: true,
		CanRename: true, CanTrash: true, CanUntrash: true, CanDelete: true,
		CanModifyContent: true, CanReadRevisions: true, CanMoveItemWithinDrive: true,
	}
	if mime == gdrive.MimeFolder {
		c.CanListChildren = true
		c.CanAddChildren = true
		c.CanRemoveChildren = true
		c.CanTrashChildren = true
		c.CanCopy = false
	}
	return c
}

// exportFormatsFor is the export table Drive advertises per Workspace
// kind, used to fill exportLinks.
var exportFormatsFor = map[string][]string{
	gdrive.MimeDocument: {"application/pdf", "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.oasis.opendocument.text", "application/rtf", "text/plain", "text/html", "text/markdown",
		"application/zip", "application/epub+zip"},
	gdrive.MimeSheet: {"application/pdf", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.oasis.opendocument.spreadsheet", "text/csv", "text/tab-separated-values", "application/zip"},
	gdrive.MimeSlides: {"application/pdf", "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"application/vnd.oasis.opendocument.presentation", "text/plain"},
	gdrive.MimeDrawing: {"application/pdf", "image/jpeg", "image/png", "image/svg+xml"},
}
