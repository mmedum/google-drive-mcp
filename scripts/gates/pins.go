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
	checked := 0
	for _, path := range files {
		source, err := os.ReadFile(path) //nolint:gosec // a path this repository owns
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		for i, line := range strings.Split(string(source), "\n") {
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
	_, _ = fmt.Fprintf(out, "pin check ok (%d tool versions, %d workflows, all pinning the shell)\n",
		checked, len(files))
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
