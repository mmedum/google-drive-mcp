package main

import (
	"slices"
	"strings"
	"testing"
)

// The hook scans with the gitleaks module@version the Makefile's GITLEAKS
// names, so the version has one home and the pins gate holds it there.
func TestTheHookScansWithTheMakefilesGitleaks(t *testing.T) {
	for _, tc := range []struct {
		name, makefile string
		want           []string
		err            string
	}{
		{"the Makefile's module", "GL = github.com/zricethezav/gitleaks/v8@v8.1.0\nGITLEAKS ?= $(GL)\n",
			[]string{"go", "run", "github.com/zricethezav/gitleaks/v8@v8.1.0", "git", "--staged", "--redact", "--no-banner",
				"--config", ".gitleaks.toml", "."}, ""},
		{"no GITLEAKS", "GO ?= go\n", nil,
			"Makefile: GITLEAKS names no module@version, so the hook has no gitleaks to scan with"},
		{"GITLEAKS with no version", "GITLEAKS ?= github.com/zricethezav/gitleaks/v8\n", nil,
			"Makefile: GITLEAKS names no module@version, so the hook has no gitleaks to scan with"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := precommitGitleaks(parseMakefile(tc.makefile))
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			if !slices.Equal(got, tc.want) || msg != tc.err {
				t.Errorf("precommitGitleaks() = %q, %q; want %q, %q", got, msg, tc.want, tc.err)
			}
		})
	}
}

// The hook reports what the Makefile reader refuses, a file GNU make reads
// before the Makefile included, as the pins and parity gates report it.
func TestTheHookReportsWhatTheMakefileReaderRefuses(t *testing.T) {
	t.Chdir(t.TempDir())
	// A module no proxy is asked for fails at once, so the scan runs nothing.
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOFLAGS", "")
	write(t, "Makefile", "GITLEAKS ?= example.invalid/gitleaks@v0.0.1\n")
	write(t, "GNUmakefile", "include Makefile\n")
	var out strings.Builder
	err := precommit(&out, nil)
	const want = "GNUmakefile: GNU make reads it before Makefile, so `make` runs a file no gate reads; fold it into Makefile"
	if err == nil || !slices.Contains(strings.Split(out.String(), "\n"), want) {
		t.Errorf("precommit() = %v, printing:\n%s\nwant a problem line %q", err, out.String(), want)
	}
}
