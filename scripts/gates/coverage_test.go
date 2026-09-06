package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeProfile writes a synthetic coverage profile.
func writeProfile(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cov.out")
	body := "mode: atomic\n" + strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	return path
}

func TestReadProfileDeduplicatesBlocks(t *testing.T) {
	// -coverpkg=./internal/... emits the same block once per test binary
	// that could have run it. Counting it twice would inflate the total;
	// treating one miss as decisive would deflate the coverage.
	const block = modulePath + "/internal/ref/ref.go:10.1,12.2 3 "
	path := writeProfile(t, block+"0", block+"5")
	statements, hit, err := readProfile(path)
	if err != nil {
		t.Fatalf("readProfile: %v", err)
	}
	if len(statements) != 1 {
		t.Fatalf("got %d blocks, want 1", len(statements))
	}
	for b, n := range statements {
		if n != 3 {
			t.Errorf("statements = %d, want 3", n)
		}
		if !hit[b] {
			t.Error("a block reached by any binary counts as covered")
		}
	}
}

func TestCoverageMeasuresAPackageOnItsOwnFiles(t *testing.T) {
	// internal/gapi/drivetest is a package of its own. Folding its
	// coverage into internal/gapi would let a well-tested fake hide an
	// untested client.
	path := writeProfile(t,
		modulePath+"/internal/gapi/client.go:1.1,2.2 10 0",
		modulePath+"/internal/gapi/drivetest/drivetest.go:1.1,2.2 10 1",
	)
	statements, hit, err := readProfile(path)
	if err != nil {
		t.Fatalf("readProfile: %v", err)
	}
	var counted int
	prefix := modulePath + "/internal/gapi/"
	for b, n := range statements {
		rest := strings.TrimPrefix(b.file, prefix)
		if !strings.HasPrefix(b.file, prefix) || strings.Contains(rest, "/") {
			continue
		}
		counted += n
		if hit[b] {
			t.Error("the nested package's hit leaked into internal/gapi")
		}
	}
	if counted != 10 {
		t.Errorf("counted %d statements for internal/gapi, want only its own 10", counted)
	}
}

func TestReadProfileRejectsRubbish(t *testing.T) {
	for _, bad := range []string{"not a profile line", modulePath + "/x.go:1.1,2.2 notanumber 1"} {
		if _, _, err := readProfile(writeProfile(t, bad)); err == nil {
			t.Errorf("readProfile accepted %q", bad)
		}
	}
	if _, _, err := readProfile(writeProfile(t)); err == nil {
		t.Error("an empty profile should be an error, not 0% everywhere")
	}
	if _, _, err := readProfile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing profile should be an error")
	}
}

func TestCoverageFailsBelowTheFloor(t *testing.T) {
	packages, err := corePackages()
	if err != nil {
		t.Fatalf("corePackages: %v", err)
	}
	lines := make([]string, 0, len(packages))
	for _, pkg := range packages {
		lines = append(lines, modulePath+"/"+pkg+"/a.go:1.1,2.2 10 1")
	}
	var out strings.Builder
	if err := coverage(&out, []string{writeProfile(t, lines...), "80"}); err != nil {
		t.Fatalf("fully covered packages should pass: %v", err)
	}
	// One package at 50% has to fail even though the rest are perfect.
	lines[0] = modulePath + "/" + packages[0] + "/a.go:1.1,2.2 10 0"
	lines = append(lines, modulePath+"/"+packages[0]+"/b.go:1.1,2.2 10 1")
	out.Reset()
	err = coverage(&out, []string{writeProfile(t, lines...), "80"})
	if err == nil {
		t.Fatal("a package below the floor should fail the gate")
	}
	if !strings.Contains(err.Error(), packages[0]) {
		t.Errorf("the failure should name the package: %v", err)
	}
}

func TestEveryPackageUnderInternalIsMeasuredOrExemptOnPurpose(t *testing.T) {
	// The list of what is measured is derived, so the thing worth
	// asserting is the other half: that every exemption still names a
	// package that exists, and that nothing under internal/ is missing
	// from both lists.
	packages, err := corePackages()
	if err != nil {
		t.Fatalf("corePackages: %v", err)
	}
	measured := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		measured[pkg] = true
		if _, err := os.Stat(filepath.Join("..", "..", pkg)); err != nil {
			t.Errorf("measured package %s is not present: %v", pkg, err)
		}
	}
	for pkg, why := range exemptPackages {
		if why == "" {
			t.Errorf("%s is exempt with no reason given", pkg)
		}
		if _, err := os.Stat(filepath.Join("..", "..", pkg)); err != nil {
			t.Errorf("exempt package %s no longer exists, so the exemption is stale: %v", pkg, err)
		}
		if measured[pkg] {
			t.Errorf("%s is both measured and exempt", pkg)
		}
	}
	// The one that matters: a package under internal/ that nobody has
	// decided about. It would otherwise be unmeasured and look exactly
	// like a package that does not exist.
	out, err := exec.Command("go", "list", modulePath+"/internal/...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pkg := strings.TrimPrefix(strings.TrimSpace(line), modulePath+"/")
		if pkg == "" || measured[pkg] || exemptPackages[pkg] != "" {
			continue
		}
		t.Errorf("%s is neither measured nor exempt", pkg)
	}
}

func TestPinsRejectsEveryRangeForm(t *testing.T) {
	// The forms that have actually appeared here, plus the ones that
	// would. "~> v2.18.0" is the one that matters: it reads like a pin,
	// was recorded in the evidence log as the fix, and survived a review
	// looking straight at it.
	for _, bad := range []string{"~> v2.18.0", "~> v2", "latest", "v2", "v2.18", "nightly", "^1.2.3"} {
		if exactVersion.MatchString(strings.Trim(bad, `"`)) {
			t.Errorf("%q was accepted as an exact version", bad)
		}
	}
	for _, good := range []string{"v2.18.0", "1.51.1", "v3.1.3", "v2.13.2", "v1.0.0-rc.1"} {
		if !exactVersion.MatchString(good) {
			t.Errorf("%q was rejected, and it is one exact version", good)
		}
	}
	// go-version-file names a file rather than a version, and go.mod is
	// the pin: matching it would fail the gate on every workflow here.
	for _, line := range []string{"          go-version-file: go.mod"} {
		if versionInput.MatchString(line) {
			t.Errorf("%q was read as a tool version", line)
		}
	}
	for _, line := range []string{
		`          version: "v2.18.0"`,
		"          cosign-release: v3.1.3",
		"          syft-version: v1.51.1",
	} {
		if !versionInput.MatchString(line) {
			t.Errorf("%q was not read as a tool version", line)
		}
	}
}

func TestPinsPassesOnThisRepositoryAndSaysWhatItChecked(t *testing.T) {
	// The gate runs from the repository root; the tests run from the
	// package directory.
	t.Chdir("../..")
	var out strings.Builder
	if err := pins(&out, nil); err != nil {
		t.Fatalf("pins: %v\n%s", err, out.String())
	}
	// A check that silently examined nothing would report success too.
	if !strings.Contains(out.String(), "tool versions") || !strings.Contains(out.String(), "workflows") {
		t.Errorf("the gate does not say how much it checked: %q", out.String())
	}
}
