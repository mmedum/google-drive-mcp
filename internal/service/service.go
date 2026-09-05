// Package service orchestrates the work behind each tool: resolve a
// reference, decide what is allowed, call Drive, and hand internal/render
// a model to lay out. Every rule worth testing lives here, so the tools
// stay thin.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/config"
	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
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
	RememberResourceKey(id, key string)
}

// Options configure the service.
type Options struct {
	ReadOnly    bool
	Destructive bool
	Sharing     config.Sharing
	LocalDir    string
	MaxDownload int64
	Labels      bool
	Logger      *slog.Logger
	// PathTTL is how long a resolved (parent, name) pair is trusted, so
	// a burst of calls on one path costs one walk. Default 60s.
	PathTTL time.Duration
	// FileTTL coalesces repeated reads of one file. Default 5s.
	FileTTL time.Duration
	Now     func() time.Time
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
	// drives caches the shared drive list, which changes rarely and is
	// needed to name a location.
	drivesAt time.Time
	drives   []*gdrive.Drive
	// root is My Drive's root folder id, which never changes. rootTried
	// records that the lookup happened, so a failure is not retried once
	// per file.
	root      string
	rootTried bool
	// tools are the names the server registered, for get_account.
	tools []string
}

type cached[T any] struct {
	value T
	at    time.Time
}

// New builds a service.
func New(api API, o Options) *Service {
	s := &Service{api: api, opts: o, log: o.Logger, now: o.Now,
		paths: map[string]cached[string]{}, files: map[string]cached[*gdrive.File]{}}
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
