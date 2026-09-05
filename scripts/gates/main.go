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
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	args := os.Args[2:]
	var err error
	switch os.Args[1] {
	case "coverage":
		err = coverage(os.Stdout, args)
	case "schema-diff":
		err = schemaDiff(os.Stdout, args)
	case "smoke":
		err = smoke(os.Stdout, args)
	case "staleness":
		err = staleness(os.Stdout, args)
	case "leaks":
		err = leaks(os.Stdout, args)
	case "pins":
		err = pins(os.Stdout, args)
	case "precommit":
		err = precommit(os.Stdout, args)
	case "install-hooks":
		err = installHooks(os.Stdout, args)
	case "help", "-h", "--help":
		usage(os.Stdout)
		return
	default:
		fmt.Fprintf(os.Stderr, "gates: unknown check %q\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gates: %v\n", err)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `gates — the repository's own checks

Usage:
  go run ./scripts/gates coverage [PROFILE] [MIN]   statement-coverage floor per core package
  go run ./scripts/gates schema-diff [BINARY]       tool surface against the last tag
  go run ./scripts/gates smoke [BINARY]             drive the binary over stdio, no credentials
  go run ./scripts/gates staleness [BINARY]         documentation must match the code
  go run ./scripts/gates leaks                     nothing from a real Drive is in the working tree
  go run ./scripts/gates leaks history             ...nor anywhere in the history
  go run ./scripts/gates pins                      every tool a workflow installs is one exact version
  go run ./scripts/gates precommit                 gofmt, vet and a secret scan
  go run ./scripts/gates install-hooks             write the git pre-commit hook
`)
}

// arg returns the i-th argument or a fallback.
func arg(args []string, i int, fallback string) string {
	if i < len(args) && args[i] != "" {
		return args[i]
	}
	return fallback
}
