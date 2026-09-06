package redact

import (
	"regexp"
	"strings"
	"testing"
)

func TestRedactsIdsLinksAndAddresses(t *testing.T) {
	r := NewRedactor(false)
	in := strings.Join([]string{
		"Budget.xlsx — Excel spreadsheet",
		"id: 1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms",
		"link: https://drive.google.com/file/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms/view?usp=sharing",
		"owner: A Person <someone@example.com>",
		"drive: 0AKl3lQ5UUqptUk9PVA",
	}, "\n")
	got := r.Do(in)

	for _, secret := range []string{
		"1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms",
		"someone@example.com",
		"drive.google.com/file",
		"0AKl3lQ5UUqptUk9PVA",
	} {
		if strings.Contains(got, secret) {
			t.Errorf("%q survived redaction:\n%s", secret, got)
		}
	}
	// A display name beside an address is a person, and goes too.
	if strings.Contains(got, "A Person") {
		t.Errorf("a person's name survived beside their address:\n%s", got)
	}
	// File names cannot be told from prose, so they stay — which is why
	// Summary has to say so.
	for _, kept := range []string{"Budget.xlsx", "Excel spreadsheet", "owner:"} {
		if !strings.Contains(got, kept) {
			t.Errorf("%q was redacted but is not account-specific:\n%s", kept, got)
		}
	}
}

func TestPeopleBesideAddressesAreRedacted(t *testing.T) {
	r := NewRedactor(false)
	got := r.Do("modified 2026-09-04 13:17Z by Kim Nørskov <kim@example.com>")
	for _, secret := range []string{"Kim", "Nørskov", "kim@example.com"} {
		if strings.Contains(got, secret) {
			t.Errorf("%q survived:\n%s", secret, got)
		}
	}
	if !strings.Contains(got, "modified 2026-09-04 13:17Z by ") {
		t.Errorf("the surrounding line was mangled:\n%s", got)
	}
	// "(you)" is the signed-in person and names nobody else.
	if got := r.Do("by Someone Else (you)"); !strings.Contains(got, "(you)") {
		t.Errorf("(you) should survive: %s", got)
	}
}

func TestSummaryAlwaysWarnsAboutNames(t *testing.T) {
	// A transcript believed to be clean and is not is worse than one
	// nobody trusts, so every run says what is still in it.
	quiet := NewRedactor(false)
	if !strings.Contains(quiet.Summary(), "names are never redacted") {
		t.Errorf("summary with nothing redacted = %q", quiet.Summary())
	}
	busy := NewRedactor(false)
	busy.Do("a@example.com")
	if !strings.Contains(busy.Summary(), "NOT redacted") {
		t.Errorf("summary after redacting = %q", busy.Summary())
	}
}

func TestPlaceholdersAreStable(t *testing.T) {
	r := NewRedactor(false)
	const id = "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms"
	// The same value must get the same placeholder every time, or the
	// transcript stops telling a story a reader can follow.
	first := r.Do("id: " + id)
	second := r.Do("parent of " + id + " is elsewhere")
	name := strings.TrimPrefix(strings.TrimSpace(first), "id: ")
	if name == "" || !strings.Contains(second, name) {
		t.Errorf("the same id got different placeholders: %q then %q", first, second)
	}
	other := r.Do("1ZzzMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms")
	if strings.Contains(other, name) {
		t.Errorf("two different ids share a placeholder: %q and %q", name, other)
	}
}

func TestOrdinaryWordsSurvive(t *testing.T) {
	r := NewRedactor(false)
	// An unreadable transcript is one nobody checks before pasting it, so
	// long lower-case words have to come through untouched.
	in := "run `google-drive-mcp login`; modified_before and created_after are search fields; " +
		"GDRIVE_ENABLE_DESTRUCTIVE=true registers them"
	if got := r.Do(in); got != in {
		t.Errorf("prose was redacted:\n%s", got)
	}
}

func TestRawLeavesEverythingAlone(t *testing.T) {
	r := NewRedactor(true)
	const in = "id: 1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms someone@example.com"
	if got := r.Do(in); got != in {
		t.Errorf("-raw should not redact: %q", got)
	}
	if !strings.Contains(r.Summary(), "redaction off") {
		t.Errorf("the summary must say redaction was off: %q", r.Summary())
	}
}

func TestSummaryCountsWhatWasHidden(t *testing.T) {
	r := NewRedactor(false)
	if got := r.Summary(); !strings.Contains(got, "nothing needed redacting") {
		t.Errorf("summary = %q", got)
	}
	r.Do("a@example.com and b@example.com and 1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms")
	got := r.Summary()
	if !strings.Contains(got, "2 email") || !strings.Contains(got, "1 id") {
		t.Errorf("summary = %q", got)
	}
}

func TestLinksAreRedactedWholeNotJustTheirIds(t *testing.T) {
	r := NewRedactor(false)
	got := r.Do("https://docs.google.com/spreadsheets/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms/edit#gid=0")
	if strings.Contains(got, "docs.google.com") || strings.Contains(got, "gid=0") {
		t.Errorf("the link leaked: %s", got)
	}
	if !strings.HasPrefix(got, "<LINK_") {
		t.Errorf("a link should become one placeholder, got %q", got)
	}
}

func TestANumericPermissionIDIsRedacted(t *testing.T) {
	// A permission id for a person is twenty digits with no letter, so
	// the "a capital and a digit" rule let one through in a live run. It
	// identifies a Google account, which is an id in every sense that
	// matters here.
	r := NewRedactor(false)
	got := r.Do("owns it  Someone  17839208826236824972  inherited from a folder above it")
	if strings.Contains(got, "17839208826236824972") {
		t.Errorf("a numeric permission id survived redaction:\n%s", got)
	}
	if !strings.Contains(got, "<ID_") {
		t.Errorf("it was removed rather than replaced with a placeholder:\n%s", got)
	}

	// Ordinary long numbers in output must stay readable, or a transcript
	// becomes one nobody checks: byte counts and years are far shorter
	// than an id and must not be touched.
	plain := "bytes: 6291456 (6.0 MiB), modified 2026-09-05 20:04Z"
	if out := NewRedactor(false).Do(plain); out != plain {
		t.Errorf("an ordinary number was redacted:\n%s", out)
	}
}

// TestANameWithNoAddressBesideItIsHidden is the gap phase 3 opened and
// this file did not notice. internal/model prints a person three ways
// and only one of them has an address; the redactor could see that one.
//
// Every line below is the shape a renderer in internal/render actually
// emits, with invented names. A live transcript is never the fixture:
// one copied from a real response is itself the leak.
func TestANameWithNoAddressBesideItIsHidden(t *testing.T) {
	r := NewRedactor(false)
	lines := []struct{ what, in string }{
		{"the signed-in account in a file card", "owner: Wendell Ashgrove (you)"},
		{"the signed-in account in a listing", "folder  Reports  modified 2026-03-04 09:00Z (2 days ago) by Wendell Ashgrove (you)"},
		{"another person, no address", "owner: Perpetua Blackwood"},
		{"a revision's author", "id-revision-b  2026-03-04 09:05Z (2 days ago) by Perpetua Blackwood  1.2 KiB  current"},
		{"a comment author, which Drive gives no address for", "id-comment-1  open  Perpetua Blackwood, 2026-03-04 09:00Z (2 days ago)"},
		{"a reply author", "    id-reply-1  Wendell Ashgrove (you), 2026-03-05 11:00Z (1 day ago)"},
		{"a name beside an address, which already worked", "owner: Perpetua Blackwood <someone@corp.example.net>"},
	}
	for _, line := range lines {
		got := r.Do(line.in)
		for _, name := range []string{"Wendell", "Ashgrove", "Perpetua", "Blackwood"} {
			if strings.Contains(got, name) {
				t.Errorf("%s: %q survived\n  in:  %s\n  out: %s", line.what, name, line.in, got)
			}
		}
		if !strings.Contains(got, "<PERSON_") {
			t.Errorf("%s: nothing was redacted\n  in:  %s\n  out: %s", line.what, line.in, got)
		}
	}
	// One person is one placeholder across DIFFERENT positions, or the
	// transcript stops being readable as a story: the owner of a file and
	// the author of a comment on it have to be visibly the same person.
	token := regexp.MustCompile(`<PERSON_\d+>`)
	fromCard := token.FindString(r.Do("owner: Wendell Ashgrove (you)"))
	fromComment := token.FindString(r.Do("id-comment-1  open  Wendell Ashgrove (you), 2026-03-04 09:00Z"))
	if fromCard == "" || fromCard != fromComment {
		t.Errorf("one person got two placeholders: %q in a card, %q in a comment", fromCard, fromComment)
	}
}

// TestRedactionLeavesTheProseAlone is the other half. An unreadable
// transcript is one nobody checks before pasting it, so a rule that eats
// the words around a name costs more than it saves.
func TestRedactionLeavesTheProseAlone(t *testing.T) {
	r := NewRedactor(false)
	for _, line := range []string{
		"Drive discards a revision 30 days after it stops being current unless it is kept forever",
		"this is a Google Doc. Its text is available through Google's export",
		"folder  Q3 Planning Documents  modified 2026-03-04 09:00Z (2 days ago)",
		"note: made by the live driver",
		"no comments: nobody has commented on this file",
		"you can: edit, comment, share, download, rename, trash",
		"NOTHING WAS CHANGED: this was a dry run. Call it again without dry_run to do it.",
	} {
		if got := r.Do(line); got != line {
			t.Errorf("prose was redacted\n  in:  %s\n  out: %s", line, got)
		}
	}
}
