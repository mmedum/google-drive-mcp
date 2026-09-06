package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestScopes(t *testing.T) {
	cases := []struct {
		readOnly, labels bool
		want             []string
	}{
		{false, false, []string{ScopeDrive}},
		{true, false, []string{ScopeDriveReadonly}},
		{false, true, []string{ScopeDrive, ScopeLabels}},
		{true, true, []string{ScopeDriveReadonly, ScopeLabelsReadonly}},
	}
	for _, c := range cases {
		got := Scopes(Access{ReadOnly: c.readOnly, Labels: c.labels})
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("Scopes(%t, %t) = %v, want %v", c.readOnly, c.labels, got, c.want)
		}
	}
	// drive.file is deliberately never requested: it reaches only files
	// the app created or the user picked, which a stdio server cannot show.
	for _, s := range Scopes(Access{Labels: true}) {
		if strings.HasSuffix(s, "/drive.file") {
			t.Error("drive.file must not be requested")
		}
	}
}

const desktopClientJSON = `{"installed":{"client_id":"test-client-id","client_secret":"test-secret",
"auth_uri":"https://accounts.example/auth","token_uri":"https://accounts.example/token"}}`

func TestParseClientSecret(t *testing.T) {
	cfg, err := ParseClientSecret([]byte(desktopClientJSON), Scopes(Access{}))
	if err != nil {
		t.Fatalf("ParseClientSecret: %v", err)
	}
	if cfg.ClientID != "test-client-id" || cfg.ClientSecret != "test-secret" {
		t.Errorf("client details lost: %+v", cfg)
	}
	if cfg.Endpoint.AuthURL != "https://accounts.example/auth" {
		t.Errorf("auth url = %q", cfg.Endpoint.AuthURL)
	}
	if len(cfg.Scopes) != 1 || cfg.Scopes[0] != ScopeDrive {
		t.Errorf("scopes = %v", cfg.Scopes)
	}
}

func TestParseClientSecretDefaultsGoogleEndpoints(t *testing.T) {
	cfg, err := ParseClientSecret([]byte(`{"installed":{"client_id":"x"}}`), nil)
	if err != nil {
		t.Fatalf("ParseClientSecret: %v", err)
	}
	if cfg.Endpoint.AuthURL != GoogleAuthURL || cfg.Endpoint.TokenURL != GoogleTokenURL {
		t.Errorf("endpoints not defaulted: %+v", cfg.Endpoint)
	}
}

func TestParseClientSecretRejectsWebClient(t *testing.T) {
	_, err := ParseClientSecret([]byte(`{"web":{"client_id":"x"}}`), nil)
	if !errors.Is(err, ErrNotDesktopClient) {
		t.Fatalf("err = %v, want ErrNotDesktopClient", err)
	}
	if !strings.Contains(err.Error(), "Desktop app client instead") {
		t.Errorf("the error should say what to do: %v", err)
	}
}

func TestParseClientSecretRejectsGarbage(t *testing.T) {
	if _, err := ParseClientSecret([]byte("not json"), nil); err == nil {
		t.Error("invalid JSON should be rejected")
	}
	if _, err := ParseClientSecret([]byte(`{"installed":{}}`), nil); err == nil {
		t.Error("a client without an id should be rejected")
	}
	if _, err := ParseClientSecret([]byte(`{}`), nil); !errors.Is(err, ErrNotDesktopClient) {
		t.Error("an empty object should be reported as not a desktop client")
	}
}

func TestLoadClientSecretMissingFile(t *testing.T) {
	if _, err := LoadClientSecret("/nonexistent/client_secret.json", nil); err == nil {
		t.Error("a missing file should be an error")
	}
}

// tokenServer stands in for Google's token endpoint.
func tokenServer(t *testing.T, refresh string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.Form.Get("code_verifier") == "" {
			t.Error("PKCE verifier missing from the exchange")
		}
		w.Header().Set("Content-Type", "application/json")
		body := map[string]any{"access_token": "access", "token_type": "Bearer", "expires_in": 3600}
		if refresh != "" {
			body["refresh_token"] = refresh
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
}

// runLogin drives the loopback flow: it starts Login, waits for the URL,
// and calls back to the listener the way a browser would.
func runLogin(t *testing.T, cfg *oauth2.Config, callback func(redirect string)) (*oauth2.Token, error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var buf strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return Login(ctx, cfg, LoginOptions{
		Out:      &buf,
		Listener: ln,
		Timeout:  5 * time.Second,
		OpenBrowser: func(authURL string) error {
			u, err := url.Parse(authURL)
			if err != nil {
				t.Errorf("bad auth url: %v", err)
				return nil
			}
			q := u.Query()
			if q.Get("code_challenge_method") != "S256" {
				t.Errorf("PKCE challenge missing: %v", q)
			}
			if q.Get("access_type") != "offline" {
				t.Errorf("offline access not requested: %v", q)
			}
			redirect := q.Get("redirect_uri")
			if !strings.HasPrefix(redirect, "http://127.0.0.1:") {
				t.Errorf("redirect is not loopback: %q", redirect)
			}
			go callback(redirect + "?state=" + url.QueryEscape(q.Get("state")))
			return nil
		},
	})
}

func TestLoginHappyPath(t *testing.T) {
	ts := tokenServer(t, "refresh-value")
	defer ts.Close()
	cfg := &oauth2.Config{ClientID: "id", ClientSecret: "secret",
		Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.example/auth", TokenURL: ts.URL, AuthStyle: oauth2.AuthStyleInParams}}

	tok, err := runLogin(t, cfg, func(u string) {
		resp, err := http.Get(u + "&code=the-code") //nolint:noctx // test callback
		if err == nil {
			_ = resp.Body.Close()
		}
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tok.RefreshToken != "refresh-value" {
		t.Errorf("refresh token = %q", tok.RefreshToken)
	}
}

func TestLoginRejectsAStateMismatch(t *testing.T) {
	ts := tokenServer(t, "refresh-value")
	defer ts.Close()
	cfg := &oauth2.Config{ClientID: "id", Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.example/auth", TokenURL: ts.URL}}
	_, err := runLogin(t, cfg, func(u string) {
		base, _, _ := strings.Cut(u, "?")
		resp, err := http.Get(base + "?state=wrong&code=the-code") //nolint:noctx // test callback
		if err == nil {
			_ = resp.Body.Close()
		}
	})
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("err = %v, want a state mismatch", err)
	}
}

func TestLoginReportsAnAuthorizationDenial(t *testing.T) {
	ts := tokenServer(t, "refresh-value")
	defer ts.Close()
	cfg := &oauth2.Config{ClientID: "id", Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.example/auth", TokenURL: ts.URL}}
	_, err := runLogin(t, cfg, func(u string) {
		resp, err := http.Get(u + "&error=access_denied") //nolint:noctx // test callback
		if err == nil {
			_ = resp.Body.Close()
		}
	})
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("err = %v, want the denial reported", err)
	}
}

func TestLoginRequiresARefreshToken(t *testing.T) {
	ts := tokenServer(t, "")
	defer ts.Close()
	cfg := &oauth2.Config{ClientID: "id", Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.example/auth", TokenURL: ts.URL}}
	_, err := runLogin(t, cfg, func(u string) {
		resp, err := http.Get(u + "&code=the-code") //nolint:noctx // test callback
		if err == nil {
			_ = resp.Body.Close()
		}
	})
	if err == nil || !strings.Contains(err.Error(), "no refresh token") {
		t.Fatalf("err = %v, want the missing-refresh-token advice", err)
	}
}

func TestLoginTimesOut(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	cfg := &oauth2.Config{ClientID: "id", Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.example/auth"}}
	_, err = Login(context.Background(), cfg, LoginOptions{
		Listener:    ln,
		Timeout:     50 * time.Millisecond,
		OpenBrowser: func(string) error { return errors.New("no browser here") },
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
}

func TestLoginHonoursContextCancellation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cfg := &oauth2.Config{ClientID: "id", Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.example/auth"}}
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err = Login(ctx, cfg, LoginOptions{Listener: ln, Timeout: 5 * time.Second,
		OpenBrowser: func(string) error { return nil }})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestRevoke(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.Form.Get("token")
	}))
	defer srv.Close()
	old := RevokeURL
	RevokeURL = srv.URL
	t.Cleanup(func() { RevokeURL = old })

	if err := Revoke(context.Background(), nil, "the-token"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if got != "the-token" {
		t.Errorf("server saw token %q", got)
	}
}

func TestRevokeReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "already revoked", http.StatusBadRequest)
	}))
	defer srv.Close()
	old := RevokeURL
	RevokeURL = srv.URL
	t.Cleanup(func() { RevokeURL = old })
	if err := Revoke(context.Background(), nil, "t"); err == nil {
		t.Fatal("a 400 should be reported")
	}
}

func TestInspect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"scope":"` + ScopeDrive + ` ` + ScopeLabels + `","email":"person@example.com","expires_in":"3599","aud":"aud-value"}`))
	}))
	defer srv.Close()
	old := TokenInfoURL
	TokenInfoURL = srv.URL
	t.Cleanup(func() { TokenInfoURL = old })

	info, err := Inspect(context.Background(), nil, "access")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Email != "person@example.com" || len(info.Scopes) != 2 {
		t.Errorf("info = %+v", info)
	}
	if info.ExpiresIn != 3599*time.Second {
		t.Errorf("expires in = %s", info.ExpiresIn)
	}
}

func TestInspectReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid_token", http.StatusBadRequest)
	}))
	defer srv.Close()
	old := TokenInfoURL
	TokenInfoURL = srv.URL
	t.Cleanup(func() { TokenInfoURL = old })
	if _, err := Inspect(context.Background(), nil, "access"); err == nil {
		t.Fatal("a 400 should be reported")
	}
}

func TestHasScopes(t *testing.T) {
	granted := []string{ScopeDrive}
	if missing := HasScopes(granted, []string{ScopeDrive}); len(missing) != 0 {
		t.Errorf("missing = %v, want none", missing)
	}
	missing := HasScopes(granted, []string{ScopeDrive, ScopeLabels})
	if len(missing) != 1 || missing[0] != ScopeLabels {
		t.Errorf("missing = %v, want the labels scope", missing)
	}
}

func TestTokenSourceUsesTheRefreshToken(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		seen = r.Form.Get("refresh_token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()
	cfg := &oauth2.Config{ClientID: "id", Endpoint: oauth2.Endpoint{TokenURL: srv.URL, AuthStyle: oauth2.AuthStyleInParams}}
	ts := TokenSource(context.Background(), cfg, "stored-refresh", DefaultHTTPTimeout)
	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "fresh" {
		t.Errorf("access token = %q", tok.AccessToken)
	}
	if seen != "stored-refresh" {
		t.Errorf("server saw refresh token %q", seen)
	}
	// A second call must be served from the cache, not a second exchange.
	seen = ""
	if _, err := ts.Token(); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if seen != "" {
		t.Error("a valid token was refreshed again instead of reused")
	}
}

// TestATokenRefreshCannotHangForever covers a claim docs/security.md
// makes and the code did not keep: that every attempt, token refresh and
// transfer chunk runs under a deadline.
//
// A refresh happens inside the oauth2 transport against the context
// captured when the source was built, not the one on the request being
// made, so internal/gapi's per-request timeout never reaches it. With
// http.DefaultClient — which has no timeout — a token endpoint that
// accepts a connection and never answers would hang the first tool call
// for the life of the process.
//
// The endpoint here is a raw listener that accepts and never writes,
// rather than an httptest.Server: Close on one of those blocks waiting
// for exactly the connection this test is deliberately leaving open.
func TestATokenRefreshCannotHangForever(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan struct{}, 1)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case accepted <- struct{}{}:
			default:
			}
			// Held open, answering nothing, until the test ends.
			defer func() { _ = conn.Close() }()
		}
	}()

	cfg := &oauth2.Config{
		ClientID: "client-id-fixture", ClientSecret: "client-secret-fixture",
		Endpoint: oauth2.Endpoint{TokenURL: "http://" + ln.Addr().String() + "/token"},
	}
	ts := TokenSource(context.Background(), cfg, "refresh-token-fixture", 200*time.Millisecond)

	done := make(chan error, 1)
	go func() {
		_, err := ts.Token()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a token came back from an endpoint that never answered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh hung: the token source has no deadline of its own")
	}
	select {
	case <-accepted:
	default:
		t.Error("the refresh never reached the endpoint, so the test proved nothing")
	}
}
