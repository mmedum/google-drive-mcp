package main

import (
	"io"
	"strings"
	"testing"
)

func TestToolchainRefusesAnyGoButGoModsRelease(t *testing.T) {
	for _, tc := range []struct {
		version, pinned string
		// way is the way out the refusal names, which is its contract;
		// "" means the version passes.
		way string
	}{
		{"go1.27.4", "go1.27.4", ""},
		{"go1.27.3", "go1.27.4", "run with GOTOOLCHAIN=go1.27.4"},
		// A distribution's build of the same release, which govulncheck
		// cannot read.
		{"go1.27.4-X:nodwarf5", "go1.27.4", "run with GOTOOLCHAIN=go1.27.4"},
		{"go1.27.4", "go1.27.3", "set it to go1.27.4"},
	} {
		err := checkToolchain(io.Discard, tc.version, "go1.27.4", tc.pinned)
		switch {
		case tc.way == "" && err != nil:
			t.Errorf("checkToolchain(%q, pinned %q) = %v, want nil", tc.version, tc.pinned, err)
		case tc.way != "" && (err == nil || !strings.Contains(err.Error(), tc.way)):
			t.Errorf("checkToolchain(%q, pinned %q) = %v, want a refusal naming %q", tc.version, tc.pinned, err, tc.way)
		}
	}
}
