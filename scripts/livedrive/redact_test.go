package main

import (
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
	for _, kept := range []string{"Budget.xlsx", "Excel spreadsheet", "owner:", "A Person"} {
		if !strings.Contains(got, kept) {
			t.Errorf("%q was redacted but is not account-specific:\n%s", kept, got)
		}
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
