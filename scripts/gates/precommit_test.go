package main

import (
	"slices"
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
