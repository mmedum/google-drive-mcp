// Package render turns the model into the text a tool returns. Output is
// for a model to read: dense, stable, and never a table of numbers where
// a sentence would do. Every renderer is pure, so the goldens in
// testdata/golden are the specification.
package render

import (
	"fmt"
	"strings"
)

// DefaultMaxChars is the output budget a listing or read uses when the
// caller names none. Claude Code warns above 10 000 tokens and truncates
// above 25 000, so a result that plans its own size keeps the model in
// control of what it asks for next.
const DefaultMaxChars = 20000

// MaxMaxChars is the largest budget a caller may ask for.
const MaxMaxChars = 400000

// Budget clamps a caller's requested output size.
func Budget(maxChars int) int {
	switch {
	case maxChars <= 0:
		return DefaultMaxChars
	case maxChars > MaxMaxChars:
		return MaxMaxChars
	default:
		return maxChars
	}
}

// buf is a small line-oriented builder; every renderer writes through it
// so trailing whitespace and line endings stay uniform.
type buf struct {
	sb strings.Builder
}

func (b *buf) line(s string) {
	b.sb.WriteString(strings.TrimRight(s, " \t"))
	b.sb.WriteByte('\n')
}

func (b *buf) linef(format string, args ...any) { b.line(fmt.Sprintf(format, args...)) }

// field writes "name: value", skipping the line when the value is empty.
func (b *buf) field(name, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	b.linef("%s: %s", name, value)
}

func (b *buf) String() string { return b.sb.String() }

// joinOr renders a comma-separated list, or a fallback when it is empty.
func joinOr(items []string, fallback string) string {
	if len(items) == 0 {
		return fallback
	}
	return strings.Join(items, ", ")
}
