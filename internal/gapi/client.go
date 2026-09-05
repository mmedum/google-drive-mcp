// Package gapi is a raw REST client for the Google Drive API v3.
// Requests are built by hand against our own wire types in
// internal/gdrive: the generated client would pull in gRPC,
// OpenTelemetry and the cloud auth stack for a binary that needs only
// JSON and streaming HTTP. No MCP imports live here.
package gapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/time/rate"
)

// Default endpoints.
const (
	DefaultBaseURL = "https://www.googleapis.com/drive/v3"
)

// RetryPolicy bounds retries for transient failures.
type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// DefaultRetry is exponential backoff with jitter, capped at 30 seconds,
// which is what Drive's error guide asks for.
func DefaultRetry() RetryPolicy {
	return RetryPolicy{MaxAttempts: 5, BaseDelay: 500 * time.Millisecond, MaxDelay: 30 * time.Second}
}

// Options configure a Client. Zero values are production defaults.
type Options struct {
	// BaseTransport sits under the OAuth transport. nil uses http.DefaultTransport.
	BaseTransport http.RoundTripper
	BaseURL       string
	Logger        *slog.Logger
	// Timeout applies per attempt and per transfer chunk.
	Timeout time.Duration
	Retry   RetryPolicy
	// Limiters keep the process inside Drive's per-user unit budget. A
	// list costs 100 units against a read's 5, so listings are what the
	// read limiter is really protecting.
	ReadLimiter    *rate.Limiter
	WriteLimiter   *rate.Limiter
	SharingLimiter *rate.Limiter
	UserAgent      string
	// Sleep is replaced in tests.
	Sleep func(context.Context, time.Duration) error
	// AllowURL decides which URLs receive credentials; nil means HTTPS to
	// Google's API and content hosts only. Tests point it at their server.
	AllowURL func(u *url.URL) bool
}

// Client talks to Drive with one user's credentials.
type Client struct {
	httpc      *http.Client
	base       string
	log        *slog.Logger
	timeout    time.Duration
	retry      RetryPolicy
	readLim    *rate.Limiter
	writeLim   *rate.Limiter
	sharingLim *rate.Limiter
	ua         string
	sleep      func(context.Context, time.Duration) error
	allowURL   func(*url.URL) bool

	// Resource keys seen in URLs or responses. A link-shared file under
	// the 2021 security update needs its key on every later call, and
	// only the process that saw the key knows it.
	keysMu sync.RWMutex
	keys   map[string]string
}

// New builds a client whose requests carry tokens from ts.
func New(ts oauth2.TokenSource, o Options) *Client {
	base := o.BaseTransport
	if base == nil {
		base = http.DefaultTransport
	}
	c := &Client{
		httpc:      &http.Client{Transport: &oauth2.Transport{Source: ts, Base: base}},
		base:       strings.TrimRight(o.BaseURL, "/"),
		log:        o.Logger,
		timeout:    o.Timeout,
		retry:      o.Retry,
		readLim:    o.ReadLimiter,
		writeLim:   o.WriteLimiter,
		sharingLim: o.SharingLimiter,
		ua:         o.UserAgent,
		sleep:      o.Sleep,
		allowURL:   o.AllowURL,
		keys:       map[string]string{},
	}
	if c.allowURL == nil {
		c.allowURL = func(u *url.URL) bool { return u.Scheme == "https" && googleHost(u.Host) }
	}
	if c.base == "" {
		c.base = DefaultBaseURL
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.timeout <= 0 {
		c.timeout = 60 * time.Second
	}
	if c.retry.MaxAttempts <= 0 {
		c.retry = DefaultRetry()
	}
	if c.readLim == nil {
		c.readLim = rate.NewLimiter(10, 20)
	}
	if c.writeLim == nil {
		c.writeLim = rate.NewLimiter(5, 10)
	}
	if c.sharingLim == nil {
		// Sharing has a quota of its own (sharingRateLimitExceeded), and
		// it is much tighter than the general write budget.
		c.sharingLim = rate.NewLimiter(1, 3)
	}
	if c.ua == "" {
		c.ua = "google-drive-mcp"
	}
	if c.sleep == nil {
		c.sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	return c
}

// googleHost reports whether credentials may be sent to this host. Only
// Google's API and content hosts are on the list; a download URI that
// files.download hands back lives on googleusercontent.com.
//
// A host with an explicit port is refused rather than stripped of it.
// Google's endpoints do not use one, and this check decides whether an
// access token goes out — some of it against URLs that arrived in a
// response body, such as a revision's export link. Where such a check is
// going to be wrong, it should be wrong in the direction of refusing.
func googleHost(host string) bool {
	if strings.ContainsRune(host, ':') {
		return false
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	switch host {
	case "www.googleapis.com", "googleapis.com", "oauth2.googleapis.com", "accounts.google.com", "drive.google.com":
		return true
	}
	return strings.HasSuffix(host, ".googleapis.com") || strings.HasSuffix(host, ".googleusercontent.com")
}

// RememberResourceKey records a resource key for a file id so later calls
// for that id carry the X-Goog-Drive-Resource-Keys header.
func (c *Client) RememberResourceKey(id, key string) {
	if id == "" || key == "" {
		return
	}
	c.keysMu.Lock()
	c.keys[id] = key
	c.keysMu.Unlock()
}

// ResourceKey returns a remembered key for an id, if any.
func (c *Client) ResourceKey(id string) string {
	c.keysMu.RLock()
	defer c.keysMu.RUnlock()
	return c.keys[id]
}

// resourceKeyHeader builds the header value for the ids given, in a
// stable order so a request is reproducible.
func (c *Client) resourceKeyHeader(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	c.keysMu.RLock()
	defer c.keysMu.RUnlock()
	seen := make(map[string]bool, len(ids))
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if k := c.keys[id]; k != "" {
			parts = append(parts, id+"/"+k)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

type reqKind int

const (
	kindRead reqKind = iota
	kindWrite
	kindSharing
)

// request is one logical call to Drive.
type request struct {
	kind   reqKind
	method string
	url    string
	body   []byte
	// contentType overrides application/json for the request body.
	contentType string
	accept      string
	// resourceIDs are file ids whose remembered resource keys must ride
	// along on this call.
	resourceIDs []string
	header      http.Header
	// accepted reports whether a status is a success for this request.
	// nil means 2xx: a resumable upload's 308 is progress, not a failure,
	// and it is the only caller that needs to say so.
	accepted func(int) bool
}

// ok reports whether a response status is a success for this request.
func (r request) ok(status int) bool {
	if r.accepted != nil {
		return r.accepted(status)
	}
	return status >= 200 && status < 300
}

// do performs one logical request with rate limiting and retries and
// returns the response body.
func (c *Client) do(ctx context.Context, r request) ([]byte, error) {
	resp, err := c.doResponse(ctx, r)
	if err != nil {
		return nil, err
	}
	return resp.body, nil
}

func (c *Client) limiter(k reqKind) *rate.Limiter {
	switch k {
	case kindWrite:
		return c.writeLim
	case kindSharing:
		return c.sharingLim
	default:
		return c.readLim
	}
}

func (c *Client) doResponse(ctx context.Context, r request) (*attemptResult, error) {
	return attempts(c, ctx, r, "drive api", c.once, func(res *attemptResult) []any {
		return []any{"status", res.status, "bytes", len(res.body), "ms", res.elapsed.Milliseconds()}
	})
}

// attempts is the retry loop both the metadata path and the transfer
// path run: take a token, try once, and on a transient failure back off
// and try again. It is written once because the policy it encodes — what
// may be retried, how long to wait, how many times — has to be one
// policy, and two copies of a loop are two policies waiting to diverge.
func attempts[T any](c *Client, ctx context.Context, r request, event string,
	once func(context.Context, request) (T, error), fields func(T) []any,
) (T, error) {
	var zero T
	limiter := c.limiter(r.kind)
	path := redactPath(r.url)
	var lastErr error
	for attempt := 1; attempt <= c.retry.MaxAttempts; attempt++ {
		// Every attempt takes a token, retries included: a retry is
		// triggered by exactly the rate limiting the limiter is there to
		// stay under, so exempting them would push hardest at the worst
		// possible moment.
		if err := limiter.Wait(ctx); err != nil {
			return zero, err
		}
		res, err := once(ctx, r)
		if err == nil {
			c.log.DebugContext(ctx, event, append([]any{"method", r.method, "path", path,
				"attempt", attempt}, fields(res)...)...)
			return res, nil
		}
		lastErr = err
		retry, after := retryable(r.kind, err)
		c.log.DebugContext(ctx, event+" error", "method", r.method, "path", path,
			"attempt", attempt, "class", Class(err), "reason", Reason(err), "retry", retry && attempt < c.retry.MaxAttempts)
		if !retry || attempt == c.retry.MaxAttempts {
			break
		}
		if err := c.sleep(ctx, c.backoff(attempt, after)); err != nil {
			return zero, err
		}
	}
	return zero, lastErr
}

// classify turns a non-2xx response into the error the caller sees, and
// decides whether it may be tried again. Both request paths share it:
// a new rate-limit reason must not have to be added twice.
func classify(status int, header http.Header, method, path string, body []byte) error {
	apiErr := parseAPIError(status, method, path, body)
	if status == 429 || status >= 500 || (status == 403 && isRateReason(apiErr.Reason)) {
		return &transientError{err: apiErr, after: parseRetryAfter(header.Get("Retry-After"))}
	}
	return apiErr
}

type attemptResult struct {
	status  int
	body    []byte
	header  http.Header
	elapsed time.Duration
}

// transientError carries a Retry-After hint alongside an APIError.
type transientError struct {
	err   error
	after time.Duration
}

func (t *transientError) Error() string { return t.err.Error() }
func (t *transientError) Unwrap() error { return t.err }

// maxBodyBytes bounds a JSON response. Metadata answers are small; the
// streaming paths do not come through here.
const maxBodyBytes = 32 << 20

func (c *Client) once(ctx context.Context, r request) (*attemptResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := c.newRequest(ctx, r)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	resp, err := c.httpc.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || (errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil) {
			return nil, err
		}
		return nil, wrapTransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %w", ErrNetwork, err)
	}
	res := &attemptResult{status: resp.StatusCode, body: data, header: resp.Header, elapsed: time.Since(start)}
	if r.ok(resp.StatusCode) {
		return res, nil
	}
	return nil, classify(resp.StatusCode, resp.Header, r.method, redactPath(r.url), data)
}

// newRequest builds one HTTP request, checks the host allowlist before
// any credential can be attached, and adds the resource keys the call
// needs.
func (c *Client) newRequest(ctx context.Context, r request) (*http.Request, error) {
	var rdr io.Reader
	if r.body != nil {
		rdr = bytes.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, r.url, rdr)
	if err != nil {
		return nil, err
	}
	if !c.allowURL(req.URL) {
		return nil, fmt.Errorf("%w: refusing to send credentials to %s", ErrUnexpected, req.URL.Host)
	}
	for k, vs := range r.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	accept := r.accept
	if accept == "" {
		accept = "application/json"
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", c.ua)
	if r.body != nil {
		ct := r.contentType
		if ct == "" {
			ct = "application/json"
		}
		req.Header.Set("Content-Type", ct)
	}
	if h := c.resourceKeyHeader(r.resourceIDs); h != "" {
		req.Header.Set("X-Goog-Drive-Resource-Keys", h)
	}
	return req, nil
}

func isRateReason(reason string) bool {
	switch reason {
	case reasonRateLimit, reasonUserRateLimit, reasonSharingRateLimit:
		return true
	}
	return false
}

// retryable decides whether an attempt may be repeated.
//
// Reads retry on any transient failure. A write retries only when Google
// answered, because an answer proves the request reached Drive and was
// refused; a network failure on a write is reported as ambiguous instead,
// unless the caller made the write idempotent with a pre-generated id.
func retryable(k reqKind, err error) (bool, time.Duration) {
	var te *transientError
	if errors.As(err, &te) {
		return true, te.after
	}
	if errors.Is(err, ErrNetwork) {
		return k == kindRead, 0
	}
	return false, 0
}

func (c *Client) backoff(attempt int, after time.Duration) time.Duration {
	if after > 0 {
		return min(after, c.retry.MaxDelay)
	}
	d := c.retry.BaseDelay << (attempt - 1)
	if d > c.retry.MaxDelay || d <= 0 {
		d = c.retry.MaxDelay
	}
	// Jitter across the window with a quarter of it as a floor, so a
	// burst of retries does not all land at once and the result still
	// never exceeds the cap the policy documents.
	floor := int64(d) / 4
	return time.Duration(floor + rand.Int64N(int64(d)-floor)) //nolint:gosec // jitter, not security
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

var idInPath = regexp.MustCompile(`/(files|drives|permissions|revisions|comments|replies)/([^/?]+)`)

// ShortID shortens a file id for logs. Logs carry truncated ids, counts
// and latencies; never names, paths, emails, queries or content.
func ShortID(id string) string {
	if len(id) > 6 {
		return id[:6] + "…"
	}
	return id
}

// redactPath shortens ids in URLs before they reach logs, and drops the
// query string entirely because it carries search terms and field lists.
func redactPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "?"
	}
	return idInPath.ReplaceAllStringFunc(u.Path, func(m string) string {
		parts := idInPath.FindStringSubmatch(m)
		return "/" + parts[1] + "/" + ShortID(parts[2])
	})
}

// stream is a response whose body the caller reads. Transfers do not go
// through do: a download is bounded by the file's size, not by a fixed
// deadline, and holding it in memory would defeat the point.
type stream struct {
	status int
	header http.Header
	body   io.ReadCloser
}

// doStream performs a request with the same limiting and retries as do,
// but hands the response body back unread. Retries happen only before
// the body is handed over: once the caller has bytes, a failure is the
// caller's to see.
func (c *Client) doStream(ctx context.Context, r request) (*stream, error) {
	return attempts(c, ctx, r, "drive transfer", c.onceStream, func(s *stream) []any {
		return []any{"status", s.status}
	})
}

// onceStream sends one request and returns the live body on success.
//
// The per-attempt deadline covers the response headers and then stands
// down: a 1 GiB download cannot finish inside the timeout that bounds a
// metadata call, and cutting it off there would make every large
// transfer fail. What replaces it is a stall guard on the body — each
// individual Read must make progress within the same timeout — so a
// connection that stops sending is still cut, and one that is merely
// slow is not.
func (c *Client) onceStream(ctx context.Context, r request) (*stream, error) {
	ctx, cancel := context.WithCancel(ctx)
	headers := time.AfterFunc(c.timeout, cancel)
	req, err := c.newRequest(ctx, r)
	if err != nil {
		headers.Stop()
		cancel()
		return nil, err
	}
	resp, err := c.httpc.Do(req)
	headers.Stop()
	if err != nil {
		cancel()
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			// The guard fired: no headers inside the deadline.
			return nil, fmt.Errorf("%w: no response within %s", ErrNetwork, c.timeout)
		}
		return nil, wrapTransportError(err)
	}
	if !r.ok(resp.StatusCode) {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		_ = resp.Body.Close()
		cancel()
		return nil, classify(resp.StatusCode, resp.Header, r.method, redactPath(r.url), data)
	}
	return &stream{status: resp.StatusCode, header: resp.Header,
		body: newStallGuard(resp.Body, c.timeout, cancel)}, nil
}

// maxErrorBodyBytes bounds the error body read from a failed transfer.
// Google's error envelope is a few hundred bytes; anything larger is not
// an error message.
const maxErrorBodyBytes = 1 << 20

// stallGuard cancels a transfer whose next Read makes no progress inside
// the timeout. The clock runs only while a Read is outstanding, so a
// caller that is slow to ask for more bytes is never the one cut off.
type stallGuard struct {
	rc     io.ReadCloser
	timer  *time.Timer
	limit  time.Duration
	cancel context.CancelFunc
}

func newStallGuard(rc io.ReadCloser, limit time.Duration, cancel context.CancelFunc) *stallGuard {
	t := time.AfterFunc(limit, cancel)
	t.Stop()
	return &stallGuard{rc: rc, timer: t, limit: limit, cancel: cancel}
}

func (g *stallGuard) Read(p []byte) (int, error) {
	g.timer.Reset(g.limit)
	n, err := g.rc.Read(p)
	g.timer.Stop()
	return n, err
}

func (g *stallGuard) Close() error {
	g.timer.Stop()
	err := g.rc.Close()
	g.cancel()
	return err
}
