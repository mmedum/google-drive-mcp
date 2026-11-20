package main

import (
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

// govulncheckParsed is the govulncheck whose version parser
// govulncheckReads copies; a test holds the Makefile's pin to it, so a
// bump rechecks the copy.
const govulncheckParsed = "golang.org/x/vuln/cmd/govulncheck@v1.7.0"

// govulncheckReads is the Go version govulncheck v1.7.0 can place. It
// gives any other version, such as a distribution's build with a
// GOEXPERIMENT suffix, the standard library version "", and then
// reports no standard-library advisory at all, without saying so
// (https://github.com/golang/vuln/blob/v1.7.0/internal/semver/semver.go#L62,
// https://github.com/golang/vuln/blob/v1.7.0/internal/vulncheck/vulncheck.go#L194-L202).
var govulncheckReads = regexp.MustCompile(`^go(\d+\.\d+)(\.\d+|)((beta|rc|-pre)(\d+))?$`)

// toolchain refuses a Go toolchain govulncheck would silently skip, so
// `make vuln` cannot pass on a machine where it checked nothing in the
// standard library.
func toolchain(out io.Writer, _ []string) error {
	// The go on PATH, as govulncheck v1.7.0 itself runs it
	// (https://github.com/golang/vuln/blob/v1.7.0/internal/vulncheck/packages.go#L37).
	v, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		return couldNotRun{fmt.Errorf("reading go env GOVERSION: %w", err)}
	}
	return checkToolchain(out, strings.TrimSpace(string(v)))
}

func checkToolchain(out io.Writer, version string) error {
	if govulncheckReads.MatchString(version) {
		_, _ = fmt.Fprintf(out, "toolchain ok (%s, which govulncheck reads)\n", version)
		return nil
	}
	way := "install a stock Go release"
	// A suffix after the release, such as -X:nodwarf5, leaves a release
	// GOTOOLCHAIN can fetch; a devel build leaves none.
	release, _, _ := strings.Cut(strings.NewReplacer(" ", "-", "+", "-").Replace(version), "-")
	if govulncheckReads.MatchString(release) {
		way = "run with GOTOOLCHAIN=" + release + " to use the stock build"
	}
	return fmt.Errorf("govulncheck cannot read Go version %q, so it would check nothing in the standard library; %s",
		version, way)
}
