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

// TestActionPins covers the two shapes the version-input rule cannot
// see: an action on a mutable tag, and an action whose tool nobody
// pinned. The gitleaks case is the one this check found live in this
// repository on its first run — the secret scanner was installing
// whatever shipped that morning.
func TestActionPins(t *testing.T) {
	const sha = "0000000000000000000000000000000000000000"
	cases := []struct {
		name    string
		yaml    string
		wantBad bool
	}{
		{
			"an action on a tag can be moved under you",
			"jobs:\n  a:\n    steps:\n      - uses: actions/checkout@v7\n",
			true,
		},
		{
			"an action on a SHA is pinned",
			"jobs:\n  a:\n    steps:\n      - uses: actions/checkout@" + sha + "\n",
			false,
		},
		{
			"gitleaks with no scanner version — found live here",
			"jobs:\n  a:\n    steps:\n      - uses: gitleaks/gitleaks-action@" + sha + "\n        env:\n          GITHUB_TOKEN: x\n",
			true,
		},
		{
			"gitleaks with the scanner version",
			"jobs:\n  a:\n    steps:\n      - uses: gitleaks/gitleaks-action@" + sha + "\n        env:\n          GITLEAKS_VERSION: \"8.30.1\"\n",
			false,
		},
		{
			"cosign with no release input — this failed a sibling's release",
			"jobs:\n  a:\n    steps:\n      - name: Install cosign\n        uses: sigstore/cosign-installer@" + sha + "\n",
			true,
		},
		{
			"cosign pinned",
			"jobs:\n  a:\n    steps:\n      - uses: sigstore/cosign-installer@" + sha + "\n        with:\n          cosign-release: v3.1.3\n",
			false,
		},
		{
			"syft with no version input — the same hole one step below",
			"jobs:\n  a:\n    steps:\n      - uses: anchore/sbom-action/download-syft@" + sha + "\n",
			true,
		},
		{
			"setup-go pins by reference through the file",
			"jobs:\n  a:\n    steps:\n      - uses: actions/setup-go@" + sha + "\n        with:\n          go-version-file: go.mod\n",
			false,
		},
		{
			"an action that installs nothing needs no version",
			"jobs:\n  a:\n    steps:\n      - uses: actions/upload-artifact@" + sha + "\n",
			false,
		},
		{
			"an unknown action is not quietly trusted",
			"jobs:\n  a:\n    steps:\n      - uses: some-vendor/tool-installer@" + sha + "\n        with:\n          version: v1.2.3\n",
			true,
		},
		{
			"the next step's pin does not cover this one",
			"jobs:\n  a:\n    steps:\n      - uses: sigstore/cosign-installer@" + sha + "\n\n      - uses: anchore/sbom-action/download-syft@" + sha + "\n        with:\n          syft-version: v1.51.1\n",
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, problems := actionPins("test.yml", c.yaml)
			if got := len(problems) > 0; got != c.wantBad {
				t.Errorf("found %d problem(s), want bad=%v: %v", len(problems), c.wantBad, problems)
			}
		})
	}
}
