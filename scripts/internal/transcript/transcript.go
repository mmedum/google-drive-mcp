// Package transcript is how a program that drives a real Google account
// prints: through one function, which redacts.
//
// Redaction used to be a habit rather than a construction. Every line
// HAPPENED to go through the redactor, which is not the same as every
// line HAVING to: phase 4 found the live driver echoing each call's
// arguments unredacted, which was arguably nobody's problem while the
// only address there was one the operator had typed — and became one the
// moment starting an approval put the SIGNED-IN account's own address
// into the arguments of every write run. It was fixed line by line, and
// the next print somebody added while debugging would have looked
// exactly like the two beside it that are safe.
//
// So the rule is structural now. `gates transcript` forbids fmt.Print,
// fmt.Fprint, os.Stdout and os.Stderr in every program that talks to a
// real account, which leaves write below as the single place a line
// reaches a terminal — and it redacts what passes through it. A print
// that bypasses the redactor is no longer something to remember; it is
// something the gate refuses.
//
// Two programs share this rather than one having it, because they are
// the same program in the way that matters: the live driver and the eval
// harness both run against somebody's real Drive and both print what
// comes back. A rule that covered one of them would be a rule waiting to
// be drifted around.
//
// What this still cannot do is hide the names of files, folders and
// shared drives: nothing distinguishes one from prose. Summary says so
// at the end of every run.
package transcript

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mmedum/google-drive-mcp/scripts/internal/redact"
)

// A Transcript is the only way its program writes anything.
type Transcript struct {
	red *redact.Redactor
	out io.Writer
	err io.Writer
}

// New writes the run to the terminal.
func New(red *redact.Redactor) *Transcript {
	return &Transcript{red: red, out: os.Stdout, err: os.Stderr}
}

// NewTo writes somewhere else, which is how a test reads what a run
// would have printed.
func NewTo(red *redact.Redactor, out, err io.Writer) *Transcript {
	return &Transcript{red: red, out: out, err: err}
}

// Say prints one line of the transcript.
func (t *Transcript) Say(text string) { t.write(t.out, text) }

// Sayf prints one formatted line of the transcript.
//
// The whole formatted line is redacted, not the arguments the caller
// remembered to wrap. That is the difference this type exists to make:
// an address reaching the terminal through a format string nobody
// thought about is redacted by the same code that redacts one somebody
// did think about.
func (t *Transcript) Sayf(format string, args ...any) {
	t.write(t.out, fmt.Sprintf(format, args...))
}

// Fail prints the run's own failure, which goes to stderr because it is
// not part of the transcript.
func (t *Transcript) Fail(format string, args ...any) {
	t.write(t.err, fmt.Sprintf(format, args...))
}

// write is the one place these programs write to a terminal.
//
// The trailing newlines go first because a tool result carries its own
// and a line is a line: everything printed here ends in exactly one.
func (t *Transcript) write(w io.Writer, text string) {
	_, _ = fmt.Fprintln(w, t.red.Do(strings.TrimRight(text, "\n")))
}

// Summary says what was hidden, and what could not be.
func (t *Transcript) Summary() string { return t.red.Summary() }
