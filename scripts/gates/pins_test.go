package main

import "testing"

// TestPinsShellDistinguishesWorkflowLevelFromJobLevel is the case the
// check exists for. A sibling repository pointed out that the first fix
// here was job-level, which reads as correct and is not: a job added
// later does not inherit it. So the shape that must fail is a file that
// sets the shell under a job.
func TestPinsShellDistinguishesWorkflowLevelFromJobLevel(t *testing.T) {
	cases := map[string]struct {
		source string
		want   bool
	}{
		"workflow level": {"name: ci\n\ndefaults:\n  run:\n    shell: bash\n\njobs:\n  test:\n    runs-on: x\n", true},
		"workflow level with a comment above it": {
			"name: ci\n\n# why\ndefaults:\n  run:\n    shell: bash\n\njobs:\n  test:\n    runs-on: x\n", true},
		"job level only": {
			"name: ci\n\njobs:\n  test:\n    runs-on: x\n    defaults:\n      run:\n        shell: bash\n", false},
		"defaults with no run": {"name: ci\n\ndefaults:\n  something: else\n\njobs:\n  test:\n    runs-on: x\n", false},
		"run with no shell":    {"name: ci\n\ndefaults:\n  run:\n    working-directory: ./x\n\njobs:\n  a:\n    runs-on: x\n", false},
		"nothing at all":       {"name: ci\n\njobs:\n  test:\n    runs-on: x\n", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := pinsShell(tc.source); got != tc.want {
				t.Errorf("pinsShell = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestATooldownloadedAtLatestIsNotPinned. The gate read the YAML inputs
// an action takes and nothing else, so a tool fetched by curl in a run
// step floated past it — and the first thing this repository copied from
// a primary source, the MCP registry's own publishing example, installs
// its publisher from `releases/latest/download`.
//
// `runs-on: ubuntu-latest` must not trip it: an image label is not a tool
// a release artifact comes out of, and a gate that cried wolf on every
// workflow in the world would be turned off within a day.
func TestAToolDownloadedAtLatestIsNotPinned(t *testing.T) {
	for _, c := range []struct {
		name, line string
		want       bool
	}{
		{"the registry's own documented example", `curl -L "https://github.com/x/y/releases/latest/download/z.tar.gz"`, true},
		{"a Go module at latest", `go run example.com/tool@latest`, true},
		{"a pinned download", `curl -L "https://github.com/x/y/releases/download/v1.8.1/z.tar.gz"`, false},
		{"a pinned module", `go run example.com/tool@v1.8.1`, false},
		{"a runner image", `    runs-on: ubuntu-latest`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := floatingDownload.MatchString(c.line); got != c.want {
				t.Errorf("floating = %v, want %v for %q", got, c.want, c.line)
			}
		})
	}
}
