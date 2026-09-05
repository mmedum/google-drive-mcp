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
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
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
// organisation policy decides what may actually be shared; this only
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
	HTTPTimeout      time.Duration
	Labels           bool
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
	ClientSecretPath  string
}

// Define registers one flag per setting on fs. Each flag defaults to the
// matching GDRIVE_* variable (read through env) so that a flag passed on
// the command line wins over the environment, and the environment wins
// over the built-in default.
func Define(fs *flag.FlagSet, env func(string) string) *Settings {
	s := &Settings{}
	def := func(p *string, name, key, fallback, usage string) {
		v := env(EnvPrefix + key)
		if v == "" {
			v = fallback
		}
		fs.StringVar(p, name, v, usage+" [env "+EnvPrefix+key+"]")
	}
	def(&s.Profile, "profile", "PROFILE", "default", "named configuration profile")
	def(&s.LogLevel, "log-level", "LOG_LEVEL", string(LogInfo), "log level: debug, info, warn, error")
	def(&s.LogFormat, "log-format", "LOG_FORMAT", string(LogText), "log format: text, json")
	def(&s.ReadOnly, "read-only", "READ_ONLY", "false", "register only read tools and request read-only scopes")
	def(&s.EnableDestructive, "enable-destructive", "ENABLE_DESTRUCTIVE", "false", "register the tools that destroy without a way back (permanent delete, empty trash)")
	def(&s.Sharing, "sharing", "SHARING", string(SharingAll), "sharing tools: all, off")
	def(&s.LocalDir, "local-dir", "LOCAL_DIR", "", "the one directory downloads are written to and uploads are read from (unset disables file transfer)")
	def(&s.MaxDownload, "max-download", "MAX_DOWNLOAD", "1GiB", "largest single download, e.g. 500MB or 2GiB")
	def(&s.HTTPTimeout, "http-timeout", "HTTP_TIMEOUT", "60s", "per-attempt and per-chunk timeout for Google API calls")
	def(&s.Labels, "labels", "LABELS", "false", "enable Workspace labels (adds the Drive Labels API scopes at login)")
	def(&s.ClientSecretPath, "client-secret", "CLIENT_SECRET", "", "path to the OAuth Desktop client JSON (overrides the stored profile setting)")
	return s
}

var (
	profilePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	logLevels      = map[LogLevel]bool{LogDebug: true, LogInfo: true, LogWarn: true, LogError: true}
	logFormats     = map[LogFormat]bool{LogText: true, LogJSON: true}
	sharingModes   = map[Sharing]bool{SharingAll: true, SharingOff: true}
)

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("config: invalid")

// Build validates the settings and returns a Config.
func (s *Settings) Build() (Config, error) {
	var c Config
	var errs []error

	c.Profile = strings.ToLower(strings.TrimSpace(s.Profile))
	if !profilePattern.MatchString(c.Profile) {
		errs = append(errs, fmt.Errorf("%w: profile %q must match %s", ErrInvalid, s.Profile, profilePattern))
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
	if c.Labels, err = parseBool("labels", s.Labels); err != nil {
		errs = append(errs, err)
	}

	c.Sharing = Sharing(strings.ToLower(strings.TrimSpace(s.Sharing)))
	if !sharingModes[c.Sharing] {
		errs = append(errs, fmt.Errorf("%w: sharing %q (want all or off)", ErrInvalid, s.Sharing))
	}

	if dir := strings.TrimSpace(s.LocalDir); dir != "" {
		if !filepath.IsAbs(dir) {
			errs = append(errs, fmt.Errorf("%w: local dir %q must be an absolute path", ErrInvalid, dir))
		}
		c.LocalDir = filepath.Clean(dir)
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
