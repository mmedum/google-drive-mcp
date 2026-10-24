package config

import (
	"bytes"
	"errors"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
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

// Build's refusal of a sharing mode lists the modes, as the bundle's
// field does.
func TestSharingRule(t *testing.T) {
	if SharingRule != "all or off" {
		t.Errorf("SharingRule = %q, want \"all or off\"", SharingRule)
	}
	_, err := build(t, map[string]string{"GDRIVE_SHARING": "internal"})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "(want all or off)") {
		t.Errorf("GDRIVE_SHARING=internal: %v; want it refused, listing the modes", err)
	}
}

// Every GDRIVE_ variable the server's code spells is one EnvVars lists,
// so a variable read outside Define cannot escape the bundle and
// staleness gates. So is every one scripts/ spells: the smoke test, the
// evals and the live driver set them for the server, and a misspelling
// there tests a configuration nobody asked for. The live driver reads no
// GDRIVE_ variable of its own today; one would be left out here by name.
// spelled says what counts as a name. The scan must find a name outside
// this package in each root, or it read too little.
func TestEveryVariableTheCodeNamesIsInEnvVars(t *testing.T) {
	known := map[string]bool{}
	for _, name := range EnvVars() {
		known[name] = true
	}
	found := map[string]string{}
	elsewhere := map[string]int{}
	fset := token.NewFileSet()
	roots := []string{"../../cmd", "../../internal", "../../scripts"}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			outside := filepath.Dir(path) != filepath.Join("..", "..", "internal", "config")
			for word, at := range spelled(f) {
				found[word] = fset.Position(at).String()
				if outside {
					elsewhere[root]++
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range roots {
		if elsewhere[root] == 0 {
			t.Errorf("the scan found no GDRIVE_ name in %s outside internal/config; it read too little", root)
		}
	}
	for _, v := range slices.Sorted(maps.Keys(found)) {
		if !known[v] {
			t.Errorf("%s names %s, which EnvVars does not list", found[v], v)
		}
	}
}

// The scan reads a + chain in order, so a name built after another
// operand is seen, and reads inside an operand it cannot join.
func TestTheNameScanReadsEveryChainInOrder(t *testing.T) {
	const src = `package p

var (
	a = "set " + x + EnvPrefix + "AFTER_OPERAND"
	b = EnvPrefix + ("IN_" + "PARENS")
	c = x + "GDRIVE_LITERAL"
	d = f(config.EnvPrefix+"IN_CALL") + x
	e = EnvPrefix + "A" + x + EnvPrefix + "B"
	g = Other + "NOT_A_NAME"
)
`
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := slices.Sorted(maps.Keys(spelled(f)))
	want := []string{"GDRIVE_A", "GDRIVE_AFTER_OPERAND", "GDRIVE_B", "GDRIVE_IN_CALL", "GDRIVE_IN_PARENS", "GDRIVE_LITERAL"}
	if !slices.Equal(got, want) {
		t.Errorf("spelled = %v, want %v", got, want)
	}
}

// envName is a whole GDRIVE_ variable name.
var envName = regexp.MustCompile(`^GDRIVE_[A-Z0-9][A-Z0-9_]*$`)

// spelled is every GDRIVE_ name f spells, each with where. A name is
// seen as a string literal, or as a run of literals and EnvPrefix joined
// by +, read in order: any other operand ends the run and is read on its
// own. One built from any other constant, or through a format verb, is
// not seen.
func spelled(f *ast.File) map[string]token.Pos {
	found := map[string]token.Pos{}
	spell := func(s string, at token.Pos) {
		for _, word := range strings.FieldsFunc(s, func(r rune) bool {
			return r != '_' && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
		}) {
			if envName.MatchString(word) {
				found[word] = at
			}
		}
	}
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BinaryExpr:
			if n.Op != token.ADD {
				return true
			}
			run, at := "", n.Pos()
			for _, op := range operands(n) {
				if s, ok := joined(op); ok {
					if run == "" {
						at = op.Pos()
					}
					run += s
					continue
				}
				spell(run, at)
				run = ""
				ast.Inspect(op, visit)
			}
			spell(run, at)
			return false
		case *ast.BasicLit:
			if s, err := strconv.Unquote(n.Value); n.Kind == token.STRING && err == nil {
				spell(s, n.Pos())
			}
		}
		return true
	}
	ast.Inspect(f, visit)
	return found
}

// operands is a + chain's operands in order, through parentheses.
func operands(e ast.Expr) []ast.Expr {
	switch e := e.(type) {
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return append(operands(e.X), operands(e.Y)...)
		}
	case *ast.ParenExpr:
		if b, ok := e.X.(*ast.BinaryExpr); ok && b.Op == token.ADD {
			return operands(b)
		}
	}
	return []ast.Expr{e}
}

// joined is a + chain of string literals and EnvPrefix as one string.
func joined(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		x, ok := joined(e.X)
		if !ok {
			return "", false
		}
		y, ok := joined(e.Y)
		return x + y, ok
	case *ast.ParenExpr:
		return joined(e.X)
	case *ast.BasicLit:
		s, err := strconv.Unquote(e.Value)
		return s, e.Kind == token.STRING && err == nil
	case *ast.Ident:
		return EnvPrefix, e.Name == "EnvPrefix"
	case *ast.SelectorExpr:
		return EnvPrefix, e.Sel.Name == "EnvPrefix"
	}
	return "", false
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
