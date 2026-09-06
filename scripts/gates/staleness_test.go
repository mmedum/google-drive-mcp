package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			name:   "the README is behind",
			readme: "**Status: v0.3.0, phase 3 of the plan.**\n",
			arch:   "**Status:** phase 5 complete (2026-09-06), released as v1.0.0.\n",
			want:   "README.md says the status is v0.3.0",
		},
		{
			name:   "the architecture status is behind",
			readme: "**Status: v1.0.0, phase 5 of the plan.**\n",
			arch:   "**Status:** phase 4 complete (2026-09-06), released as v0.4.0.\n",
			want:   "docs/architecture.md says the status is v0.4.0",
		},
		{
			name:   "a status line that no longer has the shape this reads",
			readme: "Status: somewhere around v1.0.0 probably\n",
			arch:   "**Status:** phase 5 complete (2026-09-06), released as v1.0.0.\n",
			want:   "README.md has no status line naming a version",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "CHANGELOG.md"),
				"# Changelog\n\n## [1.0.0] - 2026-09-06\n\n### Added\n\n- something\n")
			writeFile(t, filepath.Join(dir, "README.md"), c.readme)
			writeFile(t, filepath.Join(dir, "docs", "architecture.md"), c.arch)
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
	writeFile(t, filepath.Join(dir, "CHANGELOG.md"), "# Changelog\n\n## [Unreleased]\n\n- something\n")
	writeFile(t, filepath.Join(dir, "README.md"), "**Status: pre-release.**\n")
	writeFile(t, filepath.Join(dir, "docs", "architecture.md"), "**Status:** no code yet.\n")
	t.Chdir(dir)

	if problems := checkStatusVersions(); len(problems) > 0 {
		t.Errorf("an unreleased repository was reported as stale:\n%s", strings.Join(problems, "\n"))
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
