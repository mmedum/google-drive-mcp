package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/redact"
	"github.com/mmedum/google-drive-mcp/internal/version"
)

// statusSchemaVersion is the version of the JSON object `status --json`
// prints. A caller may branch on it; it changes only when a field is
// removed or its meaning changes, never when one is added.
const statusSchemaVersion = 1

// statusReport is everything `status` knows, collected once and then
// rendered either as the lines a person reads or as the object a script
// parses.
//
// One collector, two renderers, because the alternative drifts. A script
// that wants to know whether this server is authorized otherwise has to
// read the text, and the text is written for a person: the field that
// answers the question is a label with a value beside it, and a label is
// free to be reworded in any release. The object below is the part that
// is promised not to move.
//
// Nothing here contacts Google. It reports what is configured and where
// the token is, which is the question a launcher asks before starting the
// server; `doctor` is the one that asks Google whether the token works.
type statusReport struct {
	SchemaVersion int    `json:"schema_version"`
	Binary        string `json:"binary"`
	Version       string `json:"version"`
	Profile       string `json:"profile"`
	ConfigDir     string `json:"config_dir"`
	// Account is masked the same way the text line is, to the domain
	// only: null when no profile has been written yet.
	Account     *string           `json:"account"`
	Credentials statusCredentials `json:"credentials"`
	Scopes      statusScopes      `json:"scopes"`
	Settings    statusSettings    `json:"settings"`
}

// statusCredentials is the half a caller checks before starting the
// server.
type statusCredentials struct {
	// Resolved is the one field worth branching on: true means a refresh
	// token was found, false means every tool will answer [auth] until
	// `login` succeeds. It is always present, so an absent or unparseable
	// object is distinguishable from an unauthorized one — which a grep
	// for a label in the text output cannot do.
	Resolved bool `json:"resolved"`
	// TokenStore is where the token came from — "keyring", "file" or
	// "env" — and null when there is none.
	TokenStore *string `json:"token_store"`
	// Reason says why nothing resolved, and is null when something did.
	Reason              *string `json:"reason"`
	ClientSecretPath    string  `json:"client_secret_path"`
	ClientSecretPresent bool    `json:"client_secret_present"`
}

// statusScopes is what the last login was granted and what this
// configuration asks for; they differ after a settings change that widens
// the scopes, which is the commonest reason a working setup starts
// refusing one tool.
type statusScopes struct {
	Granted []string `json:"granted"`
	Wanted  []string `json:"wanted"`
}

// statusSettings is the configuration the text output already lists.
// Sizes are bytes and durations are Go duration strings, so a caller
// compares numbers rather than parsing "1.0 GiB".
type statusSettings struct {
	ReadOnly         bool    `json:"read_only"`
	Sharing          string  `json:"sharing"`
	Destructive      bool    `json:"destructive"`
	Labels           bool    `json:"labels"`
	LocalDir         *string `json:"local_dir"`
	MaxDownloadBytes int64   `json:"max_download_bytes"`
	HTTPTimeout      string  `json:"http_timeout"`
}

// newStatusReport collects the state without contacting Google.
func newStatusReport(p *profile) statusReport {
	cfg := p.cfg
	r := statusReport{
		SchemaVersion: statusSchemaVersion,
		Binary:        "google-drive-mcp",
		Version:       version.String(),
		Profile:       cfg.Profile,
		ConfigDir:     p.dir,
		Account:       orNil(redact.Account(p.user.AccountEmail)),
		Credentials: statusCredentials{
			ClientSecretPath: p.clientSecretPath,
		},
		Scopes: statusScopes{Granted: orEmpty(p.user.Scopes), Wanted: orEmpty(p.scopes())},
		Settings: statusSettings{
			ReadOnly:         cfg.ReadOnly,
			Sharing:          string(cfg.Sharing),
			Destructive:      cfg.EnableDestructive,
			Labels:           cfg.Labels,
			LocalDir:         orNil(cfg.LocalDir),
			MaxDownloadBytes: cfg.MaxDownload,
			HTTPTimeout:      cfg.HTTPTimeout.String(),
		},
	}
	if _, err := os.Stat(p.clientSecretPath); err == nil {
		r.Credentials.ClientSecretPresent = true
	}
	if _, src, err := p.store.Resolve(); err == nil {
		r.Credentials.Resolved = true
		r.Credentials.TokenStore = orNil(string(src))
	} else {
		// Through the redactor for the reason the package documents: this
		// error is formatted elsewhere and an address can arrive inside
		// one nothing here wrote. No error this path can currently return
		// carries one, which is why the text below is unchanged by it.
		r.Credentials.Reason = orNil(redact.Accounts(err.Error()))
	}
	return r
}

// writeText writes the human-readable form: the same lines, in the same
// order, that `status` has always printed.
func (r statusReport) writeText(w io.Writer) {
	_, _ = fmt.Fprintln(w, version.Info())
	_, _ = fmt.Fprintf(w, "profile:        %s\n", r.Profile)
	_, _ = fmt.Fprintf(w, "config dir:     %s\n", r.ConfigDir)
	_, _ = fmt.Fprintf(w, "account:        %s\n", orUnset(deref(r.Account)))
	exists := "missing"
	if r.Credentials.ClientSecretPresent {
		exists = "present"
	}
	_, _ = fmt.Fprintf(w, "client secret:  %s (%s)\n", r.Credentials.ClientSecretPath, exists)
	if r.Credentials.Resolved {
		_, _ = fmt.Fprintf(w, "token store:    %s\n", deref(r.Credentials.TokenStore))
	} else {
		_, _ = fmt.Fprintf(w, "token store:    none (%s)\n", deref(r.Credentials.Reason))
	}
	if len(r.Scopes.Granted) > 0 {
		_, _ = fmt.Fprintf(w, "scopes:         %s\n", strings.Join(r.Scopes.Granted, " "))
	}
	_, _ = fmt.Fprintf(w, "scopes wanted:  %s\n", strings.Join(r.Scopes.Wanted, " "))
	_, _ = fmt.Fprintf(w, "read-only:      %t\n", r.Settings.ReadOnly)
	_, _ = fmt.Fprintf(w, "sharing tools:  %s\n", r.Settings.Sharing)
	_, _ = fmt.Fprintf(w, "destructive:    %t\n", r.Settings.Destructive)
	_, _ = fmt.Fprintf(w, "labels:         %t\n", r.Settings.Labels)
	_, _ = fmt.Fprintf(w, "local dir:      %s\n", orUnset(deref(r.Settings.LocalDir)))
	_, _ = fmt.Fprintf(w, "max download:   %s\n", model.HumanSize(r.Settings.MaxDownloadBytes))
	_, _ = fmt.Fprintf(w, "http timeout:   %s\n", r.Settings.HTTPTimeout)
}

// writeJSON writes the object, indented and newline-terminated, so that
// the whole of stdout is one JSON value.
func (r statusReport) writeJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// Paths are not HTML and an escaped ampersand in one is a path a
	// caller cannot compare against its own.
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

// orNil turns an unset string into the JSON null that says so. An empty
// string would be a value, and a caller cannot tell a value it does not
// recognize from one that is not there.
func orNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// orEmpty keeps a list a list. A nil slice marshals as null, and a
// caller counting it has to guard for that before it can count.
func orEmpty(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
