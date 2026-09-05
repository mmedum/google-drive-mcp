package main

import (
	"strings"
	"testing"
)

func TestFirstRevisionReadsTheIDOutOfAListing(t *testing.T) {
	// The parser is only ever run against this renderer's output, so the
	// shape it expects is worth pinning: a subject line, then rows whose
	// second field is a date, then prose.
	listing := "rows.csv — csv: 2 revisions\n" +
		"id-revision-b  2026-03-04 09:05Z (2 days ago) by Test Person (you)  1.2 KiB  current\n" +
		"id-revision-a  2026-03-04 09:00Z (2 days ago) by Test Person (you)  1.0 KiB\n" +
		"Drive discards a revision 30 days after it stops being current unless it is kept forever\n"
	got := ""
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && looksLikeDate(fields[1]) {
			got = fields[0]
			break
		}
	}
	if got != "id-revision-b" {
		t.Errorf("first revision = %q, want the newest row's id", got)
	}
	for _, not := range []string{"rows.csv", "2026-3-4", "09:00Z", ""} {
		if looksLikeDate(not) {
			t.Errorf("looksLikeDate(%q) = true", not)
		}
	}
}

func TestOwnerIsReadFromACardForComparison(t *testing.T) {
	// Spike F compares the owner before and after a transfer, because a
	// call that answers 200 and leaves the owner where it was looks
	// identical to one that worked. The value is only ever compared,
	// never printed, so it is read before redaction.
	card := "created: ownership transfer probe — text file\n" +
		"id: id-transfer-probe-fixture\n" +
		"owner: Someone Else <someone@example.com>\n" +
		"you can: edit, comment\n"
	if got := ownerFromResult(card); got != "Someone Else <someone@example.com>" {
		t.Errorf("owner = %q", got)
	}
	// A card with no owner line means the file could not be read back,
	// which spike F reports as "unknown" rather than as success.
	if got := ownerFromResult("id: id-transfer-probe-fixture\n"); got != "" {
		t.Errorf("owner = %q, want empty when there is no owner line", got)
	}
}
