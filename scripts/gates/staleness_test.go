package main

import (
	"os"
	"path/filepath"
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
	clEmpty      = "source changed but CHANGELOG.md [Unreleased] is empty"
)

// The CHANGELOG rule: a change to Go source is written up under
// [Unreleased], or, on the release commit before its tag, under the
// version that commit releases.
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
		{"a release commit with nothing under its version", clUnreleased + clNextEmpty + clReleased, true,
			[]string{"0.9.0"},
			[]string{"source changed but CHANGELOG.md [0.9.1] (untagged, so this is a release commit) is empty"}},
		{"an undocumented source change after the release is tagged", clUnreleased + clNext + clReleased, true,
			[]string{"0.9.0", "0.9.1"}, []string{clEmpty}},
		{"no change to Go source", clUnreleased + clReleased, false, []string{"0.9.0"}, nil},
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

// What changelogRule is told comes from git: whether Go source differs
// from the last tag, which is not always the commit before HEAD, and
// whether a version is tagged. Each row is a repository whose v0.9.0 tag
// is followed by one more commit.
func TestTheChangelogRuleReadsGit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		changelog string
		// source and readme say which files the commit after v0.9.0 changes,
		// and later commits a change to the README alone after it.
		source, readme, later bool
		// tag tags that commit, and edit then changes Go source uncommitted.
		tag, edit bool
		want      []string
	}{
		{name: "an undocumented source change after the release is tagged", changelog: clUnreleased + clNext + clReleased,
			source: true, tag: true, edit: true, want: []string{clEmpty}},
		{name: "an undocumented source change two commits after the tag", changelog: clUnreleased + clReleased,
			source: true, later: true, want: []string{clEmpty}},
		{name: "a change to no Go source", changelog: clUnreleased + clReleased, readme: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateGit(t)
			dir := t.TempDir()
			git := func(args ...string) { t.Helper(); gitAs(t, dir, "a@example.com", args...) }
			put := func(name, body string) { t.Helper(); write(t, filepath.Join(dir, name), body) }
			git("init", "-q", "-b", "main")
			put("main.go", "package main\n")
			put("README.md", "# Fixture\n")
			put("CHANGELOG.md", clUnreleased+clReleased)
			git("add", "-A")
			git("commit", "-q", "-m", "Release 0.9.0")
			git("tag", "-a", "v0.9.0", "-m", "v0.9.0")

			if tc.source {
				put("main.go", "package main\n\nfunc main() {}\n")
			}
			if tc.readme {
				put("README.md", "# Fixture\n\nMore words.\n")
			}
			put("CHANGELOG.md", tc.changelog)
			git("add", "-A")
			git("commit", "-q", "-m", "The next change")
			if tc.later {
				put("README.md", "# Fixture\n\nA later change.\n")
				git("commit", "-q", "-am", "A later change")
			}
			if tc.tag {
				git("tag", "-a", "v0.9.1", "-m", "v0.9.1")
			}
			if tc.edit {
				put("main.go", "package main\n\nfunc main() { main() }\n")
			}

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
	} {
		t.Run(tc.name, func(t *testing.T) {
			if problems := configDocProblems(settings, tc.page); !slices.Contains(problems, tc.want) {
				t.Errorf("problems %q, want one %q", problems, tc.want)
			}
		})
	}
}
