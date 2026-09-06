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
