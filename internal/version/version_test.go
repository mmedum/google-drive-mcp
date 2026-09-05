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
