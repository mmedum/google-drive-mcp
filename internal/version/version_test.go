package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestStringPrefersLDFlagsVersion(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "v1.2.3"
	if got := String(); got != "v1.2.3" {
		t.Fatalf("String() = %q, want v1.2.3", got)
	}
}

func TestStringFallsBackToBuildInfo(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "dev"
	// A test binary reports "(devel)" or an empty main version, so the
	// honest answer here is "dev" rather than a made-up number.
	if got := String(); got != "dev" {
		t.Fatalf("String() = %q, want dev in a test binary", got)
	}
}

func TestInfoNamesTheBinaryAndPlatform(t *testing.T) {
	got := Info()
	for _, want := range []string{"google-drive-mcp", runtime.Version(), runtime.GOOS, runtime.GOARCH} {
		if !strings.Contains(got, want) {
			t.Errorf("Info() = %q, missing %q", got, want)
		}
	}
}

// TestOneSpellingWhicheverWayItWasBuilt is the drift a reader found by
// comparing five servers side by side: goreleaser stamps its own
// {{.Version}}, which has the leading v stripped, while `go install`
// leaves the fallback to read "v1.1.0" out of the build info. The same
// release reported two different strings depending on how it was
// installed, and the README promises it "reports the release it came
// from either way".
func TestOneSpellingWhicheverWayItWasBuilt(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"1.1.0", "v1.1.0"},  // goreleaser's stamping
		{"v1.1.0", "v1.1.0"}, // the build-info fallback
		{"1.1.0-rc.1", "v1.1.0-rc.1"},
		{"dev", "dev"}, // an untagged build says so
		{"", ""},
	} {
		if got := canonical(tc.in); got != tc.want {
			t.Errorf("canonical(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
