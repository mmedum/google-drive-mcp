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
	// gate marks a command that has to run in BOTH `make check` and the
	// CI workflow. The two that are not gates are developer
	// conveniences, and api-diff refetches Google's discovery documents
	// over the network, which neither of those places may depend on.
	gate bool
}

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
// and the list names parity, which Go sees as an initialisation cycle.
var commands map[string]command

func init() {
	commands = map[string]command{
		"coverage":    {run: coverage, args: "[PROFILE] [MIN]", doc: "statement-coverage floor per core package", gate: true},
		"schema-diff": {run: schemaDiff, args: "[BINARY]", doc: "tool surface against the last tag", gate: true},
		"smoke":       {run: smoke, args: "[BINARY]", doc: "drive the binary over stdio, no credentials", gate: true},
		"staleness":   {run: staleness, args: "[BINARY]", doc: "documentation must match the code", gate: true},
		"leaks":       {run: leaks, args: "[history]", doc: "nothing from a real Drive is in the tree, or the history", gate: true},
		"pins":        {run: pins, doc: "every tool a workflow installs is one exact version", gate: true},
		"classes":     {run: classes, doc: "the error classes the code emits are the ones it declares", gate: true},
		"api-coverage": {run: apiCoverage,
			doc: "every API method is used on purpose or left out on purpose", gate: true},
		"parity": {run: parity, doc: "`make check` and CI run the same gates", gate: true},
		"api-diff": {run: apiDiff,
			doc: "refetch the discovery documents and report what changed (needs the network)"},
		"precommit":     {run: precommit, doc: "gofmt, vet and a secret scan"},
		"install-hooks": {run: installHooks, doc: "write the git pre-commit hook"},
	}
}

// gateNames are the commands that must run in both places, sorted.
func gateNames() []string {
	var out []string
	for name, c := range commands {
		if c.gate {
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
		os.Exit(1)
	}
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
