package main

import (
	"io"
	"strings"
	"testing"
)

func TestToolchainRefusesAVersionGovulncheckCannotRead(t *testing.T) {
	for _, tc := range []struct {
		version string
		// way is the way out the refusal names, which is its contract;
		// "" means the version passes.
		way string
	}{
		{"go1.27.4", ""},
		{"go1.28rc1", ""},
		{"go1.21-pre1", ""},
		{"go1.27.4-X:nodwarf5", "GOTOOLCHAIN=go1.27.4 "},
		{"go1.27.4 X:nodwarf5", "GOTOOLCHAIN=go1.27.4 "},
		{"devel go1.28-abc123 Tue Nov 3 2026", "install a stock Go release"},
	} {
		err := checkToolchain(io.Discard, tc.version)
		switch {
		case tc.way == "" && err != nil:
			t.Errorf("checkToolchain(%q) = %v, want nil", tc.version, err)
		case tc.way != "" && (err == nil || !strings.Contains(err.Error(), tc.way)):
			t.Errorf("checkToolchain(%q) = %v, want a refusal naming %q", tc.version, err, tc.way)
		}
	}
}

func TestToolchainParserMatchesTheGovulncheckPin(t *testing.T) {
	t.Chdir("../..")
	mk, err := readMakefile()
	if err != nil {
		t.Fatal(err)
	}
	if got := mk.vars["GOVULNCHECK"]; got != govulncheckParsed {
		t.Errorf("Makefile pins GOVULNCHECK = %q; govulncheckReads copies the parser of %q: "+
			"recheck it against the new version, then move govulncheckParsed", got, govulncheckParsed)
	}
}
