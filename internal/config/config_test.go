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

// build loads the configuration from a fake environment and the given
// flags, the way the real commands do.
func build(t *testing.T, env map[string]string, args ...string) (Config, error) {
	t.Helper()
	return Load(args, func(k string) string { return env[k] })
}

func TestLoad(t *testing.T) {
	c, err := Load([]string{"-read-only", "true"}, func(string) string { return "" })
	if err != nil || !c.ReadOnly {
		t.Errorf("Load = %+v, %v; want read-only", c, err)
	}
	if _, err := Load([]string{"-unknown"}, func(string) string { return "" }); err == nil {
		t.Error("an unknown flag was accepted")
	}
}

// A profile name is held to ProfileRule, and the refusal says it. Only A
// to Z are lowered: a character Unicode folds into a to z, such as the
// Kelvin sign, is refused rather than renamed to another profile.
func TestProfileRule(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"Work", "work"}, {" work ", "work"}, {"a", "a"}, {"9-", "9-"}, {"a-b_c", "a-b_c"},
		{strings.Repeat("a", 64), strings.Repeat("a", 64)},
	} {
		if c, err := build(t, map[string]string{"GDRIVE_PROFILE": tc.name}); err != nil || c.Profile != tc.want {
			t.Errorf("GDRIVE_PROFILE=%q: profile %q, %v; want %q", tc.name, c.Profile, err, tc.want)
		}
	}
	for _, name := range []string{strings.Repeat("a", 65), "-a", "_a", "a.b", "a b", "caf\u00e9", "\u212aey"} {
		_, err := build(t, map[string]string{"GDRIVE_PROFILE": name})
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "must be "+ProfileRule) {
			t.Errorf("GDRIVE_PROFILE=%q: %v; want it refused, naming the rule", name, err)
		}
	}
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

// Vars is what the bundle gate reads a setting's kind from, so each
// claim it makes is held to what Build does. A switch takes true and
// false and refuses anything else, and nothing else does that. An empty
// value configures what setting nothing does, which is what lets a
// bundle leave a field blank.
func TestVarsDescribeWhatBuildDoes(t *testing.T) {
	vars := Vars()
	if len(vars) != 15 {
		t.Fatalf("Vars has %d variables; the server reads 15", len(vars))
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
		c, err := build(t, map[string]string{v.Name: ""})
		if err != nil {
			t.Errorf("%s=\"\" is refused: %v", v.Name, err)
			continue
		}
		if !reflect.DeepEqual(c, unset) {
			t.Errorf("%s=\"\" configures something:\n got %+v\nwant %+v", v.Name, c, unset)
		}
	}
	want := []string{"GDRIVE_READ_ONLY", "GDRIVE_ENABLE_DESTRUCTIVE", "GDRIVE_LABELS", "GDRIVE_ACTIVITY"}
	if !slices.Equal(switches, want) {
		t.Errorf("switches %v, want %v", switches, want)
	}
}

// EnvVars is every variable the server reads, the removed one and the
// two read outside Define included.
func TestEnvVars(t *testing.T) {
	want := []string{
		"GDRIVE_ACTIVITY", "GDRIVE_CLIENT_SECRET", "GDRIVE_CONFIG_DIR", "GDRIVE_ENABLE_DESTRUCTIVE",
		"GDRIVE_HTTP_TIMEOUT", "GDRIVE_LABELS", "GDRIVE_LOCAL_DIR", "GDRIVE_LOG_FORMAT", "GDRIVE_LOG_LEVEL",
		"GDRIVE_MAX_DOWNLOAD", "GDRIVE_PROFILE", "GDRIVE_READ_ONLY", "GDRIVE_REFRESH_TOKEN",
		"GDRIVE_REQUIRE_PROMPT", "GDRIVE_SHARING",
	}
	if got := EnvVars(); !slices.Equal(got, want) {
		t.Errorf("EnvVars =\n%v\nwant\n%v", got, want)
	}
}

// Vars records what Define reads, so Define reads nothing past it: a
// variable read around the recording would be one the bundle gate and
// the staleness gate never see. Each is read once.
func TestDefineReadsOnlyWhatVarsNames(t *testing.T) {
	var read []string
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	Define(fs, func(name string) string {
		read = append(read, name)
		return ""
	})
	var want []string
	for _, v := range Vars() {
		if !slices.Contains(envOnly, v.Name) {
			want = append(want, v.Name)
		}
	}
	if len(read) < 13 {
		t.Fatalf("Define read %d variables, below the floor of 13", len(read))
	}
	if !slices.Equal(read, want) {
		t.Errorf("Define reads\n%v\nand Vars names\n%v", read, want)
	}
	if twice := readTwice(read); len(twice) != 0 {
		t.Errorf("Define reads %v more than once", twice)
	}
	if got := readTwice([]string{"GDRIVE_A", "GDRIVE_B", "GDRIVE_A"}); !slices.Equal(got, []string{"GDRIVE_A"}) {
		t.Errorf("readTwice of a duplicate read = %v, want [GDRIVE_A]", got)
	}
}

// readTwice is every name that appears more than once in names, sorted.
func readTwice(names []string) []string {
	seen := map[string]int{}
	for _, n := range names {
		seen[n]++
	}
	var twice []string
	for n, count := range seen {
		if count > 1 {
			twice = append(twice, n)
		}
	}
	slices.Sort(twice)
	return twice
}
