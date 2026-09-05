package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// precommit runs the checks that are worth paying for before every
// commit: formatting, vet, and a secret scan. The slow gates (tests,
// lint, govulncheck) belong to `make check` and to CI.
//
// gitleaks is used when it is installed and skipped with a warning when
// it is not, because a hook that fails on a missing tool is a hook people
// disable. CI runs it unconditionally, so nothing reaches main unscanned.
func precommit(out io.Writer, _ []string) error {
	var problems []string

	unformatted, err := exec.Command("gofmt", "-l", ".").Output()
	if err != nil {
		return fmt.Errorf("gofmt: %w", err)
	}
	if files := strings.TrimSpace(string(unformatted)); files != "" {
		problems = append(problems, "gofmt would change:\n  "+strings.ReplaceAll(files, "\n", "\n  "))
	}

	if vetted, err := exec.Command("go", "vet", "./...").CombinedOutput(); err != nil {
		problems = append(problems, "go vet:\n"+strings.TrimSpace(string(vetted)))
	}

	// The rule this repository is most likely to break by accident is
	// not a leaked credential but a leaked file name, address or id, and
	// that arrives through a person, not through a build.
	var leakReport strings.Builder
	if err := leaks(&leakReport, nil); err != nil {
		problems = append(problems, strings.TrimSpace(leakReport.String()))
	}

	if _, err := exec.LookPath("gitleaks"); err != nil {
		_, _ = fmt.Fprintln(out, "gitleaks is not installed; skipping the secret scan (CI still runs it)")
	} else {
		scan := exec.Command("gitleaks", "protect", "--staged", "--redact", "--config", ".gitleaks.toml")
		if found, err := scan.CombinedOutput(); err != nil {
			problems = append(problems, "gitleaks found something in the staged changes:\n"+strings.TrimSpace(string(found)))
		}
	}

	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, p)
		}
		return fmt.Errorf("%d pre-commit problem(s)", len(problems))
	}
	_, _ = fmt.Fprintln(out, "pre-commit ok")
	return nil
}

// installHooks writes the git pre-commit hook. It is two lines of shell
// because git requires an executable file; everything it decides is Go.
func installHooks(out io.Writer, _ []string) error {
	dir, err := exec.Command("git", "rev-parse", "--git-path", "hooks").Output()
	if err != nil {
		return fmt.Errorf("locate the hooks directory: %w", err)
	}
	path := strings.TrimSpace(string(dir)) + "/pre-commit"
	hook := "#!/bin/sh\n" +
		"# Installed by `make hooks`. The checks live in scripts/gates.\n" +
		"exec go run ./scripts/gates precommit\n"
	if err := os.WriteFile(path, []byte(hook), 0o755); err != nil { //nolint:gosec // a hook has to be executable
		return fmt.Errorf("write %s: %w", path, err)
	}
	_, _ = fmt.Fprintf(out, "installed %s\n", path)
	return nil
}
