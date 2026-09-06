package livecover

import (
	"fmt"
	"strings"
)

// A Recorder remembers what a run actually sent.
//
// It exists because reading the source is not the same as watching the
// run. A step can sit in a slice nobody passes on, or behind a condition
// that was false all evening, and the source cannot tell: it says the
// option is driven because the words are there. A sibling repository
// demonstrated this the only way worth doing — by deleting one call and
// watching its static gate still report full coverage.
//
// So the driver records every call it makes, at the one place calls go
// out, and compares the record against what the source believes. The
// comparison is the point. The number on its own is the smaller half.
type Recorder struct {
	sent map[string]map[string]bool
	// called counts invocations per tool, which is not derivable from
	// sent: get_account takes no options at all, so a run that called it
	// recorded an empty set and then reported the tool as never called.
	called map[string]int
}

// NewRecorder returns a recorder with nothing in it.
func NewRecorder() *Recorder {
	return &Recorder{sent: map[string]map[string]bool{}, called: map[string]int{}}
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

// Report says what this run drove, and what it did not drive that the
// source says it should have.
//
// published is the surface the server registered for THIS run, which is
// the only fair denominator: a run without -destructive registers none
// of the five, and counting them would report a driver as worse than it
// is. believed is what FromSource read, and everything interesting is in
// the gap between the two.
func (r *Recorder) Report(published map[string][]string, believed map[string]map[string]bool) string {
	var b strings.Builder
	total, driven := 0, 0
	var untouched []string
	for _, tool := range Sorted(published) {
		if r.called[tool] == 0 {
			untouched = append(untouched, tool)
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
	var ghosts []string
	for _, tool := range Sorted(believed) {
		if _, registered := published[tool]; !registered {
			continue
		}
		// A tool this run never called at all is reported once, by name,
		// rather than once per option it did not send.
		if r.called[tool] == 0 {
			continue
		}
		for _, option := range Sorted(believed[tool]) {
			if !r.sent[tool][option] {
				ghosts = append(ghosts, tool+"."+option)
			}
		}
	}
	// Already in order: the two Sorted walks above build these as
	// tool.option, and "." sorts below every character a name can carry.
	if len(ghosts) > 0 {
		fmt.Fprintf(&b, "\n\n!! %d option(s) the driver's source says it sends were NOT sent by this run.\n"+
			"   Expected for a step behind an option this run was not given — -file, -share, -drive.\n"+
			"   Otherwise it is a step that exists and does not run, which `gates live-cover` reads\n"+
			"   as coverage because the words are there:\n   %s",
			len(ghosts), strings.Join(ghosts, ", "))
	}
	if len(untouched) > 0 {
		fmt.Fprintf(&b, "\n(%d registered tool(s) were not called at all this run: %s. That is expected "+
			"for a mode this run was not asked to exercise, and a defect otherwise.)",
			len(untouched), strings.Join(untouched, ", "))
	}
	return b.String()
}
