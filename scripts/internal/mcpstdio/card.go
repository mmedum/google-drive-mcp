package mcpstdio

import (
	"regexp"
	"strings"
)

// The two drivers under scripts/ both read values back out of a rendered
// file card, because the card is what a write returns and its id is what
// the next call needs. One parser, because two had already drifted: one
// required the whole line to be "id: <value>" and the other took any
// line beginning "id: ", so a change to the renderer would have broken
// one of them and not the other.

// idLine matches the "id: <value>" line every card carries.
var idLine = regexp.MustCompile(`(?m)^id:[ \t]*(\S+)[ \t]*$`)

// IDIn reads the id out of a rendered file card, or "" when there is
// none — which is what a refusal looks like.
func IDIn(card string) string {
	m := idLine.FindStringSubmatch(card)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// FirstLine is the head of a result, for a message that names what went
// wrong without repeating the whole card.
func FirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// A comment listing prints ids INSIDE the listing rather than on an
// `id:` line: a thread's id opens its line, and a reply's is indented
// under it. IDIn cannot see either, which is why three of the driver's
// last uncovered options were uncovered — every one of them needs an id
// that only a listing carries.
//
// The patterns are anchored on internal/render's own shape, and a test
// renders a real listing through it rather than describing one. That is
// the same guard the id reader above needed: the renderer decides these,
// and nothing else here would notice it changing them.
var (
	threadIDLine = regexp.MustCompile(`(?m)^([A-Za-z0-9_\-]+)  \S`)
	replyIDLine  = regexp.MustCompile(`(?m)^  ([A-Za-z0-9_\-]+)  \S`)
)

// ThreadIDs are the comment threads a listing shows, in the order it
// shows them.
func ThreadIDs(listing string) []string {
	return firstSubmatches(threadIDLine, listing)
}

// ReplyIDs are the replies a listing shows, across every thread in it.
func ReplyIDs(listing string) []string {
	return firstSubmatches(replyIDLine, listing)
}

func firstSubmatches(re *regexp.Regexp, s string) []string {
	found := re.FindAllStringSubmatch(s, -1)
	out := make([]string, 0, len(found))
	for _, m := range found {
		out = append(out, m[1])
	}
	return out
}
