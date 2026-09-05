package main

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A Redactor replaces anything account-specific with a stable
// placeholder, so a transcript can go into an issue or a commit message
// without leaking a file id, a link or an address. The same input always
// gets the same placeholder, so the transcript still reads as a story:
// FILE_1 in one result is FILE_1 in the next.
type Redactor struct {
	// Off returns text unchanged. Only for a terminal nobody else sees.
	Off bool

	seen   map[string]string
	counts map[string]int
}

// pattern is one class of thing to hide, with the name its placeholders
// carry. Order matters: a link is replaced before the id inside it.
type pattern struct {
	name string
	re   *regexp.Regexp
}

var patterns = []pattern{
	{"EMAIL", regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)},
	{"LINK", regexp.MustCompile(`https://(?:drive|docs)\.google\.com/[^\s"'<>)\]]+`)},
	// A Drive id is base64url and at least 19 characters. Requiring a
	// capital and a digit keeps ordinary words — google-drive-mcp,
	// modified_before — readable, which matters because an unreadable
	// transcript is one nobody checks before pasting it.
	{"ID", regexp.MustCompile(`\b[A-Za-z0-9_\-]{19,}\b`)},
}

var (
	hasUpper = regexp.MustCompile(`[A-Z]`)
	hasDigit = regexp.MustCompile(`[0-9]`)
)

// NewRedactor returns a redactor with an empty memory.
func NewRedactor(off bool) *Redactor {
	return &Redactor{Off: off, seen: map[string]string{}, counts: map[string]int{}}
}

// Do replaces every account-specific value in text.
func (r *Redactor) Do(text string) string {
	if r.Off {
		return text
	}
	for _, p := range patterns {
		text = p.re.ReplaceAllStringFunc(text, func(match string) string {
			if p.name == "ID" && (!hasUpper.MatchString(match) || !hasDigit.MatchString(match)) {
				// A long lower-case word is prose, not an id.
				return match
			}
			return r.placeholder(p.name, match)
		})
	}
	return text
}

func (r *Redactor) placeholder(kind, value string) string {
	if got, ok := r.seen[value]; ok {
		return got
	}
	r.counts[kind]++
	out := "<" + kind + "_" + strconv.Itoa(r.counts[kind]) + ">"
	r.seen[value] = out
	return out
}

// Summary lists what was hidden, by kind, so the reader knows the
// transcript was redacted at all.
func (r *Redactor) Summary() string {
	if r.Off {
		return "redaction off: this transcript carries real ids, links and addresses"
	}
	if len(r.seen) == 0 {
		return "nothing needed redacting"
	}
	kinds := make([]string, 0, len(r.counts))
	for k := range r.counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, strconv.Itoa(r.counts[k])+" "+strings.ToLower(k))
	}
	return "redacted: " + strings.Join(parts, ", ")
}
