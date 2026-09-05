package config

import (
	"bytes"
	"flag"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// build runs Define + Build over a fake environment, the way the real
// commands do.
func build(t *testing.T, env map[string]string, args ...string) (Config, error) {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	s := Define(fs, func(k string) string { return env[k] })
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return s.Build()
}

func TestDefaults(t *testing.T) {
	c, err := build(t, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Profile != "default" {
		t.Errorf("profile = %q", c.Profile)
	}
	if c.LogLevel != LogInfo || c.LogFormat != LogText {
		t.Errorf("log = %q/%q", c.LogLevel, c.LogFormat)
	}
	if c.Sharing != SharingAll {
		t.Errorf("sharing = %q, want all", c.Sharing)
	}
	if c.ReadOnly || c.EnableDestructive || c.Labels {
		t.Errorf("switches should default off: %+v", c)
	}
	if c.LocalDir != "" {
		t.Errorf("local dir should default to unset, got %q", c.LocalDir)
	}
	if c.MaxDownload != 1<<30 {
		t.Errorf("max download = %d, want 1GiB", c.MaxDownload)
	}
	if c.HTTPTimeout != 60*time.Second {
		t.Errorf("http timeout = %s", c.HTTPTimeout)
	}
}

func TestEnvironmentThenFlagWins(t *testing.T) {
	env := map[string]string{"GDRIVE_LOG_LEVEL": "warn", "GDRIVE_SHARING": "off"}
	c, err := build(t, env)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.LogLevel != LogWarn || c.Sharing != SharingOff {
		t.Fatalf("environment ignored: %+v", c)
	}
	c, err = build(t, env, "-log-level", "debug")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.LogLevel != LogDebug {
		t.Errorf("flag did not override the environment: %q", c.LogLevel)
	}
	if c.Sharing != SharingOff {
		t.Errorf("unrelated environment setting lost: %q", c.Sharing)
	}
}

func TestInvalidValuesAreReportedTogether(t *testing.T) {
	_, err := build(t, map[string]string{
		"GDRIVE_PROFILE":      "Not Valid",
		"GDRIVE_LOG_LEVEL":    "loud",
		"GDRIVE_LOG_FORMAT":   "xml",
		"GDRIVE_SHARING":      "internal",
		"GDRIVE_READ_ONLY":    "maybe",
		"GDRIVE_HTTP_TIMEOUT": "12 parsecs",
		"GDRIVE_MAX_DOWNLOAD": "many",
	})
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	for _, want := range []string{"profile", "log level", "log format", "sharing", "read-only", "http timeout", "max download"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not mention %q: %v", want, msg)
		}
	}
}

func TestSharingRejectsTheModeThatWasDropped(t *testing.T) {
	// An `internal` mode that copied the admin's rules was considered and
	// dropped; it must not quietly become `all`.
	if _, err := build(t, map[string]string{"GDRIVE_SHARING": "internal"}); err == nil {
		t.Fatal("sharing=internal should be rejected")
	}
}

func TestLocalDirMustBeAbsolute(t *testing.T) {
	if _, err := build(t, map[string]string{"GDRIVE_LOCAL_DIR": "downloads"}); err == nil {
		t.Fatal("a relative local dir should be rejected")
	}
	c, err := build(t, map[string]string{"GDRIVE_LOCAL_DIR": absPath(t)})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.LocalDir == "" {
		t.Error("absolute local dir was dropped")
	}
}

func TestHTTPTimeoutBounds(t *testing.T) {
	for _, v := range []string{"0s", "-1s", "11m"} {
		if _, err := build(t, map[string]string{"GDRIVE_HTTP_TIMEOUT": v}); err == nil {
			t.Errorf("timeout %q should be rejected", v)
		}
	}
	if _, err := build(t, map[string]string{"GDRIVE_HTTP_TIMEOUT": "90s"}); err != nil {
		t.Errorf("90s should be accepted: %v", err)
	}
}

func TestParseBytes(t *testing.T) {
	cases := map[string]int64{
		"1024":  1024,
		"1KiB":  1024,
		"1KB":   1000,
		"2G":    2 << 30,
		"1GiB":  1 << 30,
		"1.5GB": 1_500_000_000,
		"500MB": 500_000_000,
		"10 MB": 10_000_000,
		"1TiB":  1 << 40,
	}
	for in, want := range cases {
		got, err := ParseBytes(in)
		if err != nil {
			t.Errorf("ParseBytes(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseBytes(%q) = %d, want %d", in, got, want)
		}
	}
	for _, in := range []string{"", "big", "1XB", "-5MB", "MB"} {
		if _, err := ParseBytes(in); err == nil {
			t.Errorf("ParseBytes(%q) should fail", in)
		}
	}
}

func TestMaxDownloadMustBePositive(t *testing.T) {
	if _, err := build(t, map[string]string{"GDRIVE_MAX_DOWNLOAD": "0"}); err == nil {
		t.Fatal("a zero download cap should be rejected")
	}
}

func TestBoolSpellings(t *testing.T) {
	for _, v := range []string{"true", "yes", "on", "1"} {
		c, err := build(t, map[string]string{"GDRIVE_READ_ONLY": v})
		if err != nil || !c.ReadOnly {
			t.Errorf("%q should mean true (err %v)", v, err)
		}
	}
	for _, v := range []string{"false", "no", "off", "0", ""} {
		c, err := build(t, map[string]string{"GDRIVE_READ_ONLY": v})
		if err != nil || c.ReadOnly {
			t.Errorf("%q should mean false (err %v)", v, err)
		}
	}
}

func TestSlogLevels(t *testing.T) {
	want := map[LogLevel]slog.Level{
		LogDebug: slog.LevelDebug, LogInfo: slog.LevelInfo,
		LogWarn: slog.LevelWarn, LogError: slog.LevelError,
		LogLevel("nonsense"): slog.LevelInfo,
	}
	for l, w := range want {
		if got := l.Slog(); got != w {
			t.Errorf("%q.Slog() = %v, want %v", l, got, w)
		}
	}
}

func TestNewLoggerHonoursFormatAndLevel(t *testing.T) {
	var buf bytes.Buffer
	NewLogger(Config{LogLevel: LogError, LogFormat: LogText}, &buf).Info("quiet")
	if buf.Len() != 0 {
		t.Errorf("info logged below the level: %q", buf.String())
	}
	buf.Reset()
	NewLogger(Config{LogLevel: LogInfo, LogFormat: LogJSON}, &buf).Info("loud")
	if !strings.HasPrefix(buf.String(), "{") {
		t.Errorf("json format did not produce JSON: %q", buf.String())
	}
}

func absPath(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func TestHTTPTimeoutFloorIsEnforcedNotJustDescribed(t *testing.T) {
	// A sub-second deadline makes every Drive call time out, and the
	// error text says it is refused, so it has to be.
	for _, v := range []string{"1ms", "999ms"} {
		if _, err := build(t, map[string]string{"GDRIVE_HTTP_TIMEOUT": v}); err == nil {
			t.Errorf("timeout %q should be rejected, as the message promises", v)
		}
	}
	if _, err := build(t, map[string]string{"GDRIVE_HTTP_TIMEOUT": "1s"}); err != nil {
		t.Errorf("1s is the documented floor and should be accepted: %v", err)
	}
}
