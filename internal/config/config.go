// Package config loads and validates runtime configuration.
//
// Environment variables (GDRIVE_*) are the source of truth because every
// MCP client (Claude Code, Claude Desktop, Cursor) passes only command,
// args and env to a stdio server. Each setting also has a flag bound to
// the same name; a flag given explicitly overrides the environment.
// Validation runs once at start so a misconfigured server fails before
// it announces itself.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/v3/internal/credentials"
	"github.com/mmedum/google-drive-mcp/v3/internal/userconfig"
)

// EnvPrefix is prepended to every environment variable name.
const EnvPrefix = "GDRIVE_"

// LogLevel is a typed enum constrained at load time.
type LogLevel string

// Allowed LogLevel values.
const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// Slog returns the slog.Level for this level.
func (l LogLevel) Slog() slog.Level {
	switch l {
	case LogDebug:
		return slog.LevelDebug
	case LogWarn:
		return slog.LevelWarn
	case LogError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LogFormat is a typed enum constrained at load time.
type LogFormat string

// Allowed LogFormat values.
const (
	LogText LogFormat = "text"
	LogJSON LogFormat = "json"
)

// Sharing says which sharing tools the deployer allows. Google's own
// organization policy decides what may actually be shared; this only
// decides whether the agent is offered the action at all.
type Sharing string

// Allowed Sharing values.
const (
	// SharingAll registers the sharing tools. An `anyone` link still
	// needs allow_anyone: true on the individual call.
	SharingAll Sharing = "all"
	// SharingOff leaves share_file, unshare_file and shared-drive
	// membership changes unregistered. list_permissions stays.
	SharingOff Sharing = "off"
)

// Config is the validated runtime configuration.
type Config struct {
	Profile           string
	LogLevel          LogLevel
	LogFormat         LogFormat
	ReadOnly          bool
	EnableDestructive bool
	Sharing           Sharing
	// LocalDir is the one directory downloads land in and uploads read
	// from. Empty means no file transfer at all.
	LocalDir string
	// MaxDownload caps a single download in bytes.
	MaxDownload int64
	// HTTPTimeout applies per attempt and per transfer chunk.
	HTTPTimeout time.Duration
	Labels      bool
	// Activity turns on list_activity, which reads the separate Drive
	// Activity API and asks for its scope at login.
	Activity         bool
	ClientSecretPath string
}

// Settings holds the raw string values before validation. Flags and the
// environment both feed it; Build turns it into a Config.
type Settings struct {
	Profile           string
	LogLevel          string
	LogFormat         string
	ReadOnly          string
	EnableDestructive string
	Sharing           string
	LocalDir          string
	MaxDownload       string
	HTTPTimeout       string
	Labels            string
	Activity          string
	ClientSecretPath  string
	// removed are the settings a release took out, as the environment
	// gives them, read only to refuse one still set.
	removed map[string]string
	// vars is every variable Define reads, in order.
	vars []Var
}

// Var is one variable the server reads.
type Var struct {
	// Name is the environment variable.
	Name string
	// Switch marks a setting that is on or off.
	Switch bool
}

// envOnly is every variable read outside Define, for Vars: the profile
// directory, which internal/userconfig reads before any flag is parsed,
// and the refresh token, which internal/credentials reads.
var envOnly = []string{userconfig.EnvDir, credentials.EnvVar}

// removedSettings are the settings a release took out, each with what to
// do instead. Off is still accepted. On, or a value that is not a
// boolean, is refused at start: ignored, it would quietly allow what its
// owner set it to stop.
var removedSettings = []struct{ key, instead string }{
	// 3.0.0: the server no longer asks the person before a write, so
	// there is nothing left to require of a client that cannot ask.
	{"REQUIRE_PROMPT", "leave " + EnvPrefix + "ENABLE_DESTRUCTIVE off for a client that runs with nobody watching, " +
		"and list the writes you want to approve under your client's ask rules, as the README shows"},
}

// removedSet refuses each removed setting that is set to anything but
// off. The value is not repeated: it is the operator's, and the refusal
// says what to do whatever it was.
func (s *Settings) removedSet() []error {
	var errs []error
	for _, r := range removedSettings {
		if on, err := parseBool(r.key, s.removed[r.key]); err == nil && !on {
			continue
		}
		errs = append(errs, fmt.Errorf("%w: %s%s was removed: the server no longer asks the person before a "+
			"write, and the client's own approval decides each call. Unset it, and %s", ErrInvalid, EnvPrefix,
			r.key, r.instead))
	}
	return errs
}

// Define registers one flag per setting on fs. Each flag defaults to the
// matching GDRIVE_* variable (read through env) so that a flag passed on
// the command line wins over the environment, and the environment wins
// over the built-in default.
//
// Every variable is read through read, which records it for Vars, so a
// setting added here reaches the bundle and staleness gates.
func Define(fs *flag.FlagSet, env func(string) string) *Settings {
	s := &Settings{removed: map[string]string{}}
	read := func(name string, isSwitch bool) string {
		s.vars = append(s.vars, Var{Name: name, Switch: isSwitch})
		return env(name)
	}
	for _, r := range removedSettings {
		s.removed[r.key] = read(EnvPrefix+r.key, false)
	}
	bind := func(p *string, name, key, fallback, usage string, isSwitch bool) {
		v := read(EnvPrefix+key, isSwitch)
		if v == "" {
			v = fallback
		}
		fs.StringVar(p, name, v, usage+" [env "+EnvPrefix+key+"]")
	}
	def := func(p *string, name, key, fallback, usage string) { bind(p, name, key, fallback, usage, false) }
	// A switch is off unless set.
	sw := func(p *string, name, key, usage string) { bind(p, name, key, "false", usage, true) }
	def(&s.Profile, "profile", "PROFILE", "default", "named configuration profile")
	def(&s.LogLevel, "log-level", "LOG_LEVEL", string(LogInfo), "log level: debug, info, warn, error")
	def(&s.LogFormat, "log-format", "LOG_FORMAT", string(LogText), "log format: text, json")
	sw(&s.ReadOnly, "read-only", "READ_ONLY", "register only read tools and request read-only scopes")
	sw(&s.EnableDestructive, "enable-destructive", "ENABLE_DESTRUCTIVE", "register the tools that destroy without a way back (permanent delete, empty trash)")
	def(&s.Sharing, "sharing", "SHARING", string(SharingAll), "sharing tools: all, off")
	def(&s.LocalDir, "local-dir", "LOCAL_DIR", "", "the one directory downloads are written to and uploads are read from (unset disables file transfer)")
	def(&s.MaxDownload, "max-download", "MAX_DOWNLOAD", "1GiB", "largest single download, e.g. 500MB or 2GiB")
	def(&s.HTTPTimeout, "http-timeout", "HTTP_TIMEOUT", "60s", "per-attempt and per-chunk timeout for Google API calls")
	sw(&s.Labels, "labels", "LABELS", "enable Workspace labels (adds the Drive Labels API scopes at login)")
	sw(&s.Activity, "activity", "ACTIVITY", "enable list_activity (adds the Drive Activity API scope at login)")
	def(&s.ClientSecretPath, "client-secret", "CLIENT_SECRET", "", "path to the OAuth Desktop client JSON (overrides the stored profile setting)")
	return s
}

// Vars is every variable the server reads: Define's, in the order it
// reads them, the removed ones included, then the ones read outside it.
// Define's are recorded rather than listed, so they cannot fall behind.
// The bundle gate derives what it asks for from these.
func Vars() []Var {
	fs := flag.NewFlagSet("vars", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	vars := Define(fs, func(string) string { return "" }).vars
	for _, name := range envOnly {
		vars = append(vars, Var{Name: name})
	}
	return vars
}

// EnvVars is the names of Vars, sorted.
func EnvVars() []string {
	vars := Vars()
	names := make([]string, 0, len(vars))
	for _, v := range vars {
		names = append(names, v.Name)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// SharingModes are the GDRIVE_SHARING values Build accepts, which the
// bundle's sharing field lists.
var SharingModes = []Sharing{SharingAll, SharingOff}

// ProfileRule is profilePattern in words, which Build's refusal and the
// bundle's profile field both give.
const ProfileRule = "up to 64 characters from a to z, 0 to 9, - and _, starting with a letter or digit"

var (
	profilePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	logLevels      = map[LogLevel]bool{LogDebug: true, LogInfo: true, LogWarn: true, LogError: true}
	logFormats     = map[LogFormat]bool{LogText: true, LogJSON: true}
)

// asciiLower lowers A to Z alone, so a character Unicode folds into a
// to z, such as the Kelvin sign, is refused rather than renamed.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("config: invalid")

// Build validates the settings and returns a Config.
func (s *Settings) Build() (Config, error) {
	var c Config
	var errs []error

	c.Profile = asciiLower(strings.TrimSpace(s.Profile))
	if !profilePattern.MatchString(c.Profile) {
		errs = append(errs, fmt.Errorf("%w: profile %q must be %s", ErrInvalid, s.Profile, ProfileRule))
	}

	c.LogLevel = LogLevel(strings.ToLower(strings.TrimSpace(s.LogLevel)))
	if !logLevels[c.LogLevel] {
		errs = append(errs, fmt.Errorf("%w: log level %q (want debug, info, warn, error)", ErrInvalid, s.LogLevel))
	}
	c.LogFormat = LogFormat(strings.ToLower(strings.TrimSpace(s.LogFormat)))
	if !logFormats[c.LogFormat] {
		errs = append(errs, fmt.Errorf("%w: log format %q (want text, json)", ErrInvalid, s.LogFormat))
	}

	var err error
	if c.ReadOnly, err = parseBool("read-only", s.ReadOnly); err != nil {
		errs = append(errs, err)
	}
	if c.EnableDestructive, err = parseBool("enable-destructive", s.EnableDestructive); err != nil {
		errs = append(errs, err)
	}
	if c.Activity, err = parseBool("activity", s.Activity); err != nil {
		errs = append(errs, err)
	}
	if c.Labels, err = parseBool("labels", s.Labels); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, s.removedSet()...)

	c.Sharing = Sharing(strings.ToLower(strings.TrimSpace(s.Sharing)))
	if !slices.Contains(SharingModes, c.Sharing) {
		errs = append(errs, fmt.Errorf("%w: sharing %q (want all or off)", ErrInvalid, s.Sharing))
	}

	if dir := strings.TrimSpace(s.LocalDir); dir != "" {
		// Existence is part of the rule, not a detail. A relative path was
		// always refused here; an absolute one that does not exist was not,
		// so a typo was accepted at startup and surfaced later as a file
		// operation failing for reasons nobody could see from `status`.
		var info os.FileInfo
		var statErr error
		switch {
		case !filepath.IsAbs(dir):
			errs = append(errs, fmt.Errorf("%w: local dir %q must be an absolute path", ErrInvalid, dir))
		default:
			if info, statErr = os.Stat(dir); statErr != nil {
				errs = append(errs, fmt.Errorf("%w: local dir %q cannot be read: %w", ErrInvalid, dir, statErr))
			} else if !info.IsDir() {
				errs = append(errs, fmt.Errorf("%w: local dir %q is not a directory", ErrInvalid, dir))
			} else {
				c.LocalDir = filepath.Clean(dir)
			}
		}
	}

	if c.MaxDownload, err = ParseBytes(strings.TrimSpace(s.MaxDownload)); err != nil {
		errs = append(errs, fmt.Errorf("%w: max download %q: %w", ErrInvalid, s.MaxDownload, err))
	} else if c.MaxDownload <= 0 {
		errs = append(errs, fmt.Errorf("%w: max download %s must be positive", ErrInvalid, s.MaxDownload))
	}

	if c.HTTPTimeout, err = time.ParseDuration(strings.TrimSpace(s.HTTPTimeout)); err != nil {
		errs = append(errs, fmt.Errorf("%w: http timeout %q: %w", ErrInvalid, s.HTTPTimeout, err))
	} else if c.HTTPTimeout < time.Second || c.HTTPTimeout > 10*time.Minute {
		// The floor is real, not decoration: a sub-second deadline makes
		// every Drive call time out, and the message promised it would be
		// refused.
		errs = append(errs, fmt.Errorf("%w: http timeout %s must be between 1s and 10m", ErrInvalid, c.HTTPTimeout))
	}

	c.ClientSecretPath = strings.TrimSpace(s.ClientSecretPath)

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return c, nil
}

// Load is Define, Parse and Build for a caller with no flags of its own.
func Load(args []string, env func(string) string) (Config, error) {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	s := Define(fs, env)
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	return s.Build()
}

func parseBool(name, v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false, nil
	case "1", "true", "yes", "on":
		return true, nil
	}
	return false, fmt.Errorf("%w: %s %q (want true or false)", ErrInvalid, name, v)
}

var bytePattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([KMGT]?I?B?)$`)

var byteUnits = map[string]int64{
	"":   1,
	"B":  1,
	"KB": 1000, "MB": 1000 * 1000, "GB": 1000 * 1000 * 1000, "TB": 1000 * 1000 * 1000 * 1000,
	"KIB": 1 << 10, "MIB": 1 << 20, "GIB": 1 << 30, "TIB": 1 << 40,
	// A bare K, M, G or T is the binary unit, which is how people write
	// it when they mean a file size.
	"K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40,
}

// ParseBytes reads a byte size written the way people write file sizes:
// 1GiB, 500MB, 2G, 1048576. Decimal units are powers of 1000 and IEC
// units powers of 1024, as their definitions say.
func ParseBytes(s string) (int64, error) {
	m := bytePattern.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(s)))
	if m == nil {
		return 0, fmt.Errorf("not a byte size (want 1GiB, 500MB, 2G or a plain number)")
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, err
	}
	unit, ok := byteUnits[m[2]]
	if !ok {
		return 0, fmt.Errorf("unknown unit %q", m[2])
	}
	v := n * float64(unit)
	if v > float64(1<<62) {
		return 0, fmt.Errorf("too large")
	}
	return int64(v), nil
}

// NewLogger builds the process logger. It writes to w, which must be
// stderr in the server path: stdout carries only JSON-RPC frames.
func NewLogger(c Config, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.LogLevel.Slog()}
	if c.LogFormat == LogJSON {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
