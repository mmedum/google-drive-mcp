package redact

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A Redactor replaces account-specific values with stable placeholders,
// so that a transcript can be shared. The same input always gets the
// same placeholder, so the transcript still reads as a story: ID_1 in
// one result is ID_1 in the next.
//
// What it hides: file and shared-drive ids, Drive and Docs links, email
// addresses, and the display names that sit beside those addresses.
//
// What it CANNOT hide: the names of files and folders. Nothing
// distinguishes "Q3 revenue plan" from prose, and guessing would either
// leave names in or redact the output into uselessness. Summary says so
// every run, because a transcript that is believed to be clean and is
// not is worse than one nobody trusts.
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
	{"LINK", regexp.MustCompile(`https://(?:drive|docs)\.google\.com/[^\s"'<>)\]]+`)},
	{"EMAIL", regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)},
	// A Drive id is base64url, at least 19 characters, and may carry
	// base64 padding. Requiring a capital and a digit keeps ordinary
	// words — google-drive-mcp, modified_before — readable, which
	// matters because an unreadable transcript is one nobody checks
	// before pasting it.
	//
	// A permission id for a person is the exception: it is twenty digits
	// with no letter at all, so that rule let one through in a live run.
	// It identifies a Google account, so it is an id in every sense that
	// matters here.
	{"ID", regexp.MustCompile(`[A-Za-z0-9_\-]{19,}={0,2}`)},
}

// personName is the shape of a display name: capitalised words of two
// letters or more, with the usual lowercase particles allowed between
// them. Requiring the capital keeps the match off the words around it —
// without it, "13:17Z by Kim" was swallowed whole, timestamp and all.
//
// A name has no shape of its own that tells it from a file's, which is
// why it is never matched on its own. What identifies it is the POSITION
// this server printed it in, and those positions are a closed set
// because internal/render wrote every one of them.
const personName = `(?:\p{Lu}[\p{L}'\-.]+)(?: (?:\p{Lu}[\p{L}'\-.]+|van|von|der|den|de|del|di|du|la|le|bin|al)){0,4}`

// personPositions are those positions. internal/model prints a person in
// exactly three forms (userWords): "Name (you)", "Name <address>", and
// the bare "Name" — and only the middle one has an address beside it to
// be found by, which is all the earlier version of this file could see.
//
// The bare form is not an edge case. Drive populates no email on a
// COMMENT author, so a comment listing carries somebody's name and
// nothing else; phase 3 added that surface and the redactor did not
// learn about it. Nor did it ever hide the signed-in account's own name,
// which prints beside "(you)" in almost every result there is.
var personPositions = []*regexp.Regexp{
	// The signed-in account. Nothing else is followed by "(you)".
	regexp.MustCompile(`(` + personName + `) \(you\)`),
	// Beside an address, once the address is a placeholder.
	regexp.MustCompile(`(` + personName + `) <<EMAIL_\d+>>`),
	// "modified … by Name" ending a field, in a file card, a listing or
	// a revision. A column break or the end of the line closes it.
	regexp.MustCompile(`\bby (` + personName + `)(?:  |$)`),
	// "owner: Name" in a file card.
	regexp.MustCompile(`(?m)^owner: (` + personName + `)`),
	// "Name, 2026-03-04 …" — a comment or reply author, which is the
	// form with nothing else beside it at all.
	regexp.MustCompile(`(` + personName + `), \d{4}-\d{2}-\d{2}`),
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
			if p.name == "ID" && !looksLikeID(match) {
				// A long lower-case word is prose, not an id.
				return match
			}
			return r.placeholder(p.name, match)
		})
	}
	// Names last: the address patterns above turn "<a@b>" into a
	// placeholder, and one of the positions below is defined by it.
	for _, re := range personPositions {
		text = re.ReplaceAllStringFunc(text, func(match string) string {
			name := re.FindStringSubmatch(match)[1]
			return strings.Replace(match, name, r.placeholder("PERSON", name), 1)
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
		return "nothing needed redacting. File and folder names are never redacted: read the transcript before sharing it."
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
	return "redacted: " + strings.Join(parts, ", ") +
		".\nFile and folder names are NOT redacted — nothing distinguishes them from prose. " +
		"Read the transcript before sharing it."
}

// looksLikeID separates an id from an ordinary long word. A Drive file
// id mixes case and digits; a permission id for a person is all digits.
// Neither shape occurs in prose at this length, and a word that is
// neither is left readable on purpose.
func looksLikeID(v string) bool {
	if allDigits.MatchString(v) {
		return true
	}
	return hasUpper.MatchString(v) && hasDigit.MatchString(v)
}

// allDigits matches the numeric form, which is what a permission id for
// a person looks like.
var allDigits = regexp.MustCompile(`^[0-9]+$`)
