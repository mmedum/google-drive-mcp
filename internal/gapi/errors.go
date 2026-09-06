package gapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/oauth2"
)

// Sentinel error classes. Every error returned by the client wraps
// exactly one of them so callers can branch with errors.Is.
var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrMissingScope = errors.New("missing scope")
	ErrForbidden    = errors.New("forbidden")
	// ErrBlocked is the organisation's own policy refusing a share. It is
	// not a bug and not something the server can work around: the
	// Workspace admin's external-sharing setting is enforced by Drive.
	ErrBlocked     = errors.New("blocked by policy")
	ErrNotFound    = errors.New("not found")
	ErrExists      = errors.New("already exists")
	ErrRateLimited = errors.New("rate limited")
	ErrServer      = errors.New("server error")
	ErrInvalid     = errors.New("invalid request")
	// ErrUnsupported is Drive refusing something it structurally cannot
	// do, such as moving a My Drive folder into a shared drive.
	ErrUnsupported = errors.New("unsupported")
	ErrNetwork     = errors.New("network error")
	// ErrAmbiguous means a write may or may not have been applied.
	ErrAmbiguous  = errors.New("ambiguous outcome")
	ErrUnexpected = errors.New("unexpected response")
	// ErrNoCredentials is the token-source error when no login exists.
	ErrNoCredentials = errors.New("no credentials stored; run `google-drive-mcp login`")
)

// Google's documented error reasons that this server branches on.
//
// They are compared through sameReason, never with ==, because Google
// spells one condition two ways: the legacy `error.errors[].reason`
// envelope is camelCase and a `google.rpc.ErrorInfo` detail is
// UPPER_SNAKE_CASE. parseAPIError prefers the ErrorInfo detail, so an
// exact camelCase comparison would miss every reason that arrived the
// modern way — classifying a throttled read as a permission error, which
// is the failure this vocabulary exists to prevent.
const (
	reasonRateLimit               = "rateLimitExceeded"
	reasonUserRateLimit           = "userRateLimitExceeded"
	reasonSharingRateLimit        = "sharingRateLimitExceeded"
	reasonDailyLimit              = "dailyLimitExceeded"
	reasonDomainPolicy            = "domainPolicy"
	reasonInvalidSharing          = "invalidSharingRequest"
	reasonShareOutBlocked         = "shareOutNotPermittedForContent"
	reasonInsufficientPerm        = "insufficientFilePermissions"
	reasonScopeInsufficient       = "ACCESS_TOKEN_SCOPE_INSUFFICIENT"
	reasonFolderMove              = "teamDrivesFolderMoveInNotSupported"
	reasonDuplicate               = "duplicate"
	reasonAbuse                   = "cannotDownloadAbusiveFile"
	reasonStorageFull             = "storageQuotaExceeded"
	reasonActiveItemCreationLimit = "activeItemCreationLimitExceeded"
)

// NoCredentials is a token source that always fails with ErrNoCredentials
// so a server without a login still starts and answers every call with
// an actionable auth error.
type NoCredentials struct{ Reason error }

// Token implements oauth2.TokenSource.
func (n NoCredentials) Token() (*oauth2.Token, error) {
	if n.Reason != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoCredentials, n.Reason)
	}
	return nil, ErrNoCredentials
}

// APIError is a non-2xx response from Google, decoded from the standard
// error envelope.
type APIError struct {
	Status  int
	RPC     string // e.g. NOT_FOUND, PERMISSION_DENIED
	Reason  string // e.g. rateLimitExceeded, domainPolicy
	Message string
	Method  string
	Path    string
}

func (e *APIError) Error() string {
	reason := e.RPC
	if e.Reason != "" {
		reason = e.Reason
	}
	return fmt.Sprintf("google api: HTTP %d %s: %s [%s %s]", e.Status, reason, e.Message, e.Method, e.Path)
}

// Unwrap maps the response onto a sentinel class. The reasons come from
// the Drive API's handle-errors guide; the message is only consulted
// where Google sends no machine-readable reason.
func (e *APIError) Unwrap() error {
	msg := strings.ToLower(e.Message)
	switch {
	case e.Status == 401:
		return ErrUnauthorized
	case sameReason(e.Reason, reasonScopeInsufficient) || strings.Contains(msg, "insufficient authentication scopes"):
		return ErrMissingScope

	// A 403 is Google's answer to three unrelated things: too many
	// requests, a policy refusal, and a plain lack of rights.
	case e.Status == 403 && rateLimitedReason(e.Reason):
		return ErrRateLimited
	// A policy refusal is a 403. invalidSharingRequest is the reason to
	// be careful with: Google uses it for a genuine policy refusal AND
	// for a request that is simply malformed, and the second arrives as
	// a 400.
	//
	// Seen live 2026-09-06: sharing with an address that has no Google
	// account behind it answers 400 invalidSharingRequest with the
	// message "you must check the Notify people box to invite this
	// recipient". This mapping called that a policy refusal, so the
	// server told the caller that their organisation forbade it and that
	// no option here could work around it — when the truth was that the
	// request was fixable, by the caller, with notify: true. [blocked]
	// says give up; [invalid] says fix it and try again, which is what a
	// model needed to hear.
	case sameReason(e.Reason, reasonDomainPolicy) || sameReason(e.Reason, reasonShareOutBlocked):
		return ErrBlocked
	case e.Status == 403 && sameReason(e.Reason, reasonInvalidSharing):
		return ErrBlocked
	case e.Status == 403 && sameReason(e.Reason, reasonStorageFull):
		return ErrInvalid
	// Drive answers a structural refusal — moving a My Drive folder into
	// a shared drive — with a 403 as well, so the reason has to be read
	// before the status: "you may not" and "this cannot be done" lead a
	// model to different next steps.
	case sameReason(e.Reason, reasonFolderMove):
		return ErrUnsupported
	case e.Status == 403:
		return ErrForbidden

	case e.Status == 404:
		return ErrNotFound
	case e.Status == 416:
		// A byte range that starts past the end of the file.
		return ErrInvalid
	case e.Status == 409:
		return ErrExists
	case e.Status == 429:
		return ErrRateLimited
	// 501 is "this cannot be done", not "this went wrong", so it is
	// matched before the rest of the 5xx range rather than after it,
	// where it could never fire.
	case e.Status == 501:
		return ErrUnsupported
	case e.Status >= 500:
		return ErrServer

	case e.Status == 400 && sameReason(e.Reason, reasonDuplicate):
		return ErrExists
	case e.Status == 400:
		return ErrInvalid
	}
	return ErrUnexpected
}

// IsAbuse reports whether Drive refused a download because it flagged the
// file; the caller may retry with acknowledgeAbuse.
func IsAbuse(err error) bool {
	var e *APIError
	return errors.As(err, &e) && sameReason(e.Reason, reasonAbuse)
}

// IsNotOwner reports whether Drive refused because only the owner may do
// this, which is how My Drive trashing works.
func IsNotOwner(err error) bool {
	var e *APIError
	return errors.As(err, &e) && sameReason(e.Reason, reasonInsufficientPerm)
}

// IsDailyQuota reports whether Google refused because a daily quota is
// spent. It is worth telling apart from a burst: the advice is different
// and so is the wait.
func IsDailyQuota(err error) bool {
	var e *APIError
	return errors.As(err, &e) && sameReason(e.Reason, reasonDailyLimit)
}

// rateReasons are the 403 reasons that mean "too many requests", mapped
// to whether backing off can help. Three of them are a burst this
// account is being throttled for, and waiting is exactly the answer. The
// fourth is a daily project quota: no amount of backing off frees it
// before it resets, and retrying sends a model round a loop until the
// quota's own clock rolls over.
var rateReasons = map[string]bool{
	normalReason(reasonRateLimit):        true,
	normalReason(reasonUserRateLimit):    true,
	normalReason(reasonSharingRateLimit): true,
	normalReason(reasonDailyLimit):       false,
}

// isRateReason reports whether a reason means the request was throttled,
// and whether waiting and trying again can help.
func isRateReason(reason string) (rateLimited, backoffHelps bool) {
	helps, ok := rateReasons[normalReason(reason)]
	return ok, helps
}

// rateLimitedReason reports whether a reason means throttled at all,
// whatever the answer about waiting.
func rateLimitedReason(reason string) bool {
	rateLimited, _ := isRateReason(reason)
	return rateLimited
}

// sameReason compares a reason Google sent against one this package
// names, in whichever of the two spellings it arrived.
func sameReason(got, want string) bool {
	return got != "" && normalReason(got) == normalReason(want)
}

// normalReason folds the two spellings onto one key. Nothing displays
// this form: APIError.Reason keeps whatever Google actually sent, which
// is what a log line and a message have to carry.
func normalReason(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "_", ""))
}

// Reason returns Google's machine-readable reason, or "".
func Reason(err error) string {
	var e *APIError
	if errors.As(err, &e) {
		return e.Reason
	}
	return ""
}

// Message returns Google's own message for an API error, or "". It is
// attached to policy refusals so the person sees what their organisation
// actually said.
func Message(err error) string {
	var e *APIError
	if errors.As(err, &e) {
		return e.Message
	}
	return ""
}

// AuthError is a failure to obtain an access token (revoked or expired
// refresh token, bad client secret).
type AuthError struct {
	Code string
	Msg  string
	// Err is what actually went wrong, kept so callers can tell "no
	// login yet" from "Google rejected the login" with errors.Is.
	Err error
}

func (e *AuthError) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("google auth: %s: %s", e.Code, e.Msg)
	}
	return "google auth: " + e.Code
}

// Unwrap classifies every auth failure as unauthorized, and keeps the
// cause reachable. Returning only the class would make
// errors.Is(err, ErrNoCredentials) false for the one case that most
// needs to be told apart: no login at all.
func (e *AuthError) Unwrap() []error {
	if e.Err == nil {
		return []error{ErrUnauthorized}
	}
	return []error{ErrUnauthorized, e.Err}
}

type googleErrorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Errors  []struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"errors"`
		Details []struct {
			Type   string `json:"@type"`
			Reason string `json:"reason"`
		} `json:"details"`
	} `json:"error"`
}

func parseAPIError(status int, method, path string, body []byte) *APIError {
	e := &APIError{Status: status, Method: method, Path: path}
	var g googleErrorBody
	if err := json.Unmarshal(body, &g); err == nil && g.Error.Message != "" {
		e.Message = g.Error.Message
		e.RPC = g.Error.Status
		for _, d := range g.Error.Details {
			if d.Reason != "" {
				e.Reason = d.Reason
				break
			}
		}
		if e.Reason == "" && len(g.Error.Errors) > 0 {
			e.Reason = g.Error.Errors[0].Reason
		}
	} else {
		e.Message = strings.TrimSpace(string(body))
		if r := []rune(e.Message); len(r) > 300 {
			e.Message = string(r[:300]) + "…"
		}
		if e.Message == "" {
			e.Message = "empty error body"
		}
	}
	return e
}

// wrapTransportError turns errors from the oauth2 transport into typed
// auth errors, and everything else into ErrNetwork.
func wrapTransportError(err error) error {
	if errors.Is(err, ErrNoCredentials) {
		return &AuthError{Code: "no_credentials", Msg: "run `google-drive-mcp login`", Err: err}
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		code := re.ErrorCode
		if code == "" && re.Response != nil {
			code = fmt.Sprintf("HTTP %d", re.Response.StatusCode)
		}
		return &AuthError{Code: code, Msg: re.ErrorDescription}
	}
	if strings.Contains(err.Error(), "oauth2:") {
		return &AuthError{Code: "token", Msg: err.Error()}
	}
	return fmt.Errorf("%w: %w", ErrNetwork, err)
}

// The class names that go in front of an LLM-facing message, as
// `[class] message`. They are the vocabulary a tool result speaks, so
// they are declared once here and aliased by internal/service; a literal
// spelled out at a call site is how the two halves drift apart.
const (
	ClassAuth        = "auth"
	ClassForbidden   = "forbidden"
	ClassNotFound    = "not_found"
	ClassAmbiguous   = "ambiguous"
	ClassExists      = "exists"
	ClassInvalid     = "invalid"
	ClassUnsupported = "unsupported"
	ClassBlocked     = "blocked"
	ClassRateLimited = "rate_limited"
	ClassServer      = "server"
	ClassNetwork     = "network"
	// ClassAmbiguousIO is a write that may or may not have been applied.
	ClassAmbiguousIO = "ambiguous_outcome"
	ClassUnexpected  = "unexpected"
)

// Classes lists every class Class can return, so a test can check that
// the two halves of the vocabulary still line up.
func Classes() []string {
	return []string{ClassAuth, ClassForbidden, ClassNotFound, ClassAmbiguous, ClassExists,
		ClassInvalid, ClassUnsupported, ClassBlocked, ClassRateLimited, ClassServer,
		ClassNetwork, ClassAmbiguousIO, ClassUnexpected}
}

// Class returns the short class name that goes in front of an LLM-facing
// message, as `[class] message`.
func Class(err error) string {
	switch {
	case errors.Is(err, ErrMissingScope):
		return ClassForbidden
	case errors.Is(err, ErrUnauthorized):
		return ClassAuth
	case errors.Is(err, ErrBlocked):
		return ClassBlocked
	case errors.Is(err, ErrForbidden):
		return ClassForbidden
	case errors.Is(err, ErrNotFound):
		return ClassNotFound
	case errors.Is(err, ErrExists):
		return ClassExists
	case errors.Is(err, ErrRateLimited):
		return ClassRateLimited
	case errors.Is(err, ErrServer):
		return ClassServer
	case errors.Is(err, ErrUnsupported):
		return ClassUnsupported
	case errors.Is(err, ErrInvalid):
		return ClassInvalid
	case errors.Is(err, ErrAmbiguous):
		return ClassAmbiguousIO
	case errors.Is(err, ErrNetwork):
		return ClassNetwork
	}
	return ClassUnexpected
}
