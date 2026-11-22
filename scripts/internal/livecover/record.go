package livecover

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// A Recorder remembers what a run actually sent.
//
// It exists because reading the source is not the same as watching the
// run. A step can sit in a slice nobody passes on, or behind a condition
// that was false all run, and the source cannot tell: it says the option
// is driven because the words are there. Delete one call from a run and
// a gate over the source still reports full coverage.
//
// So the driver records every call it makes, at the one place calls go
// out, and compares the record against what the source believes. The
// comparison is the point. The number on its own is the smaller half.
type Recorder struct {
	sent map[string]map[string]bool
	// called counts invocations per tool, which is not derivable from
	// sent: get_account takes no options at all, so a call of it records
	// an empty set, which alone would read as the tool never called.
	called map[string]int
	// refused is the steps the account refused, by name, for a feature an
	// edition or an admin turns off: the steps after one do not run.
	refused map[string]bool
}

// NewRecorder returns a recorder with nothing in it.
func NewRecorder() *Recorder {
	return &Recorder{sent: map[string]map[string]bool{}, called: map[string]int{}, refused: map[string]bool{}}
}

// calls is how many tool invocations this run made.
func (r *Recorder) calls() int {
	n := 0
	for _, count := range r.called {
		n += count
	}
	return n
}

// Sent records one tool call. It takes the arguments actually handed to
// the server, so an option added by a helper on the way out is recorded
// as sent — which it was.
func (r *Recorder) Sent(tool string, args map[string]any) {
	r.called[tool]++
	if r.sent[tool] == nil {
		r.sent[tool] = map[string]bool{}
	}
	for name := range args {
		r.sent[tool][name] = true
	}
}

// NotOffered records that the account refused the named step for want of
// a feature: the steps after it are then reported as the account's
// limit rather than as steps that never run.
func (r *Recorder) NotOffered(step string) { r.refused[step] = true }

// Report says what this run drove, and what it did not drive that the
// source says it should have.
//
// published is the surface the server registered for THIS run, which is
// the only fair denominator: a run without -destructive registers none
// of the five, and counting them would report a driver as worse than it
// is. steps is what ReadSteps read, and everything interesting is in
// the gap between the two. given is the flags this run was passed, which
// is what tells a step behind a flag the run lacked from a step that
// exists and does not run.
func (r *Recorder) Report(published map[string][]string, steps Steps, given map[string]bool) string {
	var b strings.Builder
	total, driven := 0, 0
	var untouched []string
	for _, tool := range Sorted(published) {
		if r.called[tool] == 0 {
			untouched = append(untouched, tool+r.whyUncalled(steps[tool], given))
		}
		for _, option := range published[tool] {
			total++
			if r.sent[tool][option] {
				driven++
			}
		}
	}
	fmt.Fprintf(&b, "live cover: this run sent %d of %d options across %d registered tools, in %d calls",
		driven, total, len(published), r.calls())

	// The check the source cannot make. A step whose words are in the
	// file and whose call never happened reads as coverage nobody has,
	// and this is the only place the difference is visible.
	var behind, ghosts []string
	for _, tool := range Sorted(steps) {
		if _, registered := published[tool]; !registered {
			continue
		}
		// A tool this run never called at all is reported once, by name,
		// rather than once per option it did not send.
		if r.called[tool] == 0 {
			continue
		}
		for _, option := range Sorted(steps[tool]) {
			if r.sent[tool][option] {
				continue
			}
			if lacked := lackedFlags(steps[tool][option], given, r.refused); lacked != "" {
				behind = append(behind, tool+"."+option+" ("+lacked+")")
			} else {
				ghosts = append(ghosts, tool+"."+option)
			}
		}
	}
	// Already in order: the two Sorted walks above build these as
	// tool.option, and "." sorts below every character a name can carry.
	if len(behind) > 0 {
		fmt.Fprintf(&b, "\n\n%d option(s) the driver sends only behind a flag this run was not given, or past a feature this account refused:\n   %s",
			len(behind), strings.Join(behind, ", "))
	}
	if len(ghosts) > 0 {
		fmt.Fprintf(&b, "\n\n!! %d option(s) the driver's source says it sends were NOT sent by this run,\n"+
			"   and no flag this run lacked explains it. It is a step that exists and does not run,\n"+
			"   which `gates live-cover` reads as coverage because the words are there:\n   %s",
			len(ghosts), strings.Join(ghosts, ", "))
	}
	if len(untouched) > 0 {
		fmt.Fprintf(&b, "\n(%d registered tool(s) were not called at all this run: %s. That is expected "+
			"for a mode this run was not asked to exercise, and a defect otherwise.)",
			len(untouched), strings.Join(untouched, ", "))
	}
	return b.String()
}

// lackedFlags names the flags this run lacked that kept every step
// sending an option from running: "-labels", or "-drive or
// -destructive" when two steps wait for different ones. It is "" when a
// step waits for nothing this run lacked, which means that step should
// have run. A gate a refused step holds is lacked only when the account
// refused that step, and is named by it.
func lackedFlags(gates []Gate, given, refused map[string]bool) string {
	var alternatives []string
	for _, gate := range gates {
		var lacked []string
		for _, flag := range gate {
			if step, ok := strings.CutPrefix(flag, RefusedGate); ok {
				if refused[step] {
					lacked = append(lacked, "the account refused "+strconv.Quote(step))
				}
			} else if !given[flag] {
				lacked = append(lacked, "-"+flag)
			}
		}
		if len(lacked) == 0 {
			return ""
		}
		if alt := strings.Join(lacked, " and "); !slices.Contains(alternatives, alt) {
			alternatives = append(alternatives, alt)
		}
	}
	sort.Strings(alternatives)
	return strings.Join(alternatives, " or ")
}

// whyUncalled is, for a tool this run never called, what every step
// sending one of its options lacked, as " (-labels)"; "" when a step
// lacked nothing, or the source names none.
func (r *Recorder) whyUncalled(options map[string][]Gate, given map[string]bool) string {
	var reasons []string
	for _, option := range Sorted(options) {
		lacked := lackedFlags(options[option], given, r.refused)
		if lacked == "" {
			return ""
		}
		if !slices.Contains(reasons, lacked) {
			reasons = append(reasons, lacked)
		}
	}
	if len(reasons) == 0 {
		return ""
	}
	return " (" + strings.Join(reasons, "; ") + ")"
}
