// Package service orchestrates the work behind each tool: resolve a
// reference, decide what is allowed, call Drive, and hand internal/render
// a model to lay out. Every rule worth testing lives here, so the tools
// stay thin.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/config"
	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
)

// API is the subset of the Drive client the service uses. It is an
// interface so the service can be tested against the in-memory Drive in
// internal/gapi/drivetest, and so a new call has to be added here on
// purpose rather than reached for by accident.
type API interface {
	About(ctx context.Context) (*gdrive.About, error)
	GetFile(ctx context.Context, id string, o gapi.GetFileOptions) (*gdrive.File, error)
	ListFiles(ctx context.Context, q gapi.ListQuery) (*gdrive.FileList, error)
	ListPermissions(ctx context.Context, fileID string) ([]*gdrive.Permission, error)
	ListDrives(ctx context.Context, o gapi.ListDrivesOptions) (*gdrive.DriveList, error)
	GenerateIDs(ctx context.Context, count int) ([]string, error)
	RememberResourceKey(id, key string)

	// Content in and out.
	Download(ctx context.Context, fileID string, o gapi.DownloadOptions) (*gapi.Content, error)
	Export(ctx context.Context, fileID, mimeType string) (*gapi.Content, error)
	DownloadURL(ctx context.Context, url string) (*gapi.Content, error)
	UploadMultipart(ctx context.Context, r gapi.UploadRequest, content []byte) (*gdrive.File, error)
	UploadResumable(ctx context.Context, r gapi.UploadRequest, content io.Reader, size int64, o gapi.ResumableOptions) (*gdrive.File, error)

	// Organising.
	CreateFile(ctx context.Context, meta *gdrive.FileMeta, o gapi.WriteOptions) (*gdrive.File, error)
	UpdateFile(ctx context.Context, id string, meta *gdrive.FileMeta, o gapi.UpdateOptions) (*gdrive.File, error)
	CopyFile(ctx context.Context, id string, meta *gdrive.FileMeta, o gapi.WriteOptions) (*gdrive.File, error)

	// History, as far as phase 1 needs it: pinning the revision an
	// update is about to replace.
	GetRevision(ctx context.Context, fileID, revisionID string) (*gdrive.Revision, error)
	UpdateRevision(ctx context.Context, fileID, revisionID string, keepForever bool) (*gdrive.Revision, error)

	// Access.
	CreatePermission(ctx context.Context, fileID string, meta *gdrive.PermissionMeta, o gapi.ShareOptions) (*gdrive.Permission, error)
	UpdatePermission(ctx context.Context, fileID, permissionID string, meta *gdrive.PermissionMeta, o gapi.UpdateShareOptions) (*gdrive.Permission, error)
	DeletePermission(ctx context.Context, fileID, permissionID string) error

	// Shared drives.
	GetDrive(ctx context.Context, driveID string) (*gdrive.Drive, error)
	CreateDrive(ctx context.Context, requestID string, meta *gdrive.DriveMeta) (*gdrive.Drive, error)
	UpdateDrive(ctx context.Context, driveID string, meta *gdrive.DriveMeta) (*gdrive.Drive, error)
	HideDrive(ctx context.Context, driveID string) (*gdrive.Drive, error)
	UnhideDrive(ctx context.Context, driveID string) (*gdrive.Drive, error)
	DeleteDrive(ctx context.Context, driveID string) error

	// History and the changes feed.
	ListRevisions(ctx context.Context, fileID string) ([]*gdrive.Revision, error)
	DeleteRevision(ctx context.Context, fileID, revisionID string) error
	StartPageToken(ctx context.Context, driveID string) (string, error)
	ListChanges(ctx context.Context, o gapi.ListChangesOptions) (*gdrive.ChangeList, error)

	// Comments and their replies.
	ListComments(ctx context.Context, fileID string, o gapi.ListCommentsOptions) (*gdrive.CommentList, error)
	GetComment(ctx context.Context, fileID, commentID string, includeDeleted bool) (*gdrive.Comment, error)
	CreateComment(ctx context.Context, fileID string, meta *gdrive.CommentMeta) (*gdrive.Comment, error)
	UpdateComment(ctx context.Context, fileID, commentID string, meta *gdrive.CommentMeta) (*gdrive.Comment, error)
	DeleteComment(ctx context.Context, fileID, commentID string) error
	CreateReply(ctx context.Context, fileID, commentID string, meta *gdrive.ReplyMeta) (*gdrive.Reply, error)
	UpdateReply(ctx context.Context, fileID, commentID, replyID string, meta *gdrive.ReplyMeta) (*gdrive.Reply, error)
	DeleteReply(ctx context.Context, fileID, commentID, replyID string) error

	// Labels. The values on a file are Drive's and need no labels scope;
	// the definitions are a separate API and do.
	AllFileLabels(ctx context.Context, fileID string) ([]*gdrive.Label, error)
	ModifyLabels(ctx context.Context, fileID string, req gdrive.ModifyLabelsRequest) (*gdrive.ModifyLabelsResponse, error)
	ListLabelDefinitions(ctx context.Context, o gapi.ListLabelDefinitionsOptions) (*gdrive.LabelDefinitionList, error)

	// Drive Activity, a separate API behind GDRIVE_ACTIVITY.
	QueryActivity(ctx context.Context, q gdrive.ActivityQuery) (*gdrive.ActivityResponse, error)

	// Approvals. Every verb mails somebody, and none of them is
	// idempotent, which is why they are separate methods rather than one
	// that takes a verb.
	ListApprovals(ctx context.Context, fileID string, o gapi.ListApprovalsOptions) (*gdrive.ApprovalList, error)
	StartApproval(ctx context.Context, fileID string, body *gdrive.StartApproval) (*gdrive.Approval, error)
	ApproveApproval(ctx context.Context, fileID, approvalID string, body *gdrive.ApprovalMessage) (*gdrive.Approval, error)
	DeclineApproval(ctx context.Context, fileID, approvalID string, body *gdrive.ApprovalMessage) (*gdrive.Approval, error)
	CancelApproval(ctx context.Context, fileID, approvalID string, body *gdrive.ApprovalMessage) (*gdrive.Approval, error)
	CommentApproval(ctx context.Context, fileID, approvalID string, body *gdrive.ApprovalMessage) (*gdrive.Approval, error)
	ReassignApproval(ctx context.Context, fileID, approvalID string, body *gdrive.ReassignApproval) (*gdrive.Approval, error)

	// Access requests. There is no create: only somebody who was refused
	// can ask.
	ListAccessProposals(ctx context.Context, fileID string) ([]*gdrive.AccessProposal, error)
	ResolveAccessProposal(ctx context.Context, fileID, proposalID string, body *gdrive.ResolveProposal) error

	// Permanent removal, registered only with GDRIVE_ENABLE_DESTRUCTIVE.
	DeleteFile(ctx context.Context, fileID string) error
	EmptyTrash(ctx context.Context, driveID string) error
}

// Options configure the service.
type Options struct {
	ReadOnly    bool
	Destructive bool
	Sharing     config.Sharing
	LocalDir    string
	MaxDownload int64
	Labels      bool
	Activity    bool
	Logger      *slog.Logger
	// PathTTL is how long a resolved (parent, name) pair is trusted, so
	// a burst of calls on one path costs one walk. Default 60s.
	PathTTL time.Duration
	// FileTTL coalesces repeated reads of one file. Default 5s.
	FileTTL time.Duration
	// ExportTTL is how long one exported document is kept so that a model
	// can page through it. It is longer than FileTTL because paging
	// happens across turns, and it is safe to be: the cache key carries
	// the revision and the modification time, so a document that changed
	// misses the entry rather than serving a stale one. Default 5m.
	ExportTTL time.Duration
	Now       func() time.Time
}

// Service is the orchestrator for one authenticated account.
type Service struct {
	api  API
	opts Options
	log  *slog.Logger
	now  func() time.Time

	mu sync.Mutex
	// paths maps "<parent>/<name>" to the id it resolved to.
	paths map[string]cached[string]
	// files caches whole file reads for a few seconds.
	files map[string]cached[*gdrive.File]
	// labelDefs caches the whole label definition listing under one key.
	// It is per account rather than per file: what a label means does not
	// change between two calls of one conversation, and naming the labels
	// on a card would otherwise cost a listing per card.
	labelDefs map[string]cached[map[string]*model.LabelDefinition]
	// drives caches the shared drive list, which changes rarely and is
	// needed to name a location.
	drivesAt time.Time
	drives   []*gdrive.Drive
	// root is My Drive's root folder id, which never changes. rootTried
	// records that the lookup happened, so a failure is not retried once
	// per file.
	root      string
	rootTried bool
	// imports is about.importFormats: what Google will convert each media
	// type into. It is static, so it is read once; importsTried records
	// the attempt so that a Drive which cannot answer is not asked again
	// per conversion.
	imports      map[string][]string
	importsTried bool
	// export holds the last document exported for a read. Drive takes no
	// byte range on an export, so without this every window of a long
	// document costs a full re-export: reading a 1 MB Doc a page at a
	// time was 53 exports and 28 MB on the wire to deliver 1 MB. One
	// entry is enough, because continuation is what makes the second
	// call happen and continuation is sequential. Google caps an export
	// at 10 MB, which bounds what it can hold.
	export exported
	// tools are the names the server registered, for get_account.
	tools []string
}

type cached[T any] struct {
	value T
	at    time.Time
}

// exported is one document's exported text, kept only long enough for a
// model to page through it.
type exported struct {
	key  string
	text string
	at   time.Time
}

// New builds a service.
func New(api API, o Options) *Service {
	s := &Service{api: api, opts: o, log: o.Logger, now: o.Now,
		paths: map[string]cached[string]{}, files: map[string]cached[*gdrive.File]{},
		labelDefs: map[string]cached[map[string]*model.LabelDefinition]{}}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.opts.PathTTL == 0 {
		s.opts.PathTTL = 60 * time.Second
	}
	if s.opts.FileTTL == 0 {
		s.opts.FileTTL = 5 * time.Second
	}
	if s.opts.ExportTTL == 0 {
		s.opts.ExportTTL = 5 * time.Minute
	}
	return s
}

// Options returns the configuration the service was built with.
func (s *Service) Options() Options { return s.opts }

// SetRegisteredTools records which tools the server registered, so
// get_account can report the surface that exists rather than describe it
// from a second copy of the gating rules.
func (s *Service) SetRegisteredTools(names []string) {
	s.mu.Lock()
	s.tools = append([]string(nil), names...)
	sort.Strings(s.tools)
	s.mu.Unlock()
}

// RegisteredTools returns the tool names the server registered.
func (s *Service) RegisteredTools() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.tools...)
}

// Error is an LLM-facing failure with a class the model can branch on
// and a message that says what to do next.
type Error struct {
	Class   string
	Message string
	Err     error
}

func (e *Error) Error() string { return "[" + e.Class + "] " + e.Message }

// Unwrap exposes the underlying error for errors.Is.
func (e *Error) Unwrap() error { return e.Err }

// Error classes. The vocabulary is declared in internal/gapi, which
// works out the class from Google's answer; these aliases exist so a
// service-level error reads in service terms without a second list that
// could drift from the first.
const (
	ClassAuth        = gapi.ClassAuth
	ClassForbidden   = gapi.ClassForbidden
	ClassNotFound    = gapi.ClassNotFound
	ClassAmbiguous   = gapi.ClassAmbiguous
	ClassExists      = gapi.ClassExists
	ClassInvalid     = gapi.ClassInvalid
	ClassUnsupported = gapi.ClassUnsupported
	ClassBlocked     = gapi.ClassBlocked
	ClassRateLimited = gapi.ClassRateLimited
	ClassServer      = gapi.ClassServer
	ClassNetwork     = gapi.ClassNetwork
	ClassAmbiguousIO = gapi.ClassAmbiguousIO
	ClassUnexpected  = gapi.ClassUnexpected
)

// Errorf builds an Error.
func Errorf(class, format string, args ...any) *Error {
	return &Error{Class: class, Message: fmt.Sprintf(format, args...)}
}

// wrap turns a client error into an LLM-facing one, keeping the class the
// client worked out and adding what the caller was trying to do.
func wrap(err error, what string) error {
	if err == nil {
		return nil
	}
	var already *Error
	if errors.As(err, &already) {
		return err
	}
	class := gapi.Class(err)
	msg := gapi.Message(err)
	if msg == "" {
		msg = err.Error()
	}
	switch class {
	case ClassAuth:
		if errors.Is(err, gapi.ErrNoCredentials) {
			return &Error{Class: ClassAuth, Message: "no Google account is signed in. Run `google-drive-mcp login`, then try again.", Err: err}
		}
		return &Error{Class: ClassAuth, Message: "Google refused this login: " + msg +
			". Run `google-drive-mcp login` again, then try once more.", Err: err}
	case ClassForbidden:
		if errors.Is(err, gapi.ErrMissingScope) {
			return &Error{Class: ClassForbidden, Message: "this login does not carry the scope that " + what + " needs: " + msg +
				". Run `google-drive-mcp login` again and approve every checkbox.", Err: err}
		}
		return &Error{Class: ClassForbidden, Message: "Google refused " + what + ": " + msg, Err: err}
	case ClassBlocked:
		return &Error{Class: ClassBlocked, Message: "your organisation's sharing policy does not allow this. Google said: " + msg, Err: err}
	case ClassRateLimited:
		// A daily quota and a burst are both "rate limited", and the
		// advice is opposite: one is worth trying again in a minute and
		// the other cannot succeed again today however long the wait.
		// Saying "the request was retried" of a daily quota would be
		// wrong twice over, because it is deliberately not retried.
		if gapi.IsDailyQuota(err) {
			return &Error{Class: ClassRateLimited, Message: "this Google Cloud project's daily quota for the " +
				"Drive API is spent, so " + what + " cannot succeed again until the quota resets. Backing off " +
				"does not help and this was not retried. Google said: " + msg, Err: err}
		}
		return &Error{Class: ClassRateLimited, Message: "Drive is rate-limiting this account; the request was retried and still refused. Wait a minute and try again. Google said: " + msg, Err: err}
	case ClassNetwork:
		return &Error{Class: ClassNetwork, Message: "could not reach Google while " + what + ": " + msg, Err: err}
	case ClassAmbiguousIO:
		return &Error{Class: ClassAmbiguousIO, Message: "the connection dropped while " + what + ", so it may or may not have happened. Check with get_file before trying again. " + msg, Err: err}
	}
	return &Error{Class: class, Message: what + " failed: " + msg, Err: err}
}

// cacheGet reads a cache entry that is still inside its time to live.
func cacheGet[T any](m map[string]cached[T], key string, now time.Time, ttl time.Duration) (T, bool) {
	var zero T
	e, ok := m[key]
	if !ok || now.Sub(e.at) > ttl {
		return zero, false
	}
	return e.value, true
}
