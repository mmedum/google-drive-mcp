package config

import (
	"bytes"
	"errors"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

// GDRIVE_REQUIRE_PROMPT was removed in 3.0.0. Off, or unset, still
// starts; anything else is refused, saying what to do instead, since
// ignoring it would quietly allow what its owner set it to stop. The
// flag is gone.
func TestARemovedSettingIsRefusedUnlessOff(t *testing.T) {
	for _, v := range []string{"", "false", " FALSE ", "0", "no", "off"} {
		if _, err := build(t, map[string]string{"GDRIVE_REQUIRE_PROMPT": v}); err != nil {
			t.Errorf("GDRIVE_REQUIRE_PROMPT=%q: %v", v, err)
		}
	}
	for _, v := range []string{"true", "1", "maybe"} {
		_, err := build(t, map[string]string{"GDRIVE_REQUIRE_PROMPT": v})
		if err == nil || !errors.Is(err, ErrInvalid) ||
			!strings.Contains(err.Error(), "GDRIVE_REQUIRE_PROMPT was removed") ||
			!strings.Contains(err.Error(), "GDRIVE_ENABLE_DESTRUCTIVE off") {
			t.Errorf("GDRIVE_REQUIRE_PROMPT=%q: %v", v, err)
		}
		if err != nil && strings.Contains(err.Error(), "maybe") {
			t.Errorf("GDRIVE_REQUIRE_PROMPT=%q: the refusal repeats the value: %v", v, err)
		}
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	Define(fs, func(string) string { return "" })
	if fs.Lookup("require-prompt") != nil {
		t.Error("--require-prompt is still a flag")
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

// The timeout must lie in [1s, 10m]. The floor is real: a sub-second
// deadline makes every Drive call time out, and the message promises it
// is refused.
func TestHTTPTimeoutBounds(t *testing.T) {
	for _, c := range []struct {
		in string
		ok bool
	}{
		{"-1s", false},
		{"0s", false},
		{"1ms", false},
		{"999ms", false},
		{"1s", true},
		{"90s", true},
		{"10m", true},
		{"10m1s", false},
		{"11m", false},
	} {
		_, err := build(t, map[string]string{"GDRIVE_HTTP_TIMEOUT": c.in})
		if (err == nil) != c.ok {
			t.Errorf("GDRIVE_HTTP_TIMEOUT=%q: err = %v, want accepted = %v", c.in, err, c.ok)
		}
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
		// The largest size accepted, exactly.
		"4194304TiB": 1 << 62,
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
	for _, in := range []string{"", "big", "1XB", "-5MB", "MB", "4194305TiB"} {
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

func TestNewLoggerHonorsFormatAndLevel(t *testing.T) {
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

// A directory that does not exist used to be accepted, because only a
// relative path was refused. The typo then surfaced at the moment
// somebody tried to move a file, a long way from the setting that caused
// it and invisible to `status`.
func TestTheDirectoryMustExistAndBeADirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, dir string
		ok        bool
	}{
		{"a real directory", dir, true},
		{"unset is allowed and turns the feature off", "", true},
		{"a path that does not exist", filepath.Join(dir, "nope"), false},
		{"a file rather than a directory", file, false},
		{"a relative path", filepath.Join("relative", "path"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := build(t, map[string]string{"GDRIVE_LOCAL_DIR": tc.dir})
			if tc.ok {
				if err != nil {
					t.Errorf("%q was refused: %v", tc.dir, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("%q was accepted; the failure would surface at the first file operation", tc.dir)
			}
			if !strings.Contains(err.Error(), "local dir") {
				t.Errorf("the error does not name the setting: %v", err)
			}
		})
	}
}

// Vars is what the bundle gate reads a switch's default from, so each
// claim it makes is held to what Build does. A switch takes true and
// false and refuses anything else, and nothing else does that. Its
// default, and an empty value, each configure what setting nothing does,
// which is what lets a bundle leave a field blank.
func TestVarsDescribeWhatBuildDoes(t *testing.T) {
	vars := Vars()
	if len(vars) != 12 {
		t.Fatalf("Vars has %d settings; Define binds 12 flags", len(vars))
	}
	unset, err := build(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	var switches []string
	for _, v := range vars {
		accepts := func(value string) bool {
			_, err := build(t, map[string]string{v.Name: value})
			return err == nil
		}
		if boolean := accepts("true") && accepts("false") && !accepts("maybe"); boolean != v.Switch {
			t.Errorf("%s: Switch is %t, and Build reads it as a boolean: %t", v.Name, v.Switch, boolean)
		}
		if v.Switch {
			switches = append(switches, v.Name)
		}
		for _, value := range []string{v.Default, ""} {
			c, err := build(t, map[string]string{v.Name: value})
			if err != nil {
				t.Errorf("%s=%q is refused: %v", v.Name, value, err)
				continue
			}
			if !reflect.DeepEqual(c, unset) {
				t.Errorf("%s=%q configures something:\n got %+v\nwant %+v", v.Name, value, c, unset)
			}
		}
	}
	want := []string{"GDRIVE_READ_ONLY", "GDRIVE_ENABLE_DESTRUCTIVE", "GDRIVE_LABELS", "GDRIVE_ACTIVITY"}
	if !slices.Equal(switches, want) {
		t.Errorf("switches %v, want %v", switches, want)
	}
}

// EnvVars is recorded from the lookups Define makes, so it holds the
// removed setting that no flag binds as well as every one that does.
func TestEnvVarsIsEveryLookup(t *testing.T) {
	got := EnvVars()
	for _, v := range Vars() {
		if !slices.Contains(got, v.Name) {
			t.Errorf("EnvVars lacks %s, which Define binds", v.Name)
		}
	}
	if !slices.Contains(got, "GDRIVE_REQUIRE_PROMPT") {
		t.Errorf("EnvVars lacks GDRIVE_REQUIRE_PROMPT, which Define reads to refuse it set: %v", got)
	}
	if len(got) != len(Vars())+1 || !slices.IsSorted(got) {
		t.Errorf("EnvVars is %v; want the %d settings and the removed one, sorted", got, len(Vars()))
	}
}
