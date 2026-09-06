package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestParityAcceptsThisRepository is the gate run against the real
// files, so a drift introduced later fails here as well as in CI.
func TestParityAcceptsThisRepository(t *testing.T) {
	t.Chdir("../..")
	var out bytes.Buffer
	if err := parity(&out, nil); err != nil {
		t.Errorf("parity(.) = %v\n%s", err, out.String())
	}
}

// TestParityCatchesDriftInEitherDirection is the point of the gate: the
// two lists live in different files, and whichever one you are editing
// is the one you remember.
func TestParityCatchesDriftInEitherDirection(t *testing.T) {
	const makefile = `GO ?= go

.PHONY: leaks
leaks:
	$(GO) run ./scripts/gates leaks

.PHONY: pins
pins:
	$(GO) run ./scripts/gates pins

.PHONY: api-diff
api-diff:
	$(GO) run ./scripts/gates api-diff

check: leaks pins
`
	cases := []struct {
		name string
		ci   string
		want string
	}{
		{
			name: "a gate CI never runs",
			ci:   "      - run: go run ./scripts/gates leaks\n",
			want: "pins runs in `make check` and not in ci.yml",
		},
		{
			name: "a gate make check never runs",
			ci: "      - run: go run ./scripts/gates leaks\n" +
				"      - run: go run ./scripts/gates pins\n" +
				"      - run: go run ./scripts/gates schema-diff ./bin\n",
			want: "schema-diff runs in ci.yml and not in `make check`",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "Makefile"), makefile)
			write(t, filepath.Join(dir, ".github", "workflows", "ci.yml"), c.ci)
			t.Chdir(dir)

			var out bytes.Buffer
			err := parityAgainst(&out, []string{"leaks", "pins", "schema-diff"})
			if err == nil {
				t.Fatalf("parity accepted a drifted pair:\n%s", out.String())
			}
			if !strings.Contains(out.String(), c.want) {
				t.Errorf("the report does not name the drift %q:\n%s", c.want, out.String())
			}
		})
	}

	// And the matching pair passes, so the two cases above are failing
	// on the drift rather than on the fixture.
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Makefile"), makefile)
	write(t, filepath.Join(dir, ".github", "workflows", "ci.yml"),
		"      - run: go run ./scripts/gates leaks\n      - run: go run ./scripts/gates pins\n")
	t.Chdir(dir)
	var out bytes.Buffer
	if err := parityAgainst(&out, []string{"leaks", "pins"}); err != nil {
		t.Errorf("parity rejected a matching pair: %v\n%s", err, out.String())
	}
}

// TestParityFollowsPrerequisitesTransitively is the false-red this
// nearly shipped: a gate hung off a second-level target — `cover`
// depends on `test` — is still run by `make check`, and a parity check
// reading only the direct prerequisites would report it as CI-only and
// fail a repository that is correct.
func TestParityFollowsPrerequisitesTransitively(t *testing.T) {
	const makefile = `GO ?= go

.PHONY: deep
deep: ## a target with its own help comment
	$(GO) run ./scripts/gates leaks

.PHONY: shallow
shallow: deep
	@echo nothing

check: shallow
`
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Makefile"), makefile)
	write(t, filepath.Join(dir, ".github", "workflows", "ci.yml"),
		"      - run: go run ./scripts/gates leaks\n")
	t.Chdir(dir)

	var out bytes.Buffer
	if err := parityAgainst(&out, []string{"leaks"}); err != nil {
		t.Errorf("a gate reached through a second-level target was not seen: %v\n%s",
			err, out.String())
	}
}

// TestParityNoticesAGateNothingRuns is the third direction, and the one
// two derived lists compared with each other cannot see: a gate this
// program implements and neither the Makefile nor CI invokes reads
// exactly like a gate nobody wrote. `gates classes` closes the same hole
// by checking against the code rather than against another list.
func TestParityNoticesAGateNothingRuns(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Makefile"), "GO ?= go\n\nleaks:\n\t$(GO) run ./scripts/gates leaks\n\ncheck: leaks\n")
	write(t, filepath.Join(dir, ".github", "workflows", "ci.yml"),
		"      - run: go run ./scripts/gates leaks\n")
	t.Chdir(dir)

	var out bytes.Buffer
	if err := parityAgainst(&out, []string{"leaks", "pins"}); err == nil {
		t.Fatalf("parity accepted a repository running only one of its gates:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "nothing runs it") {
		t.Errorf("the report does not name a gate nothing runs:\n%s", out.String())
	}
}

// TestParityRefusesToCompareNothing keeps the check from passing because
// it read the wrong file: two empty lists are equal.
func TestParityRefusesToCompareNothing(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Makefile"), "check:\n")
	write(t, filepath.Join(dir, ".github", "workflows", "ci.yml"), "jobs:\n")
	t.Chdir(dir)

	var out bytes.Buffer
	if err := parity(&out, nil); err == nil {
		t.Error("parity passed with no gates found in either file")
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestAGateThatIsNamedButNotRunDoesNotCount is the hole a sibling
// repository found in its own port of this gate, and it is the way a
// parity check gets defeated in practice: a step commented out to
// unblock a red build still matched, so the gate stayed green across
// exactly the divergence it exists to catch.
//
// Four ways a gate's name can be in the workflow file without running.
func TestAGateThatIsNamedButNotRunDoesNotCount(t *testing.T) {
	cases := []struct {
		name, ci string
		wantRun  bool
	}{
		{
			name:    "an ordinary step",
			ci:      "jobs:\n  a:\n    steps:\n      - run: go run ./scripts/gates leaks\n",
			wantRun: true,
		},
		{
			name: "commented out to unblock a red build",
			ci:   "jobs:\n  a:\n    steps:\n      # - run: go run ./scripts/gates leaks\n",
		},
		{
			name: "named in a comment explaining why it is not there",
			ci:   "jobs:\n  a:\n    steps:\n      # go run ./scripts/gates leaks needs credentials\n",
		},
		{
			name: "named in a step's name rather than run by it",
			ci:   "jobs:\n  a:\n    steps:\n      - name: go run ./scripts/gates leaks\n        run: echo skipped\n",
		},
		{
			name: "inside a multi-line run block, which does run it",
			ci: "jobs:\n  a:\n    steps:\n      - run: |\n          set -e\n" +
				"          go run ./scripts/gates leaks\n",
			wantRun: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ci.yml")
			if err := os.WriteFile(path, []byte(c.ci), 0o600); err != nil {
				t.Fatal(err)
			}
			gates, err := gatesInWorkflow(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := slices.Contains(gates, "leaks"); got != c.wantRun {
				t.Errorf("leaks running = %v, want %v (read %v)", got, c.wantRun, gates)
			}
		})
	}
}

// TestACommentedOutRecipeLineIsNotARecipeLine. Make hands an indented
// `#` to the shell, which does nothing with it; the Makefile side had
// the same hole as the workflow side and for the same reason.
func TestACommentedOutRecipeLineIsNotARecipeLine(t *testing.T) {
	const makefile = `GO ?= go

.PHONY: leaks
leaks:
	$(GO) run ./scripts/gates leaks

.PHONY: pins
pins:
#	$(GO) run ./scripts/gates pins

check: leaks pins
`
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	if err := os.WriteFile(path, []byte(makefile), 0o600); err != nil {
		t.Fatal(err)
	}
	gates, err := gatesInCheck(path)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(gates, "pins") {
		t.Errorf("a commented-out recipe line was read as a gate that runs: %v", gates)
	}
	if !slices.Contains(gates, "leaks") {
		t.Errorf("the recipe line that is not commented out was missed: %v", gates)
	}
}
