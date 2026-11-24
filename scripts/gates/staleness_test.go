package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
)

// The changelog fixtures: a released v0.9.0, a v0.9.1 with and without
// notes, and an [Unreleased] section with and without them. This
// repository never had a 0.9 tag, so a git call that reads the wrong
// checkout finds neither.
const (
	clReleased   = "## [0.9.0] - 2026-01-01\n\n### Added\n\n- The first tool.\n"
	clNext       = "## [0.9.1] - 2026-02-01\n\n### Added\n\n- A second tool.\n\n"
	clNextEmpty  = "## [0.9.1] - 2026-02-01\n\n"
	clUnreleased = "# Changelog\n\n## [Unreleased]\n\n"
	clWritten    = clUnreleased + "### Added\n\n- A second tool.\n\n"
	clEmpty      = "the binary's source changed but CHANGELOG.md [Unreleased] is empty"
)

// stalenessRepo is a git checkout of a module whose binary, cmd/fixture,
// imports internal/a, which embeds a file, and not internal/unused, with a
// release tag v0.9.0 on its one commit. A case changes a stalenessCopy of
// it.
func stalenessRepo(t *testing.T) string {
	t.Helper()
	isolateGit(t)
	t.Setenv("GOWORK", "off")
	dir := t.TempDir()
	for name, body := range map[string]string{
		changelogPath: clUnreleased + clReleased,
		"go.mod": textLines("module example.com/fixture", "", "go 1.21", "",
			"require example.com/dep v1.0.0", "", "replace example.com/dep => ./dep"),
		"dep/go.mod": textLines("module example.com/dep", "", "go 1.21"),
		"dep/dep.go": textLines("package dep"),
		"cmd/fixture/main.go": textLines("package main", "", `import (`, `	_ "example.com/dep"`,
			`	_ "example.com/fixture/internal/a"`, ")", "", "func main() {}"),
		"tools.go":              textLines("package fixture"),
		"internal/a/a.go":       textLines("package a", "", `import _ "embed"`, "", "//go:embed data/x.txt", "var X string"),
		"internal/a/b.go":       textLines("package a"),
		"internal/a/data/x.txt": "x\n",
		"internal/a/notes.md":   "notes\n",
		"internal/unused/u.go":  textLines("package unused"),
		"internal/a/a_test.go":  textLines("package a"),
		"internal/a/testdata/x": "x\n",
		"scripts/gates/main.go": textLines("package main", "", "func main() {}"),
		"docs/x.md":             "x\n",
	} {
		write(t, filepath.Join(dir, filepath.FromSlash(name)), body)
	}
	stalenessGit(t, dir, "init", "-q", "-b", "main")
	stalenessGit(t, dir, "add", "-A")
	stalenessGit(t, dir, "commit", "-q", "-m", "first")
	stalenessGit(t, dir, "tag", "v0.9.0")
	return dir
}

// stalenessCopy is a copy of the checkout at dir, its .git included, for
// one case to change on its own.
func stalenessCopy(t *testing.T, dir string) string {
	t.Helper()
	cp := t.TempDir()
	if err := os.CopyFS(cp, os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	return cp
}

func stalenessGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	gitAs(t, dir, "a@example.com", args...)
}

// stalenessWrite writes the files with their new contents, a file with no
// contents removed.
func stalenessWrite(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if body == "" {
			stalenessGit(t, dir, "rm", "-q", name)
		} else {
			write(t, filepath.Join(dir, filepath.FromSlash(name)), body)
		}
	}
}

// stalenessChange writes the files as stalenessWrite does and commits them.
func stalenessChange(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	stalenessWrite(t, dir, files)
	stalenessGit(t, dir, "add", "-A")
	stalenessGit(t, dir, "commit", "-q", "-m", "change")
}

// A committed change to what users get needs an entry: any file but a test
// in a package the binary imports, a file one embeds, a file the bundle
// carries from the tree, and go.mod but its require and tool lines. A
// test, a fixture, a package it does not import and a dependency bump do
// not, and nor does what is not committed, which CI never sees.
func TestTheChangelogGateCountsWhatShips(t *testing.T) {
	const goMod = "go.mod"
	base := stalenessRepo(t)
	raw, err := os.ReadFile(filepath.Join(base, goMod))
	if err != nil {
		t.Fatal(err)
	}
	gomod := string(raw)
	bump := strings.Replace(gomod, "require example.com/dep v1.0.0", "require (\n\texample.com/dep v1.1.0 // a bump\n)", 1)
	for _, tc := range []struct {
		name string
		// files are committed, and after is written after the commit.
		files, after map[string]string
		ships        bool
	}{
		{"a package the binary imports", map[string]string{"internal/a/b.go": "package a\n\n// changed\n"}, nil, true},
		{"a file added to one", map[string]string{"internal/a/c.go": "package a\n"}, nil, true},
		{"a file removed from one", map[string]string{"internal/a/b.go": ""}, nil, true},
		{"a file moved out of one", map[string]string{"internal/a/b.go": "", "docs/b.go": "package a\n"}, nil, true},
		{"the command", map[string]string{"cmd/fixture/main.go": "package main\n\nfunc main() { println() }\n"}, nil, true},
		{"a file a package embeds", map[string]string{"internal/a/data/x.txt": "y\n"}, nil, true},
		{"go.mod's go line", map[string]string{goMod: strings.Replace(gomod, "go 1.21", "go 1.22", 1)}, nil, true},
		{"go.mod's toolchain", map[string]string{goMod: gomod + "\ntoolchain go1.21.0\n"}, nil, true},
		{"go.mod's replace", map[string]string{goMod: strings.Replace(gomod, "dep =>", "dep v1.0.0 =>", 1)}, nil, true},
		{"a dependency bump", map[string]string{goMod: bump}, nil, false},
		// The go.mod grammar reads ( as a token of its own (https://go.dev/ref/mod#go-mod-file-lexical).
		{"a dependency bump in a require( block", map[string]string{goMod: strings.Replace(gomod,
			"require example.com/dep v1.0.0", "require(\n\texample.com/dep v1.1.0\n)", 1)}, nil, false},
		// A tool directive names a tool `go tool` runs, which the binary does not
		// build (https://go.dev/ref/mod#go-mod-file-tool).
		{"a tool directive", map[string]string{goMod: gomod + "\ntool example.com/dep\n"}, nil, false},
		{"a tool( block", map[string]string{goMod: gomod + "\ntool(\n\texample.com/dep\n)\n"}, nil, false},
		{"a test", map[string]string{"internal/a/a_test.go": "package a\n\n// changed\n"}, nil, false},
		{"a fixture", map[string]string{"internal/a/testdata/x": "y\n"}, nil, false},
		// The go command builds a package from assembly, a .syso and C as well
		// as Go (`go help filetype`), so every file of its directory counts.
		{"a file of a package that it does not embed", map[string]string{"internal/a/notes.md": "more\n"}, nil, true},
		{"assembly and a .syso in one", map[string]string{"internal/a/a_amd64.s": "TEXT ·x(SB),0,$0\n", "internal/a/a.syso": "x\n"}, nil, true},
		{"a file in a directory under one that is no package", map[string]string{"internal/a/notes/x.md": "x\n"}, nil, false},
		{"the bundle's manifest", map[string]string{mcpbManifestPath: "{}\n"}, nil, true},
		{"the bundle's launcher", map[string]string{alongside[linuxLauncher]: "#!/bin/sh\n"}, nil, true},
		// The bundle carries the root README, and it is still a doc.
		{"the root README", map[string]string{"README.md": "x\n"}, nil, false},
		{"a file beside the bundle's", map[string]string{"packaging/mcpb/README.md": "x\n"}, nil, false},
		{"a package the binary does not import", map[string]string{"internal/unused/u.go": "package unused\n\n// changed\n"}, nil, false},
		// A dependency's package sits at a path of its own module's, never this one's.
		{"a package at the path of a dependency's", map[string]string{"tools.go": "package fixture\n\n// changed\n"}, nil, false},
		{"a script", map[string]string{"scripts/gates/main.go": "package main\n\nfunc main() { println() }\n"}, nil, false},
		{"a doc", map[string]string{"docs/x.md": "y\n"}, nil, false},
		{"a change not committed", nil, map[string]string{"internal/a/b.go": "package a\n\n// changed\n"}, false},
		{"go.mod as committed, its go line changed after", map[string]string{goMod: bump},
			map[string]string{goMod: strings.Replace(bump, "go 1.21", "go 1.22", 1)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := stalenessCopy(t, base)
			t.Chdir(dir)
			var watched []string
			if tc.files != nil {
				stalenessChange(t, dir, tc.files)
				out, err := git("diff", "--name-only", "--no-renames", "-z", "HEAD~1", "HEAD")
				if err != nil {
					t.Fatal(err)
				}
				if watched, _, err = changelogWatched("HEAD~1", "HEAD", strings.FieldsFunc(out, func(r rune) bool { return r == 0 })); err != nil {
					t.Fatal(err)
				}
			}
			stalenessWrite(t, dir, tc.after)
			if ships := len(watched) > 0; ships != tc.ships {
				t.Errorf("changelogWatched() = %q, ships = %t; want %t", watched, ships, tc.ships)
			}
		})
	}
}

// The gate holds the README and the configuration page in a checkout with
// no release: a real finding fails it, and with none it passes.
func TestStalenessRunsWithoutARelease(t *testing.T) {
	isolateGit(t)
	bin := filepath.Join(t.TempDir(), "google-drive-mcp")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/google-drive-mcp")
	build.Dir = parityRepoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	files := map[string]string{}
	for _, name := range []string{"README.md", configDocPath, "internal/auth/auth.go", changelogPath} {
		files[name] = repoFile(t, name)
	}
	const row = "| `get_file` |"
	for _, tc := range []struct {
		name     string
		readme   func(string) string
		out, err string
		code     int
	}{
		{"nothing wrong", func(s string) string { return s }, "staleness check ok\n", "", 0},
		{"a tool missing from the README", func(s string) string {
			start := strings.Index(s, "\n"+row)
			if start < 0 {
				t.Fatalf("README.md has no row starting %q", row)
			}
			end := strings.IndexByte(s[start+1:], '\n')
			return s[:start] + s[start+1+end:]
		}, "README's tool table does not list the registered tool get_file\n", "1 documentation problem(s)", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range files {
				if name == "README.md" {
					body = tc.readme(body)
				}
				write(t, filepath.Join(dir, filepath.FromSlash(name)), body)
			}
			stalenessGit(t, dir, "init", "-q", "-b", "main")
			stalenessGit(t, dir, "add", "-A")
			stalenessGit(t, dir, "commit", "-q", "-m", "first")
			t.Chdir(dir)
			var out strings.Builder
			err := staleness(&out, []string{bin})
			passed := tc.err == "" && err == nil
			refused := tc.err != "" && err != nil && err.Error() == tc.err && exitCode(err) == tc.code
			if !passed && !refused || out.String() != tc.out {
				t.Errorf("staleness() = %v (exit %d), printing %q; want %q (exit %d), printing %q",
					err, exitCode(err), out.String(), tc.err, tc.code, tc.out)
			}
		})
	}
}

// TestEveryScopeLoginCanRequestIsInTheReadme. The setup step named one
// scope where the code can ask for five: the label and activity pairs
// are requested only with their features on, which is exactly why nobody
// noticed. A reader adds what they were told to, turns a feature on, and
// is refused, on an External app, by a consent screen that does not
// carry the rest.
func TestEveryScopeLoginCanRequestIsInTheReadme(t *testing.T) {
	t.Chdir("../..")
	if problems := checkScopesDocumented(); len(problems) > 0 {
		t.Errorf("%s", strings.Join(problems, "\n"))
	}
}

// TestAScopeMissingFromTheReadmeIsRefused proves it bites, and that it
// reads the code rather than a list somebody typed here.
func TestAScopeMissingFromTheReadmeIsRefused(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join("internal", "auth"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := "package auth\n\nconst (\n" +
		"\tScopeDrive    = \"https://www.googleapis.com/auth/drive\"\n" +
		"\tScopeInvented = \"https://www.googleapis.com/auth/drive.invented\"\n)\n"
	write(t, filepath.Join("internal", "auth", "auth.go"), source)
	write(t, "README.md", "Add the scope https://www.googleapis.com/auth/drive and nothing else.\n")

	problems := checkScopesDocumented()
	if len(problems) == 0 {
		t.Fatal("a scope the code requests and the README omits was accepted")
	}
	if !strings.Contains(strings.Join(problems, "\n"), "drive.invented") {
		t.Errorf("the report does not name the scope:\n%s", strings.Join(problems, "\n"))
	}
}

// The full surface turns on every switch that registers tools, and
// read-only, which removes them, is never among them.
func TestTheFullSurfaceTurnsOnEveryGatingSetting(t *testing.T) {
	want := []string{"GDRIVE_ACTIVITY=true", "GDRIVE_ENABLE_DESTRUCTIVE=true", "GDRIVE_LABELS=true"}
	if got := fullSurfaceEnv(); !slices.Equal(got, want) {
		t.Errorf("fullSurfaceEnv() = %v, want %v", got, want)
	}
}

// The README's Claude Code permission rules must name registered tools:
// a rule for a misspelled or removed tool asks nobody. A rule under
// another server's name is not this server's, and a README with no rule
// is a check that read nothing.
func TestPermissionRulesNameRegisteredTools(t *testing.T) {
	registered := []string{"delete_file", "share_by_link"}
	const rules = "```json\n{\"permissions\": {\"ask\": [\"mcp__google-drive__delete_file\", " +
		"\"mcp__google-drive__share_by_link\", \"mcp__other-server__some_tool\"]}}\n```\n"
	if problems := checkPermissionRules(rules, registered); len(problems) != 0 {
		t.Errorf("rules for registered tools were reported: %v", problems)
	}
	broken := strings.Replace(rules, "mcp__google-drive__delete_file", "mcp__google-drive__delete_files", 1)
	want := []string{"README.md gives a permission rule for delete_files, which is not registered"}
	if problems := checkPermissionRules(broken, registered); !slices.Equal(problems, want) {
		t.Errorf("problems = %v, want %v", problems, want)
	}
	want = []string{`README.md gives no Claude Code permission rule for this server ("mcp__google-drive__...")`}
	if problems := checkPermissionRules("no rules here", registered); !slices.Equal(problems, want) {
		t.Errorf("problems = %v, want %v", problems, want)
	}
}

// The configuration page names each variable as a whole word: a longer
// word that contains one does not document it, whatever the case of the
// letter beside it, a name the server does not read is refused, a
// pattern is not a name, and the page says what a profile name is.
func TestTheConfigurationPageIsHeldByWholeWord(t *testing.T) {
	settings := []string{"GDRIVE_PROFILE", "GDRIVE_READ_ONLY"}
	page := "`GDRIVE_PROFILE` and GDRIVE_READ_ONLY. A name is " + config.ProfileRule + ".\n"
	for _, page := range []string{page, page + "Each is a `GDRIVE_*` variable; GDRIVE_READ_* are switches.\n"} {
		if problems := configDocProblems(settings, page); len(problems) != 0 {
			t.Errorf("a page naming both was refused: %v\n%s", problems, page)
		}
	}
	for _, tc := range []struct{ name, page, want string }{
		{"a longer name in place of one", strings.Replace(page, "GDRIVE_READ_ONLY.", "GDRIVE_READ_ONLY_X.", 1),
			"docs/configuration.md does not document GDRIVE_READ_ONLY"},
		{"a name inside a longer word", strings.Replace(page, "and GDRIVE_READ_ONLY", "and XGDRIVE_READ_ONLY", 1),
			"docs/configuration.md does not document GDRIVE_READ_ONLY"},
		{"a lowercase letter before a name", strings.Replace(page, "and GDRIVE_READ_ONLY", "and xGDRIVE_READ_ONLY", 1),
			"docs/configuration.md does not document GDRIVE_READ_ONLY"},
		{"a lowercase letter after a name", strings.Replace(page, "GDRIVE_READ_ONLY.", "GDRIVE_READ_ONLYx.", 1),
			"docs/configuration.md does not document GDRIVE_READ_ONLY"},
		{"a name the server does not read", page + "GDRIVE_GONE\n",
			"docs/configuration.md documents GDRIVE_GONE, which the server does not read"},
		{"no profile rule", strings.Replace(page, config.ProfileRule, "a name", 1),
			`docs/configuration.md does not say a profile name is "` + config.ProfileRule + `"`},
		// An example documents nothing.
		{"a name only in fenced code", strings.Replace(page, "and GDRIVE_READ_ONLY.", ".\n\n```sh\nGDRIVE_READ_ONLY=true\n```\n", 1),
			"docs/configuration.md does not document GDRIVE_READ_ONLY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if problems := configDocProblems(settings, tc.page); !slices.Contains(problems, tc.want) {
				t.Errorf("problems %q, want one %q", problems, tc.want)
			}
		})
	}
}

// The README's tool table is the table outside fenced code: a row in an
// example documents no tool, and nor does a name in one for a tool the
// README describes in prose.
func TestTheReadmeToolsAreReadOutsideFencedCode(t *testing.T) {
	const rules = "```json\n\"mcp__google-drive__get_file\", \"mcp__google-drive__delete_file\"\n```\n"
	for _, tc := range []struct {
		name, readme string
		want         []string
	}{
		{"a row and a prose name", "| `get_file` | reads |\n\n`delete_file` is permanent.\n\n" + rules, nil},
		{"a row only in fenced code", "```\n| `get_file` | reads |\n```\n\n`delete_file` is permanent.\n\n" + rules,
			[]string{"README's tool table does not list the registered tool get_file"}},
		{"a prose tool named only in fenced code", "| `get_file` | reads |\n\n~~~\n`delete_file`\n~~~\n\n" + rules,
			[]string{"README does not mention the registered tool delete_file at all; it is meant to be described in prose rather than in the table"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			write(t, "README.md", tc.readme)
			if got := checkReadmeTools([]string{"get_file", "delete_file"}); !slices.Equal(got, tc.want) {
				t.Errorf("problems = %q, want %q", got, tc.want)
			}
		})
	}
}
