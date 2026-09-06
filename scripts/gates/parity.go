package main

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
)

// parity fails when `make check` and the CI workflow do not run the same
// set of gates.
//
// The Makefile's `check` target calls itself "Everything CI runs", and
// it was wrong in both directions at once: `api-coverage` ran locally and
// never on a pull request, so the gate holding every API method to a
// decision guarded nothing; `schema-diff` ran in CI and not locally, so
// the tool-surface contract could be broken all the way to a push.
//
// Neither is carelessness, and discipline will not fix it. The two lists
// live in different files and nobody edits both: a gate added to the
// Makefile is added while thinking about the Makefile. The only thing
// that holds them together is a third thing that reads both — which is
// the same argument as every other gate here, applied to the gates.
//
// It compares the gates only. CI does plenty the Makefile does not
// (gitleaks over history, uploading schemas, three operating systems),
// and the Makefile has targets CI should not run — `api-diff` refetches
// Google's discovery documents and needs the network, which is why it is
// in neither list and why this reads `check`'s own prerequisites rather
// than every target in the file.
func parity(out io.Writer, _ []string) error {
	return parityAgainst(out, gateNames())
}

// parityAgainst is parity with the list of gates that ought to run given
// rather than read from the registry, so a test can present a small
// repository without restating every gate this program implements.
func parityAgainst(out io.Writer, want []string) error {
	makeGates, err := gatesInCheck("Makefile")
	if err != nil {
		return err
	}
	ciGates, err := gatesInWorkflow(".github/workflows/ci.yml")
	if err != nil {
		return err
	}
	if len(makeGates) == 0 || len(ciGates) == 0 {
		// A parity check that compares two empty lists passes forever.
		return fmt.Errorf("found %d gate(s) in make check and %d in ci.yml; "+
			"one of the two files is not being read as expected", len(makeGates), len(ciGates))
	}

	// Three directions, not two. Two derived lists compared only with
	// each other are both silent about a gate that is in NEITHER, which
	// is the same hole `gates classes` closes by checking against the
	// code: a check invented and then wired up nowhere reads exactly
	// like a check nobody wrote.
	var problems []string
	for _, g := range want {
		inMake, inCI := slices.Contains(makeGates, g), slices.Contains(ciGates, g)
		switch {
		case inMake && inCI:
		case inMake:
			problems = append(problems, g+" runs in `make check` and not in ci.yml, "+
				"so it guards nothing on a pull request")
		case inCI:
			problems = append(problems, g+" runs in ci.yml and not in `make check`, "+
				"so it can only fail after a push")
		default:
			problems = append(problems, g+" is a gate this program implements and nothing runs it; "+
				"add it to both, or drop `gate: true` if it is not one")
		}
	}
	for _, g := range append(slices.Clone(makeGates), ciGates...) {
		if !slices.Contains(want, g) && !slices.Contains(problems, g) {
			problems = append(problems, g+" is run as a gate but is not marked one in commands; "+
				"either it belongs in both lists or the marking is wrong")
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, "  "+p)
		}
		return fmt.Errorf("%d gate(s) are not run in both places", len(problems))
	}
	_, _ = fmt.Fprintf(out, "gate parity ok (%d gates, in both `make check` and ci.yml)\n", len(want))
	return nil
}

// checkTarget matches the `check:` rule and captures its prerequisites.
var checkTarget = regexp.MustCompile(`(?m)^check:[ \t]*([^\n#]*)`)

// gateCall matches an invocation of this program, however it is spelled:
// the Makefile goes through $(GO) and CI writes `go` out.
var gateCall = regexp.MustCompile(`(?:\$\(GO\)|\bgo) run \./scripts/gates ([a-z][a-z-]*)`)

// gatesInCheck reads the Makefile and reports the gates `check` reaches,
// through the targets it depends on.
func gatesInCheck(path string) ([]string, error) {
	source, err := os.ReadFile(path) //nolint:gosec // a path this repository owns
	if err != nil {
		return nil, err
	}
	text := string(source)
	m := checkTarget.FindStringSubmatch(text)
	if m == nil {
		return nil, fmt.Errorf("%s has no `check:` target; has it been renamed?", path)
	}

	// Prerequisites are followed transitively. `cover` depends on `test`,
	// and a gate added to a second-level target would otherwise be
	// invisible on the Make side — reported as running only in CI, which
	// is a false failure on a repository that is actually correct.
	var gates []string
	seen := map[string]bool{}
	var walk func(target string) error
	walk = func(target string) error {
		if seen[target] {
			return nil
		}
		seen[target] = true
		recipe, prereqs, err := makeTarget(text, target)
		if err != nil {
			return err
		}
		for _, call := range gateCall.FindAllStringSubmatch(recipe, -1) {
			if !slices.Contains(gates, call[1]) {
				gates = append(gates, call[1])
			}
		}
		for _, next := range prereqs {
			// A prerequisite may be a file rather than a target; those
			// have no recipe here and are not gates.
			if err := walk(next); err != nil && !strings.Contains(err.Error(), "not a target") {
				return err
			}
		}
		return nil
	}
	for _, target := range strings.Fields(m[1]) {
		if err := walk(target); err != nil {
			return nil, err
		}
	}
	slices.Sort(gates)
	return gates, nil
}

// makeTarget returns one target's recipe lines and its own prerequisites.
func makeTarget(text, target string) (recipe string, prereqs []string, err error) {
	// Prerequisites run to a `##` help comment or to the end of the line.
	rule := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:([^\n#]*)(?:#[^\n]*)?\n`)
	loc := rule.FindStringSubmatchIndex(text)
	if loc == nil {
		return "", nil, fmt.Errorf("%q is not a target in the Makefile", target)
	}
	prereqs = strings.Fields(text[loc[2]:loc[3]])
	rest := text[loc[1]:]
	var b strings.Builder
	for line := range strings.SplitSeq(rest, "\n") {
		// A recipe line is indented; the first line that is not ends it.
		if line != "" && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") {
			break
		}
		// A commented-out recipe line is not a recipe line. Make hands
		// an indented `#` to the shell, which does nothing with it, and
		// counting it would make a disabled gate look like a running one
		// — the same hole the workflow side had.
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String(), prereqs, nil
}

// gatesInWorkflow reports the gates a workflow actually runs.
//
// Reading the whole file for the call is not enough, and the way it
// fails is the way this gate gets defeated in practice: a step commented
// out to unblock a red build still matches, so parity stays green across
// exactly the divergence it exists to catch, reached by typing one `#`.
// A sibling repository found this in its own port of these gates.
//
// So comment lines go first, and a call counts only where a `run:` step
// names it — either on the step's own line or inside a `run: |` block,
// which is found by indentation rather than by parsing YAML. A step
// disabled by an `if:` that is never true would still count, and closing
// that needs a real YAML parse and a dependency; it is a smaller hole
// than a `#`, which needs nothing.
func gatesInWorkflow(path string) ([]string, error) {
	source, err := os.ReadFile(path) //nolint:gosec // a path this repository owns
	if err != nil {
		return nil, err
	}
	var gates []string
	blockIndent := -1
	for _, line := range strings.Split(string(source), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if blockIndent >= 0 && indent <= blockIndent {
			blockIndent = -1
		}
		inRunStep := runStep.MatchString(line)
		if inRunStep && runBlock.MatchString(line) {
			blockIndent = indent
		}
		if !inRunStep && blockIndent < 0 {
			continue
		}
		for _, call := range gateCall.FindAllStringSubmatch(line, -1) {
			if !slices.Contains(gates, call[1]) {
				gates = append(gates, call[1])
			}
		}
	}
	slices.Sort(gates)
	return gates, nil
}

// runStep matches the line of a workflow step that runs a command.
var runStep = regexp.MustCompile(`(?:^|\s|-\s*)run:`)

// runBlock matches a run: that opens a multi-line block.
var runBlock = regexp.MustCompile(`run:\s*[|>]`)
