// Command gates runs the repository's own checks: the coverage floor,
// the tool-schema diff, the stdio smoke test and the staleness check.
//
// It is a Go program rather than a shell script so that the code holding
// the gates shut is itself held to them — it compiles, it is vetted, it
// is linted, and it has tests. GoReleaser builds only ./cmd/..., so this
// never ships.
//
//	go run ./scripts/gates coverage cov.out 80
//	go run ./scripts/gates schema-diff ./google-drive-mcp
//	go run ./scripts/gates smoke ./google-drive-mcp
//	go run ./scripts/gates staleness ./google-drive-mcp
//	go run ./scripts/gates pins
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
)

// command is one thing this program can do.
type command struct {
	run func(io.Writer, []string) error
	// args is how the arguments are spelled in the usage text.
	args string
	doc  string
	// runs says where the command has to run, and is what parity reads.
	runs where
}

// where is the pipeline a command has to run in. Three states, because a
// gate that only a pull request can run is neither a gate `make check`
// runs nor a convenience nothing does.
type where int

const (
	// manual is a developer convenience or a release step. Nothing
	// holds it to a pipeline: api-diff refetches Google's discovery
	// documents over the network, which no pipeline may depend on.
	manual where = iota
	// inCheck has to run in BOTH `make check` and the CI workflow.
	inCheck
	// inPR has to run in CI on a pull request and NOT in `make check`:
	// it measures from the merge-base, which only a pull request has.
	inPR
)

// commands is the one list of what this program does.
//
// It is one list because three had already drifted apart in a single
// commit: the parity gate below was added to the dispatch switch, to the
// Makefile and to the workflow, and not to the usage text — by the same
// change whose entire purpose was to stop a list of gates drifting. The
// dispatch, the usage and the parity check all read this now, so the
// only way to add a command is to add it here.
//
// Filled in init rather than as a literal because parity reads the list
// and the list names parity, which Go sees as an initialization cycle.
var commands map[string]command

func init() {
	commands = map[string]command{
		"coverage":    {run: coverage, args: "[PROFILE] [MIN]", doc: "statement-coverage floor per core package", runs: inCheck},
		"schema-diff": {run: schemaDiff, args: "[BINARY]", doc: "tool surface against the newest release's baseline", runs: inCheck},
		"schema-ack": {run: schemaAck, args: "BASE HEAD",
			doc: "a pull request's commits name each item of the tool surface it changes", runs: inPR},
		"smoke":     {run: smoke, args: "[BINARY]", doc: "drive the binary over stdio, no credentials", runs: inCheck},
		"staleness": {run: staleness, args: "[BINARY]", doc: "documentation must match the code", runs: inCheck},
		"leaks":     {run: leaks, args: "[history]", doc: "nothing from a real Drive is in the tree, or the history", runs: inCheck},
		"pins":      {run: pins, doc: "every tool a workflow installs is one exact version", runs: inCheck},
		"classes":   {run: classes, doc: "the error classes the code emits are the ones it declares", runs: inCheck},
		"api-coverage": {run: apiCoverage,
			doc: "every API method is used on purpose or left out on purpose", runs: inCheck},
		"api-fields": {run: apiFields,
			doc: "every published field is modeled on purpose or left out on purpose", runs: inCheck},
		"parity": {run: parity, doc: "`make check` and CI run the same gates", runs: inCheck},
		"release-notes": {run: releaseNotes, args: "VERSION [CHANGELOG]",
			doc: "one version's CHANGELOG section, which is the release note"},
		"schema-baseline": {run: schemaBaseline, args: "[BINARY]",
			doc: "record the release being cut as the tool-surface baseline, in its release commit"},
		"transcript": {run: transcript,
			doc: "a program that drives a real account cannot reach a terminal except through the redactor", runs: inCheck},
		"live-cover": {run: liveCover, args: "[BINARY]",
			doc: "every tool option is driven live or recorded as not, with the reason", runs: inCheck},
		"outcomes": {run: outcomes,
			doc: "no result sentence is written from the request rather than from the response", runs: inCheck},
		"registry": {run: registry,
			doc: "the committed MCP registry entry obeys the rules that registry enforces in code", runs: inCheck},
		"registry-publish": {run: registryPublish, args: "VERSION [DIST]",
			doc: "print the registry entry to publish, with the bundle's hash from the signed checksums"},
		"mcpb": {run: mcpbCheck,
			doc: "the committed bundle manifest names files the packer will stage", runs: inCheck},
		"api-diff": {run: apiDiff,
			doc: "refetch the discovery documents and report what changed (needs the network)"},
		"schema-refetch": {run: schemaRefetchCmd,
			doc: "check the vendored schemas against what their sources serve (needs the network)"},
		"precommit":     {run: precommit, doc: "gofmt, vet and a secret scan"},
		"install-hooks": {run: installHooks, doc: "write the git pre-commit hook"},
		"mcpb-manifest": {run: mcpbManifest, args: "VERSION [ROOT]",
			doc: "print the packaging manifest with a real version in it"},
		"mcpb-pack": {run: mcpbPack, args: "VERSION [DIST]",
			doc: "pack the Claude Desktop bundle from goreleaser's output"},
	}
}

// commandsIn are the commands that have to run in w, sorted.
func commandsIn(w where) []string {
	var out []string
	for name, c := range commands {
		if c.runs == w {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	name := os.Args[1]
	if name == "help" || name == "-h" || name == "--help" {
		usage(os.Stdout)
		return
	}
	c, ok := commands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "gates: unknown check %q\n", name)
		usage(os.Stderr)
		os.Exit(2)
	}
	if err := c.run(os.Stdout, os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "gates: %v\n", err)
		os.Exit(exitCode(err))
	}
}

// couldNotRun is an error that stopped a gate before it could judge,
// such as a build or a git call that failed.
type couldNotRun struct{ error }

func (e couldNotRun) Unwrap() error { return e.error }

// exitCode is 2 for a gate that could not run, as for a usage error, and
// 1 for a gate that ran and refused.
func exitCode(err error) int {
	if errors.As(err, new(couldNotRun)) {
		return 2
	}
	return 1
}

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, "gates — the repository's own checks\n\nUsage:\n")
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	slices.Sort(names)
	width := 0
	for _, name := range names {
		if n := len(name) + len(commands[name].args) + 1; n > width {
			width = n
		}
	}
	for _, name := range names {
		spelled := name
		if a := commands[name].args; a != "" {
			spelled += " " + a
		}
		_, _ = fmt.Fprintf(w, "  go run ./scripts/gates %-*s  %s\n", width, spelled, commands[name].doc)
	}
}

// arg returns the i-th argument or a fallback.
func arg(args []string, i int, fallback string) string {
	if i < len(args) && args[i] != "" {
		return args[i]
	}
	return fallback
}
