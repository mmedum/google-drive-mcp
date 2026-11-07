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
// gitleaks runs through `go run`, so the hook needs nothing installed but
// Go. Its first run fetches the module.
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

	mk, err := readMakefile()
	if err != nil {
		return err
	}
	gitleaks, err := precommitGitleaks(mk)
	if err != nil {
		return err
	}
	scan := exec.Command(gitleaks[0], gitleaks[1:]...)
	if found, err := scan.CombinedOutput(); err != nil {
		problems = append(problems, "gitleaks on the staged changes:\n"+strings.TrimSpace(string(found)))
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

// precommitGitleaks is the hook's secret scan: the gitleaks module@version
// the Makefile's GITLEAKS names, the one `make gitleaks` runs, on the
// staged changes. gitleaks v8.19.0 deprecated `protect` for `git --staged`
// (https://github.com/gitleaks/gitleaks/blob/v8.30.1/README.md#commands).
func precommitGitleaks(mk *makefile) ([]string, error) {
	module := mk.expand("$(GITLEAKS)")
	if !strings.Contains(module, "@") {
		return nil, fmt.Errorf("%s: GITLEAKS names no module@version, so the hook has no gitleaks to scan with", makefilePath)
	}
	return []string{"go", "run", module, "git", "--staged", "--redact", "--no-banner", "--config", ".gitleaks.toml", "."}, nil
}

// precommitHook is the hook installHooks writes, which parity reads for
// the gates it runs.
const precommitHook = "#!/bin/sh\n" +
	"# Installed by `make hooks`. The checks live in scripts/gates.\n" +
	"exec go run ./scripts/gates precommit\n"

// installHooks writes the git pre-commit hook. It is two lines of shell
// because git requires an executable file; everything it decides is Go.
func installHooks(out io.Writer, _ []string) error {
	dir, err := exec.Command("git", "rev-parse", "--git-path", "hooks").Output()
	if err != nil {
		return fmt.Errorf("locate the hooks directory: %w", err)
	}
	path := strings.TrimSpace(string(dir)) + "/pre-commit"
	if err := os.WriteFile(path, []byte(precommitHook), 0o755); err != nil { //nolint:gosec // a hook has to be executable
		return fmt.Errorf("write %s: %w", path, err)
	}
	_, _ = fmt.Fprintf(out, "installed %s\n", path)
	return nil
}
