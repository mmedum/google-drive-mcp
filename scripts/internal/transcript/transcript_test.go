package transcript

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/scripts/internal/redact"
)

// TestEveryLinePrintedIsRedacted is the behaviour behind the structural
// rule. `gates transcript` says the drivers cannot reach a terminal
// except through this package; this says that going through it is worth
// something.
//
// The address arrives three ways on purpose, because the three are how
// it used to get out: in a line somebody wrote as a literal, in a format
// argument nobody thought about, and in the run's own failure, which was
// the last line of a session and the one line that never went through
// the redactor at all.
func TestEveryLinePrintedIsRedacted(t *testing.T) {
	var out, errOut bytes.Buffer
	tr := NewTo(redact.NewRedactor(false), &out, &errOut)

	tr.Say("owner: someone@example.com")
	tr.Sayf("=== share_file %s ===", `{"principal":"someone@example.com"}`)
	tr.Fail("livedrive: sharing with someone@example.com failed")

	both := out.String() + errOut.String()
	if strings.Contains(both, "someone@example.com") {
		t.Errorf("an address reached the terminal:\n%s", both)
	}
	if strings.Count(both, "<EMAIL_1>") != 3 {
		t.Errorf("the same address should be the same placeholder every time:\n%s", both)
	}
	if got := strings.Count(errOut.String(), "\n"); got != 1 {
		t.Errorf("the failure line went to stderr as %d lines, want 1", got)
	}
}

// TestALineIsALine. A tool result carries its own trailing newline and a
// format string may carry another; everything printed here ends in
// exactly one, or a transcript grows blank lines wherever a result was
// long enough for somebody to have trimmed it by hand.
func TestALineIsALine(t *testing.T) {
	var out bytes.Buffer
	tr := NewTo(redact.NewRedactor(false), &out, &out)
	tr.Say("a result that ends in a newline\n")
	tr.Sayf("and one with %s\n\n", "two")
	if got := out.String(); got != "a result that ends in a newline\nand one with two\n" {
		t.Errorf("got %q", got)
	}
}

// TestRawLeavesEverythingAlone. -raw exists for a terminal nobody else
// sees, and the summary says so; it must not be quietly half-applied.
func TestRawLeavesEverythingAlone(t *testing.T) {
	var out bytes.Buffer
	tr := NewTo(redact.NewRedactor(true), &out, &out)
	tr.Say("owner: someone@example.com")
	if !strings.Contains(out.String(), "someone@example.com") {
		t.Errorf("-raw redacted anyway: %q", out.String())
	}
	if !strings.Contains(tr.Summary(), "redaction off") {
		t.Errorf("the summary does not say redaction was off: %q", tr.Summary())
	}
}
