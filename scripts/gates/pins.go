package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// pins fails when a workflow installs a tool at a version that is not
// exactly one version.
//
// It exists because this repository has made the same mistake twice and
// written the second one down as the fix. An action pinned by commit SHA
// still installs a tool whose version is a separate input, and `~> v2`
// let that tool float across a whole major version. Narrowing it to
// `~> v2.18.0` read like a pin, was recorded in the evidence log as one,
// and survived a review looking straight at it — because a narrowed
// range and a pin look identical unless somebody reads the action's
// documentation. `~>` is a max-satisfying constraint whatever follows it.
//
// A comment cannot hold this shut; the comment beside the wrong value
// said which half of the pin mattered and the value was still wrong. So
// it is a gate: the thing that decides what a release artifact is has to
// be one version, spelled out.
func pins(out io.Writer, _ []string) error {
	files, err := filepath.Glob(".github/workflows/*.yml")
	if err != nil {
		return err
	}
	if len(files) == 0 {
		// A check that silently examines nothing is worse than no check:
		// it reports success for a directory that has been renamed.
		return fmt.Errorf("no workflows found under .github/workflows; has the path changed?")
	}
	sort.Strings(files)

	var problems []string
	checked, actions, installers := 0, 0, 0
	for _, path := range files {
		source, err := os.ReadFile(path) //nolint:gosec // a path this repository owns
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		a, i, found := actionPins(path, string(source))
		actions += a
		installers += i
		problems = append(problems, found...)
		for i, line := range strings.Split(string(source), "\n") {
			// A tool downloaded in a run step floats just as a version
			// input does, and this gate could not see one: it read the
			// YAML inputs an action takes and nothing else. The MCP
			// registry's own published example installs its publisher
			// from `releases/latest/download`, so the first thing this
			// repository copied from a primary source would have been
			// the first thing to float past the gate that exists to stop
			// exactly that.
			if floating := floatingDownload.FindString(line); floating != "" {
				checked++
				problems = append(problems, fmt.Sprintf(
					"%s:%d: %s installs whatever is newest. Name the version: what builds a "+
						"release must not float.", path, i+1, strings.TrimSpace(floating)))
				continue
			}
			m := versionInput.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			checked++
			value := strings.Trim(strings.TrimSpace(m[2]), `"'`)
			if exactVersion.MatchString(value) {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"%s:%d: %s is %q, which is a range rather than a version. "+
					"Write one exact version: what builds a release must not float.",
				path, i+1, strings.TrimSpace(m[1]), value))
		}
	}
	if checked == 0 {
		return fmt.Errorf("no tool versions found in %d workflow(s); has the input naming changed?", len(files))
	}
	// Each count can go to zero on its own, and a zero here reads as a
	// pass rather than as the gate having stopped looking.
	if actions < 5 {
		return fmt.Errorf("found %d action references in %d workflow(s); the gate is not reading them",
			actions, len(files))
	}
	if installers < 3 {
		return fmt.Errorf("found %d tool installers; the classification table has probably drifted "+
			"from the actions the workflows use", installers)
	}
	for _, path := range files {
		source, err := os.ReadFile(path) //nolint:gosec // a path this repository owns
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if !pinsShell(string(source)) {
			problems = append(problems, fmt.Sprintf(
				"%s: no workflow-level `defaults: run: shell:`. The Windows runner's default shell is "+
					"PowerShell and it does not read every command line the way bash does; one line has to "+
					"mean one thing on every runner. Job level does not count: a job added later would not "+
					"inherit it.", path))
		}
	}
	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, p)
		}
		return fmt.Errorf("%d unpinned tool version(s)", len(problems))
	}
	_, _ = fmt.Fprintf(out, "pin check ok (%d tool versions, %d actions by SHA, %d installers naming "+
		"their tool's version, %d workflows, all pinning the shell)\n",
		checked, actions, installers, len(files))
	return nil
}

// pinsShell reports whether a workflow sets the shell at WORKFLOW level.
//
// It is a line scan rather than a YAML parse because the distinction it
// has to make is positional: `defaults:` at column zero applies to every
// job in the file, and the same three lines indented under a job apply
// to that job alone. A parser would answer "is there a defaults block"
// just as easily and that is the question that passes when the answer
// should be no.
func pinsShell(source string) bool {
	inDefaults, inRun := false, false
	for _, line := range strings.Split(source, "\n") {
		switch {
		case line == "defaults:":
			inDefaults, inRun = true, false
		case inDefaults && line == "  run:":
			inRun = true
		case inRun && strings.HasPrefix(line, "    shell:"):
			return true
		case line != "" && !strings.HasPrefix(line, " "):
			// Any other top-level key ends the block.
			inDefaults, inRun = false, false
		case inRun && !strings.HasPrefix(line, "    "):
			inRun = false
		}
	}
	return false
}

// versionInput matches the inputs that name the version of a tool an
// action installs. `go-version-file` is deliberately not among them: it
// points at go.mod, which is the pin.
var versionInput = regexp.MustCompile(`^\s*((?:[a-z-]+-)?(?:version|release)):\s*(\S.*?)\s*$`)

// exactVersion is one version and nothing else: no `~>`, no `^`, no
// `latest`, no bare major.
var exactVersion = regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)

// floatingDownload matches a tool fetched at whatever version is newest.
//
// Two spellings, because both appear in the wild and neither is a
// version: a GitHub "latest release" download URL, and a Go module
// installed at @latest. `runs-on: ubuntu-latest` is not one of them — an
// image label is not a tool this repository ships an artifact from — so
// the patterns are anchored to the shapes that fetch something.
var floatingDownload = regexp.MustCompile(`releases/latest/download|@latest\b`)

// The rule above judges a version input that is *written*. Two things it
// cannot see, both of which this repository was relying on habit for:
//
//   - an action referenced by a mutable tag rather than a commit SHA.
//     The doc comment at the top of this file already assumes SHA
//     pinning ("an action pinned by commit SHA still installs a tool
//     whose version is a separate input") and nothing checked it. Every
//     action here is SHA-pinned today; the four sibling servers all hold
//     that with a gate and this one did not.
//
//   - an action that installs a tool and names no version at all. That
//     is an absence, not a value, so a check over written versions is
//     blind to it. The Pipedrive server's release published nothing on
//     exactly this shape: `sigstore/cosign-installer` pinned by SHA with
//     no `cosign-release`, so the job installed whatever cosign was
//     newest, and that cosign had changed its default signing format.
//     `anchore/sbom-action/download-syft` had the same hole one step
//     below it. A SHA pins the wrapper, not the tool.
var (
	usesRef = regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*([^\s#]+)`)
	fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

	// installerPins maps an action to the input keys that pin the tool
	// it installs. Any one of them satisfies the rule.
	installerPins = map[string][]string{
		"sigstore/cosign-installer":         {"cosign-release"},
		"anchore/sbom-action/download-syft": {"syft-version"},
		"goreleaser/goreleaser-action":      {"version"},
		"golangci/golangci-lint-action":     {"version"},
		// A file is a pin by reference, and a better one: go.mod cannot
		// disagree with itself the way two literals can.
		"actions/setup-go": {"go-version-file", "go-version"},
		// This wrapper has never had a version input; it reads the
		// scanner's version from the environment.
		"gitleaks/gitleaks-action": {"GITLEAKS_VERSION"},
	}

	// notInstallers are the actions that install no tool, each with the
	// reason, so that adding one is a decision rather than an omission.
	notInstallers = map[string]string{
		"actions/checkout":                "checks out the repository",
		"actions/upload-artifact":         "uploads, installs nothing",
		"actions/download-artifact":       "downloads, installs nothing",
		"actions/attest-build-provenance": "calls the attestation API",
		"github/codeql-action/init":       "CodeQL's bundle is GitHub's to manage",
		"github/codeql-action/analyze":    "CodeQL's bundle is GitHub's to manage",
	}
)

// actionPins reports every action reference that floats: pinned to a tag
// rather than a SHA, installing a tool whose version nobody named, or
// belonging to no classification at all.
func actionPins(path, source string) (actions, installers int, problems []string) {
	for _, loc := range usesRef.FindAllStringSubmatchIndex(source, -1) {
		ref := source[loc[2]:loc[3]]
		if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "docker://") {
			continue // a local action carries no version of its own
		}
		actions++
		action, rev, ok := strings.Cut(ref, "@")
		if !ok || !fullSHA.MatchString(rev) {
			problems = append(problems, fmt.Sprintf(
				"%s: %s is not pinned to a full commit SHA. A tag can be moved under you.", path, ref))
		}
		keys, isInstaller := installerPins[action]
		if !isInstaller {
			if _, known := notInstallers[action]; !known {
				problems = append(problems, fmt.Sprintf(
					"%s: %s is not classified in scripts/gates/pins.go — add it to installerPins "+
						"with the input that pins its tool, or to notInstallers with the reason. "+
						"A SHA pins the wrapper, not the tool.", path, action))
			}
			continue
		}
		installers++
		if !namesAnyKey(stepBlock(source, loc[0]), keys) {
			problems = append(problems, fmt.Sprintf(
				"%s: %s is pinned by SHA but the tool it installs is not — set %s. "+
					"A SHA pins the wrapper, not the tool.", path, action, strings.Join(keys, " or ")))
		}
	}
	return actions, installers, problems
}

// stepBlock returns the lines belonging to the step whose `uses:` line
// starts at start, so that a `with:` or `env:` key is read from the step
// that owns it rather than from the next one down the file.
func stepBlock(source string, start int) string {
	lines := strings.Split(source[start:], "\n")
	base := leadingSpace(lines[0])
	var out []string
	for i, ln := range lines {
		if i > 0 && strings.TrimSpace(ln) != "" {
			in := leadingSpace(ln)
			if in < base || (in == base && strings.HasPrefix(strings.TrimSpace(ln), "- ")) {
				break
			}
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// leadingSpace counts the leading whitespace. A space and a tab each
// count as one.
func leadingSpace(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// namesAnyKey reports whether the step sets one of the given keys.
func namesAnyKey(block string, keys []string) bool {
	for _, k := range keys {
		if regexp.MustCompile(`(?mi)^\s*` + regexp.QuoteMeta(k) + `:\s*\S`).MatchString(block) {
			return true
		}
	}
	return false
}
