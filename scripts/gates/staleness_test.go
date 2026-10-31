package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
)

// TestStatusVersionsMatchTheChangelog runs the check against the real
// repository, so a status line left behind by a release fails here as
// well as in CI.
func TestStatusVersionsMatchTheChangelog(t *testing.T) {
	t.Chdir("../..")
	if problems := checkStatusVersions(); len(problems) > 0 {
		t.Errorf("status lines disagree with CHANGELOG.md:\n%s", strings.Join(problems, "\n"))
	}
}

// TestAStaleStatusLineIsCaught is the case that went unnoticed for two
// releases: the README said v0.3.0 while v0.4.0 and then v1.0.0 shipped,
// and every list inside it was correct the whole time.
func TestAStaleStatusLineIsCaught(t *testing.T) {
	cases := []struct {
		name    string
		readme  string
		arch    string
		want    string
		wantAny bool
	}{
		{
			name:   "the architecture status is behind",
			readme: "**Status: v1.0.0, phase 5 of the plan.**\n",
			arch:   "**Status:** phase 4 complete (2026-09-06), released as v0.4.0.\n",
			want:   "docs/architecture.md says the status is v0.4.0",
		},
		{
			name:   "a status line that no longer has the shape this reads",
			readme: "no status line here at all\n",
			arch:   "**Status:** phase 5 complete, released as v1.0.0.\n",
			want:   "docs/architecture.md has no status line naming a version",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "CHANGELOG.md"),
				"# Changelog\n\n## [1.0.0] - 2026-09-06\n\n### Added\n\n- something\n")
			write(t, filepath.Join(dir, "README.md"), c.readme)
			write(t, filepath.Join(dir, "docs", "architecture.md"), c.arch)
			t.Chdir(dir)

			problems := checkStatusVersions()
			if len(problems) == 0 {
				t.Fatal("a stale status line was accepted")
			}
			if !strings.Contains(strings.Join(problems, "\n"), c.want) {
				t.Errorf("the report does not say %q:\n%s", c.want, strings.Join(problems, "\n"))
			}
		})
	}
}

// TestStatusVersionsBeforeTheFirstRelease keeps the check quiet where
// there is nothing to be stale against: a changelog with no version
// heading yet is a repository that has never released.
func TestStatusVersionsBeforeTheFirstRelease(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "CHANGELOG.md"), "# Changelog\n\n## [Unreleased]\n\n- something\n")
	write(t, filepath.Join(dir, "README.md"), "**Status: pre-release.**\n")
	write(t, filepath.Join(dir, "docs", "architecture.md"), "**Status:** no code yet.\n")
	t.Chdir(dir)

	if problems := checkStatusVersions(); len(problems) > 0 {
		t.Errorf("an unreleased repository was reported as stale:\n%s", strings.Join(problems, "\n"))
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
