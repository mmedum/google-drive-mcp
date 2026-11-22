package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// toolchain refuses any Go but go.mod's own stock release. govulncheck
// v1.7.0 gives any other version, such as a distribution's build with a
// GOEXPERIMENT suffix, the standard library version "", and then reports
// no standard-library advisory at all, without saying so
// (https://github.com/golang/vuln/blob/v1.7.0/internal/semver/semver.go#L62,
// https://github.com/golang/vuln/blob/v1.7.0/internal/vulncheck/vulncheck.go#L194-L202).
func toolchain(out io.Writer, _ []string) error {
	gomod, err := os.ReadFile("go.mod")
	if err != nil {
		return couldNotRun{fmt.Errorf("reading go.mod: %w", err)}
	}
	release := pinsGoVersion(string(gomod))
	if release == "" {
		return couldNotRun{errors.New("go.mod names no Go release")}
	}
	mk, err := readMakefile()
	if err != nil {
		return couldNotRun{err}
	}
	// The go on PATH, as govulncheck v1.7.0 itself runs it
	// (https://github.com/golang/vuln/blob/v1.7.0/internal/vulncheck/packages.go#L37).
	v, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		return couldNotRun{fmt.Errorf("reading go env GOVERSION: %w", err)}
	}
	return checkToolchain(out, strings.TrimSpace(string(v)), "go"+release, mk.expand("$(GOTOOLCHAIN)"))
}

// checkToolchain holds version, the go that runs, and pinned, the
// Makefile's GOTOOLCHAIN, to want, the release go.mod names.
func checkToolchain(out io.Writer, version, want, pinned string) error {
	if pinned != want {
		return fmt.Errorf("%s sets GOTOOLCHAIN to %q, and go.mod runs %s; set it to %s", makefilePath, pinned, want, want)
	}
	if version != want {
		return fmt.Errorf("go is %q, and go.mod runs %s; run with GOTOOLCHAIN=%s, as make does, to fetch the stock build",
			version, want, want)
	}
	_, _ = fmt.Fprintf(out, "toolchain ok (%s, go.mod's release)\n", version)
	return nil
}
