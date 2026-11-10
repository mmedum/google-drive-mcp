package main

import (
	"errors"
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

// The CHANGELOG rule: a change to the binary's source is written up under
// [Unreleased], or, on the release commit before its tag, under the
// version that commit releases, a prerelease's included. A heading in a
// fence is an example and cuts no release.
func TestTheChangelogRule(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		changelog     string
		sourceChanged bool
		// tagged are the versions that have a tag.
		tagged []string
		want   []string
	}{
		{"an undocumented source change", clUnreleased + clReleased, true, []string{"0.9.0"}, []string{clEmpty}},
		{"a documented source change", clWritten + clReleased, true, []string{"0.9.0"}, nil},
		{"the release commit before its tag", clUnreleased + clNext + clReleased, true, []string{"0.9.0"}, nil},
		{"a prerelease's commit before its tag", clUnreleased + strings.Replace(clNext, "0.9.1", "1.0.0-rc.1", 1) + clReleased,
			true, []string{"0.9.0"}, nil},
		{"a release commit with nothing under its version", clUnreleased + clNextEmpty + clReleased, true,
			[]string{"0.9.0"},
			[]string{"the binary's source changed but CHANGELOG.md [0.9.1] (untagged, so this is a release commit) is empty"}},
		{"an undocumented source change after the release is tagged", clUnreleased + clNext + clReleased, true,
			[]string{"0.9.0", "0.9.1"}, []string{clEmpty}},
		{"a release cut only in a fence", clUnreleased + "```\n" + clNext + "```\n\n" + clReleased, true,
			[]string{"0.9.0"}, []string{clEmpty}},
		{"no change to the binary's source", clUnreleased + clReleased, false, []string{"0.9.0"}, nil},
		// An entry is a bullet at the margin: an indented one is a sub-point
		// of an entry, and with none above it is nobody's.
		{"only an indented bullet under [Unreleased]", clUnreleased + "### Added\n\n  - A sub-point.\n\n" + clReleased, true,
			[]string{"0.9.0"}, []string{clEmpty}},
		{"only a bullet in a fence under [Unreleased]", clUnreleased + "```\n- An example.\n```\n\n" + clReleased, true,
			[]string{"0.9.0"}, []string{clEmpty}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := changelogRule(tc.changelog, tc.sourceChanged, func(v string) bool { return slices.Contains(tc.tagged, v) })
			if !slices.Equal(got, tc.want) {
				t.Errorf("changelogRule() = %q, want %q", got, tc.want)
			}
		})
	}
}

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
func TestTheChangelogRuleCountsWhatShips(t *testing.T) {
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
			t.Parallel()
			dir := stalenessCopy(t, base)
			if tc.files != nil {
				stalenessChange(t, dir, tc.files)
			}
			stalenessWrite(t, dir, tc.after)
			got, err := checkChangelog(dir)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			if tc.ships {
				want = []string{clEmpty}
			}
			if !slices.Equal(got, want) {
				t.Errorf("checkChangelog() = %q, want %q", got, want)
			}
		})
	}
}

// The base is the newest release tag by semver, a prerelease included: a
// change two commits after it still counts, and a release tagged after
// the change takes it out of the next release's notes.
func TestTheChangelogRuleMeasuresFromTheNewestRelease(t *testing.T) {
	base := stalenessRepo(t)
	for _, tc := range []struct {
		name string
		// then runs in the checkout after the shipped change is committed.
		then func(t *testing.T, dir string)
		want []string
	}{
		{"a change two commits after the tag", func(t *testing.T, dir string) {
			stalenessChange(t, dir, map[string]string{"docs/x.md": "later\n"})
		}, []string{clEmpty}},
		{"a change a release then tags", func(t *testing.T, dir string) { stalenessGit(t, dir, "tag", "v0.9.1") }, nil},
		{"a change a prerelease then tags", func(t *testing.T, dir string) { stalenessGit(t, dir, "tag", "v1.0.0-rc.1") }, nil},
		{"a change older than the newest tag by date, not by version", func(t *testing.T, dir string) {
			stalenessGit(t, dir, "tag", "v0.8.9")
		}, []string{clEmpty}},
		// A release on another line is not HEAD's, even one that shipped the
		// same change.
		{"a newer release on another line with the same change", func(t *testing.T, dir string) {
			stalenessGit(t, dir, "checkout", "-q", "-b", "side", "v0.9.0")
			stalenessChange(t, dir, map[string]string{"internal/a/b.go": "package a\n\n// changed\n", "docs/x.md": "side\n"})
			stalenessGit(t, dir, "tag", "v9.9.9")
			stalenessGit(t, dir, "checkout", "-q", "main")
		}, []string{clEmpty}},
		// A tag that names no release is not one, however near.
		{"a tag that is no semver release", func(t *testing.T, dir string) { stalenessGit(t, dir, "tag", "v1.0.0--") }, []string{clEmpty}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := stalenessCopy(t, base)
			stalenessChange(t, dir, map[string]string{"internal/a/b.go": "package a\n\n// changed\n"})
			tc.then(t, dir)
			got, err := checkChangelog(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("checkChangelog() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A listing that names no command is not reading the binary.
func TestTheChangelogRuleNeedsTheBinary(t *testing.T) {
	dir := stalenessRepo(t)
	stalenessChange(t, dir, map[string]string{"cmd/fixture/main.go": "", "cmd/README.md": "moved\n"})
	_, err := checkChangelog(dir)
	want := "could not run: the CHANGELOG rule: go list -deps ./cmd/... named no package of this module, so it is not reading the binary"
	if !errors.As(err, new(couldNotRun)) || err == nil || err.Error() != want {
		t.Errorf("checkChangelog() = %v, want %q", err, want)
	}
}

// A git that cannot answer is no verdict, never "source changed".
func TestTheChangelogRuleNeedsAGitCheckout(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	_, err := checkChangelog(dir)
	if !errors.As(err, new(couldNotRun)) || exitCode(err) != 2 ||
		!strings.HasPrefix(err.Error(), "could not run: the CHANGELOG rule: git -C "+dir+" tag") {
		t.Errorf("outside git: %v; want a git tag failure that could not run", err)
	}
	// A checkout with no commit has no HEAD to read the releases of.
	dir = t.TempDir()
	stalenessGit(t, dir, "init", "-q", "-b", "main")
	_, err = checkChangelog(dir)
	if !errors.As(err, new(couldNotRun)) || err == nil ||
		!strings.HasPrefix(err.Error(), "could not run: the CHANGELOG rule: git -C "+dir+" tag --merged HEAD") {
		t.Errorf("a checkout with no commit: %v; want a git tag --merged failure that could not run", err)
	}
}

// With no release tag there is nothing to measure from, so the rule says
// why rather than measure from some other commit. A shallow clone fetches
// no tag, and the rule names it.
func TestTheChangelogRuleNeedsARelease(t *testing.T) {
	const noTag = "could not run: the CHANGELOG rule: no release tag (vX.Y.Z) HEAD reaches is in the checkout to measure from"
	base := stalenessRepo(t)
	stalenessChange(t, base, map[string]string{"internal/a/b.go": "package a\n\n// changed\n"})
	untagged := stalenessCopy(t, base)
	stalenessGit(t, untagged, "tag", "-d", "v0.9.0")
	shallow := t.TempDir()
	from := filepath.ToSlash(base)
	if !strings.HasPrefix(from, "/") {
		from = "/" + from
	}
	stalenessGit(t, shallow, "clone", "-q", "--depth=1", "--no-tags", "file://"+from, ".")
	for dir, want := range map[string]string{
		untagged: noTag,
		shallow:  noTag + "; the checkout is a shallow clone: check it out with fetch-depth: 0",
	} {
		_, err := checkChangelog(dir)
		if !errors.As(err, new(couldNotRun)) || err == nil || err.Error() != want {
			t.Errorf("checkChangelog() = %v, want %q", err, want)
		}
	}
}

// The CHANGELOG rule not running holds back no other check: the gate
// still holds the README and the configuration page, a real finding still
// fails it, and with none it reports only that the rule could not run.
func TestStalenessRunsEveryOtherCheckWithoutARelease(t *testing.T) {
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
	const unrun = "could not run: the CHANGELOG rule: no release tag (vX.Y.Z) HEAD reaches is in the checkout to measure from"
	const row = "| `get_file` |"
	for _, tc := range []struct {
		name     string
		readme   func(string) string
		out, err string
		code     int
	}{
		{"nothing else wrong", func(s string) string { return s }, "", unrun, 2},
		{"a tool missing from the README", func(s string) string {
			start := strings.Index(s, "\n"+row)
			if start < 0 {
				t.Fatalf("README.md has no row starting %q", row)
			}
			end := strings.IndexByte(s[start+1:], '\n')
			return s[:start] + s[start+1+end:]
		}, "README's tool table does not list the registered tool get_file\n" + unrun + "\n", "1 documentation problem(s)", 1},
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
			if err == nil || err.Error() != tc.err || exitCode(err) != tc.code || out.String() != tc.out {
				t.Errorf("staleness() = %v (exit %d), printing %q; want %q (exit %d), printing %q",
					err, exitCode(err), out.String(), tc.err, tc.code, tc.out)
			}
		})
	}
}

// The newest release is the highest version by semver, a prerelease
// included, and a tag that is not a version is no release.
func TestStalenessNewestRelease(t *testing.T) {
	for want, tags := range map[string][]string{
		"2.0.0-rc.1":  {"v1.9.0", "v2.0.0-rc.1"},
		"2.0.0":       {"v2.0.0-rc.1", "v2.0.0", "v1.10.0"},
		"1.10.0":      {"v1.9.0", "v1.10.0", "latest", "v11", "2.0.0"},
		"2.0.0-rc.10": {"v2.0.0-rc.2", "v2.0.0-rc.10"},
		// A prerelease identifier may hold a hyphen (https://semver.org/#spec-item-9).
		"2.0.0-rc-1": {"v1.9.0", "v2.0.0-rc-1"},
		"":           {"latest"},
	} {
		if got := stalenessNewestRelease(tags); got != want {
			t.Errorf("stalenessNewestRelease(%q) = %q, want %q", tags, got, want)
		}
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
