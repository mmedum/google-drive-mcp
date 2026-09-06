package main

import (
	"bytes"
	"os"
	"path/filepath"
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
