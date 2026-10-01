package gapi

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestAPermissionDenialDoesNotRepeatTheAccount: Google names the account in
// the message of a permission failure, and this server repeats that
// message verbatim into an error string that reaches a log, a terminal
// and a tool response. The domain stays, because it is what tells a
// person which account was refused.
func TestAPermissionDenialDoesNotRepeatTheAccount(t *testing.T) {
	body := []byte(`{"error":{"code":403,"status":"PERMISSION_DENIED",` +
		`"message":"The user someone.private@example.com does not have permission to access this file."}}`)
	e := parseAPIError(403, "GET", "/drive/v3/files/x", body)
	if strings.Contains(e.Error(), "someone.private@example.com") {
		t.Errorf("the address was repeated verbatim: %s", e.Error())
	}
	if !strings.Contains(e.Error(), "…@example.com") {
		t.Errorf("the domain should survive so the account is still identifiable: %s", e.Error())
	}
	// A message with no address in it is untouched.
	plain := parseAPIError(404, "GET", "/x", []byte(`{"error":{"message":"File not found."}}`))
	if plain.Message != "File not found." {
		t.Errorf("message = %q, want it unchanged", plain.Message)
	}

	// The body that is not an error envelope at all is the worse case:
	// it is kept verbatim as the message, so whatever Google sent goes
	// straight into the error. The first pass at this masked only the
	// parsed field and left this one.
	raw := parseAPIError(500, "GET", "/x", []byte("upstream refused someone.private@example.com"))
	if strings.Contains(raw.Error(), "someone.private@example.com") {
		t.Errorf("an unparsed body was kept verbatim: %s", raw.Error())
	}
	if !strings.Contains(raw.Error(), "…@example.com") {
		t.Errorf("the domain should survive: %s", raw.Error())
	}
}

// A failure is called ambiguous only when the write may have reached
// Google and is not repeated: a refusal or a failed dial sent nothing
// that was applied, a read or a repeatable write was retried instead.
func TestOnlyAWriteThatMayHaveLandedIsAmbiguous(t *testing.T) {
	server := &transientError{err: ErrServer}
	refused := &transientError{err: ErrRateLimited, refused: true}
	reset := fmt.Errorf("%w: %w", ErrNetwork, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")})
	dial := fmt.Errorf("%w: %w", ErrNetwork, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")})
	create := request{method: http.MethodPost}
	for _, tc := range []struct {
		name string
		r    request
		err  error
		want bool
	}{
		{"create, 5xx", create, server, true},
		{"create, reset", create, reset, true},
		{"patch, reset", request{method: http.MethodPatch}, reset, true},
		{"create, rate limited", create, refused, false},
		{"create, failed dial", create, dial, false},
		{"create with an id, 5xx", request{method: http.MethodPost, idempotent: true}, server, false},
		{"read, reset", request{method: http.MethodGet}, reset, false},
		{"create, refused outright", create, ErrForbidden, false},
	} {
		if got := errors.Is(ambiguous(tc.r, tc.err), ErrAmbiguous); got != tc.want {
			t.Errorf("%s: ambiguous = %v, want %v", tc.name, got, tc.want)
		}
	}
	if got := Class(ambiguous(create, server)); got != ClassAmbiguousIO {
		t.Errorf("an ambiguous server failure has class %q, want %q", got, ClassAmbiguousIO)
	}
}

// A failed token refresh mid-request must not quote the request URL,
// whose query holds the caller's search terms; and a refresh that failed
// on the network is a network failure, not a refused login.
func TestATokenFailureCarriesNoQuery(t *testing.T) {
	const u = "https://www.googleapis.com/drive/v3/files?q=fullText+contains+'canaryterm'"
	refused := &url.Error{Op: "Get", URL: u, Err: errors.New("oauth2: cannot fetch token: 400 Bad Request")}
	offline := &url.Error{Op: "Get", URL: u, Err: fmt.Errorf("oauth2: cannot fetch token: %w",
		&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("no route to host")})}
	for _, tc := range []struct {
		name  string
		err   error
		class string
	}{
		{"token refused", refused, ClassAuth},
		{"token endpoint unreachable", offline, ClassNetwork},
	} {
		got := wrapTransportError(tc.err)
		if c := Class(got); c != tc.class {
			t.Errorf("%s: class %q, want %q", tc.name, c, tc.class)
		}
		if strings.Contains(got.Error(), "canaryterm") || strings.Contains(Message(got), "canaryterm") {
			t.Errorf("%s: the search term reached the error: %v / %s", tc.name, got, Message(got))
		}
	}
}
