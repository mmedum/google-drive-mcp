package mcpstdio_test

import (
	"testing"
	"time"

	"strings"

	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/render"
	"github.com/mmedum/google-drive-mcp/scripts/internal/mcpstdio"
)

// Both drivers read the id out of a rendered card, because the card is
// what a write returns and its id is what the next call needs. Nothing
// tied that parser to the renderer, and the failure would be silent in
// the worst way: every step that needs an id from an earlier create
// would report "skipped, the file it needs was never created" — the same
// line it prints when the create really failed — and a live run would
// look like a run of a broken server rather than a broken driver.
//
// So this renders a card through the function the server uses rather
// than describing one somebody typed.
func TestTheIDIsReadOutOfACardTheServerActuallyRenders(t *testing.T) {
	now := time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC)
	f := &model.File{
		ID: "1SyntheticFixtureFileIdAAAAAAAAAAAA", Name: "rows with its comments.csv",
		Kind: "file", MimeType: "text/csv", Size: 128, HasSize: true,
		Modified: now.Add(-time.Hour), Created: now.Add(-time.Hour),
	}
	card := render.FileCard(f, render.FileCardOptions{Now: now})
	if got := mcpstdio.IDIn(card); got != f.ID {
		t.Errorf("IDIn(a rendered file card) = %q, want %q\n%s", got, f.ID, card)
	}
}

// A refusal is not a card and carries no id, which is what lets the
// drivers tell "the create failed" from "the create worked".
func TestARefusalCarriesNoID(t *testing.T) {
	if got := mcpstdio.IDIn("[not_found] there is no file with that id"); got != "" {
		t.Errorf("IDIn(a refusal) = %q, want empty", got)
	}
}

// TestTheIDsInsideAListingAreReadOutOfARealOne. A thread's id opens its
// line and a reply's is indented under it — neither is on an `id:` line,
// which is why the driver could not reach them and three of its last
// uncovered options stayed uncovered.
//
// Rendered through the function the server uses, for the reason the card
// test above is: the renderer decides this shape and nothing else here
// would notice it changing.
func TestTheIDsInsideAListingAreReadOutOfARealOne(t *testing.T) {
	now := time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC)
	const author = "Wendell Ashgrove"
	threads := []*model.Comment{{
		ID: "AAACCnLs6BQ", By: author, Created: now.Add(-time.Hour), Text: "is this row right?",
		Replies: []*model.Reply{
			{ID: "AAACCnLs6BU", By: author, Created: now, Text: "checked, it is"},
			{ID: "AAACCnLs6BY", By: author, Created: now, Text: "and again"},
		},
	}}
	listing := render.Comments(threads, render.CommentsOptions{
		Subject: "rows.csv", Now: now,
	})

	if got := mcpstdio.ThreadIDs(listing); len(got) != 1 || got[0] != "AAACCnLs6BQ" {
		t.Errorf("ThreadIDs = %v, want [AAACCnLs6BQ]\n%s", got, listing)
	}
	replies := mcpstdio.ReplyIDs(listing)
	if len(replies) != 2 || replies[0] != "AAACCnLs6BU" || replies[1] != "AAACCnLs6BY" {
		t.Errorf("ReplyIDs = %v, want the two replies\n%s", replies, listing)
	}
	// The content lines are indented too, and must not be read as ids.
	for _, id := range append(replies, mcpstdio.ThreadIDs(listing)...) {
		if strings.ContainsAny(id, "|") {
			t.Errorf("a content line was read as an id: %q", id)
		}
	}
}
