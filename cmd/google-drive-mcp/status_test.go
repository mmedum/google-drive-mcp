package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/config"
	"github.com/mmedum/google-drive-mcp/internal/credentials"
	"github.com/mmedum/google-drive-mcp/internal/userconfig"
	"github.com/mmedum/google-drive-mcp/internal/version"
)

// storedToken is a keyring that holds one token, for the case where a
// login has happened. A nil Store.Keyring is the other case, so no fake
// is needed for it.
type storedToken string

func (t storedToken) Get(_, _ string) (string, error) { return string(t), nil }
func (storedToken) Set(_, _, _ string) error          { return nil }
func (storedToken) Delete(_, _ string) error          { return nil }
func noEnv(string) string                             { return "" }
func envToken(tok string) func(string) string         { return func(string) string { return tok } }

// defaultConfig is the configuration a run with no flags and no
// GDRIVE_* variables produces, built the way the commands build it.
func defaultConfig(t *testing.T) config.Config {
	t.Helper()
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg, err := loadConfig(fs, nil)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	return cfg
}

// unauthorisedProfile is a profile nobody has logged in to: no token in
// the keyring, no token file, no environment override.
func unauthorisedProfile(t *testing.T) *profile {
	t.Helper()
	dir := t.TempDir()
	return &profile{
		cfg:              defaultConfig(t),
		dir:              dir,
		clientSecretPath: filepath.Join(dir, "client_secret.json"),
		store: &credentials.Store{
			Profile:  "default",
			FilePath: filepath.Join(dir, "token.json"),
			Env:      noEnv,
		},
	}
}

// The unauthorised case is the one the flag exists for. A script asks
// this question precisely when it does not know the answer, and the shape
// that only holds when everything is present is the shape that answers it
// with silence: an absent field reads as "not authorised", which is also
// how a renamed field reads.
func TestStatusJSONAnswersWhenThereIsNothingToReport(t *testing.T) {
	var buf bytes.Buffer
	if err := newStatusReport(unauthorisedProfile(t)).writeJSON(&buf); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}

	// Decoded into a map, not into the struct: unmarshalling into
	// statusReport would invent the zero value for a field the encoder
	// never wrote, so the test would pass with the fields missing.
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, buf.String())
	}
	for _, key := range []string{"schema_version", "binary", "version", "profile", "config_dir", "account", "credentials", "scopes", "settings"} {
		if _, ok := got[key]; !ok {
			t.Errorf("no %q in the unauthorised object; a caller cannot tell it apart from an old binary", key)
		}
	}
	if got["account"] != nil {
		t.Errorf("account = %v, want null when no login has happened", got["account"])
	}

	creds, ok := got["credentials"].(map[string]any)
	if !ok {
		t.Fatalf("credentials is %T, want an object", got["credentials"])
	}
	resolved, ok := creds["resolved"]
	if !ok {
		t.Fatal("no credentials.resolved; the one field worth branching on is absent exactly when it matters")
	}
	if resolved != false {
		t.Errorf("credentials.resolved = %v, want false with no token anywhere", resolved)
	}
	if creds["token_store"] != nil {
		t.Errorf("credentials.token_store = %v, want null", creds["token_store"])
	}
	reason, _ := creds["reason"].(string)
	if reason == "" {
		t.Error("credentials.reason is empty; an unauthorised answer has to say why")
	}
	if creds["client_secret_present"] != false {
		t.Errorf("credentials.client_secret_present = %v, want false", creds["client_secret_present"])
	}

	// A list stays a list. `.scopes.granted | length` must not have to
	// guard against null first.
	scopes, ok := got["scopes"].(map[string]any)
	if !ok {
		t.Fatalf("scopes is %T, want an object", got["scopes"])
	}
	if granted, ok := scopes["granted"].([]any); !ok || len(granted) != 0 {
		t.Errorf("scopes.granted = %#v, want an empty list", scopes["granted"])
	}
	if wanted, ok := scopes["wanted"].([]any); !ok || len(wanted) == 0 {
		t.Errorf("scopes.wanted = %#v, want the scopes this configuration asks for", scopes["wanted"])
	}
	// The settings are known whether or not anybody has logged in.
	if _, ok := got["settings"].(map[string]any)["max_download_bytes"]; !ok {
		t.Error("no settings.max_download_bytes in the unauthorised object")
	}
}

// Each token source names itself, because "where is the token" is the
// other half of the question and the three answers are not
// interchangeable: only the stored ones survive a restart, and only the
// stored ones `logout` can remove.
func TestStatusJSONNamesWhereTheTokenCameFrom(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(*profile)
		want  string
	}{
		{"keyring", func(p *profile) { p.store.Keyring = storedToken("refresh-token") }, "keyring"},
		{"env", func(p *profile) { p.store.Env = envToken("refresh-token") }, "env"},
		{"file", func(p *profile) { writeTokenFile(t, p.store.FilePath) }, "file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := unauthorisedProfile(t)
			tc.build(p)
			r := newStatusReport(p)
			if !r.Credentials.Resolved {
				t.Fatalf("credentials.resolved = false with a token in the %s", tc.name)
			}
			if got := deref(r.Credentials.TokenStore); got != tc.want {
				t.Errorf("credentials.token_store = %q, want %q", got, tc.want)
			}
			if r.Credentials.Reason != nil {
				t.Errorf("credentials.reason = %q, want null when a token resolved", deref(r.Credentials.Reason))
			}
		})
	}
}

// writeTokenFile puts a token where the plaintext fallback keeps one.
func writeTokenFile(t *testing.T, path string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"refresh_token": "refresh-token", "saved_at": time.Unix(0, 0)})
	if err != nil {
		t.Fatalf("marshal token file: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
}

// The address is masked to its domain in the object exactly as it is in
// the text: the JSON is another way to read the same state, not a way
// around the rule about what leaves this process.
func TestStatusJSONMasksTheAccount(t *testing.T) {
	p := unauthorisedProfile(t)
	p.user = userconfig.Config{
		AccountEmail: "somebody@example.com",
		Scopes:       []string{"https://www.googleapis.com/auth/drive"},
		UpdatedAt:    time.Unix(0, 0),
	}
	var buf bytes.Buffer
	if err := newStatusReport(p).writeJSON(&buf); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	if strings.Contains(buf.String(), "somebody@") {
		t.Errorf("the local part reached the output:\n%s", buf.String())
	}
	var got statusReport
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if want := "…@example.com"; deref(got.Account) != want {
		t.Errorf("account = %q, want %q", deref(got.Account), want)
	}
	if len(got.Scopes.Granted) != 1 {
		t.Errorf("scopes.granted = %v, want the one scope the login was granted", got.Scopes.Granted)
	}
}

// The flag is additive: the text form is what it was, to the byte. Held
// here rather than left to review, because the two renderers now share a
// collector and a change made for the object's sake would otherwise
// reach the lines a person reads without anybody noticing.
func TestStatusTextIsUnchanged(t *testing.T) {
	p := unauthorisedProfile(t)
	p.user = userconfig.Config{AccountEmail: "somebody@example.com", Scopes: []string{"https://www.googleapis.com/auth/drive"}}
	p.store.Keyring = storedToken("refresh-token")

	var buf bytes.Buffer
	printStatus(&buf, p)
	want := version.Info() + "\n" +
		"profile:        default\n" +
		"config dir:     " + p.dir + "\n" +
		"account:        …@example.com\n" +
		"client secret:  " + p.clientSecretPath + " (missing)\n" +
		"token store:    keyring\n" +
		"scopes:         https://www.googleapis.com/auth/drive\n" +
		"scopes wanted:  https://www.googleapis.com/auth/drive\n" +
		"read-only:      false\n" +
		"sharing tools:  all\n" +
		"destructive:    false\n" +
		"labels:         false\n" +
		"local dir:      (unset)\n" +
		"max download:   1.0 GiB\n" +
		"http timeout:   1m0s\n"
	if buf.String() != want {
		t.Errorf("status text changed.\n got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

// Without the flag, nothing about the output moves — including the
// scopes line, which is absent until a login has written one.
func TestStatusTextOmitsGrantedScopesBeforeALogin(t *testing.T) {
	var buf bytes.Buffer
	printStatus(&buf, unauthorisedProfile(t))
	if strings.Contains(buf.String(), "\nscopes:") {
		t.Errorf("a granted-scopes line appeared with nothing granted:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "scopes wanted:") {
		t.Errorf("no wanted-scopes line:\n%s", buf.String())
	}
}

// Driven through run(), so the flag is really reachable from the command
// line and stdout really carries the object and nothing else — a warning
// or a stray line ahead of it would make the whole stream unparseable.
func TestStatusJSONFlagIsTheWholeOfStdout(t *testing.T) {
	t.Setenv(userconfig.EnvDir, t.TempDir())
	// A profile no keyring on this machine has an entry for, so the run
	// is the unauthorised one wherever the tests happen to run.
	t.Setenv("GDRIVE_PROFILE", "status-json-gate")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"status", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("status --json exited %d: %s", code, stderr.String())
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON value: %v\n%s", err, stdout.String())
	}
	if got["profile"] != "status-json-gate" {
		t.Errorf("profile = %v, want the one the flag asked for", got["profile"])
	}
	creds, _ := got["credentials"].(map[string]any)
	if creds["resolved"] != false {
		t.Errorf("credentials.resolved = %v, want false for a profile with no login", creds["resolved"])
	}

	// And the same invocation without the flag is still the text form.
	stdout.Reset()
	if code := run([]string{"status"}, &stdout, &stderr); code != 0 {
		t.Fatalf("status exited %d: %s", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "google-drive-mcp ") {
		t.Errorf("status without the flag no longer starts with the version banner:\n%s", stdout.String())
	}
	if json.Valid(bytes.TrimSpace(stdout.Bytes())) {
		t.Errorf("status without the flag printed JSON:\n%s", stdout.String())
	}
}
