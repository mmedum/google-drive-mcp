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
