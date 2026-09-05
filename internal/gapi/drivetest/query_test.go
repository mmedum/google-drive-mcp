package drivetest

import (
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

func match(t *testing.T, q string, f *gdrive.File, s *Server) bool {
	t.Helper()
	pred, err := parseQuery(q)
	if err != nil {
		t.Fatalf("parseQuery(%q): %v", q, err)
	}
	return pred(f, s)
}

func TestNameContainsMatchesWordPrefixesOnly(t *testing.T) {
	s := New()
	defer s.Close()
	f := &gdrive.File{Name: "Budget 2026 final"}
	cases := map[string]bool{
		"name contains 'Bud'":        true,
		"name contains 'budget'":     true,
		"name contains '2026'":       true,
		"name contains 'Budget 20'":  true,
		"name contains 'fin'":        true,
		"name contains 'udget'":      false, // the middle of a word
		"name contains 'inal'":       false,
		"name contains '2026 final'": true,
		"name contains 'final 2026'": false, // words must be consecutive
	}
	for q, want := range cases {
		if got := match(t, q, f, s); got != want {
			t.Errorf("%s = %t, want %t", q, got, want)
		}
	}
}

func TestNameEqualsIsExact(t *testing.T) {
	s := New()
	defer s.Close()
	f := &gdrive.File{Name: "Q3"}
	if !match(t, "name = 'Q3'", f, s) {
		t.Error("exact name should match")
	}
	if match(t, "name = 'q3'", f, s) {
		t.Error("name = is case-sensitive")
	}
	if !match(t, "name != 'Q4'", f, s) {
		t.Error("!= should match a different name")
	}
}

func TestFullTextMatchesWholeTokensAndPhrases(t *testing.T) {
	s := New()
	defer s.Close()
	f := &gdrive.File{ID: "id-x-fixture", Name: "Report", Description: "the quarterly numbers"}
	s.Content["id-x-fixture"] = "revenue grew across every region"
	cases := map[string]bool{
		"fullText contains 'quarterly'":        true,
		"fullText contains 'revenue'":          true,
		"fullText contains 'quarter'":          false, // a prefix is not a token
		"fullText contains '\"grew across\"'":  true,
		"fullText contains '\"across grew\"'":  false,
		"fullText contains '\"every region\"'": true,
		"fullText contains 'nothinglikethis'":  false,
	}
	for q, want := range cases {
		if got := match(t, q, f, s); got != want {
			t.Errorf("%s = %t, want %t", q, got, want)
		}
	}
}

func TestParentsAndOwners(t *testing.T) {
	s := New()
	defer s.Close()
	f := &gdrive.File{Parents: []string{"id-folder"}, Owners: []*gdrive.User{{EmailAddress: "person@example.com", Me: true}}}
	if !match(t, "'id-folder' in parents", f, s) {
		t.Error("parent membership should match")
	}
	if match(t, "'id-other' in parents", f, s) {
		t.Error("a different parent should not match")
	}
	if !match(t, "'me' in owners", f, s) {
		t.Error("'me' in owners should match the signed-in account")
	}
	if !match(t, "'person@example.com' in owners", f, s) {
		t.Error("owner by address should match")
	}
	root := &gdrive.File{Parents: []string{s.RootID}}
	if !match(t, "'root' in parents", root, s) {
		t.Error("'root' should resolve to My Drive's root id")
	}
}

func TestBooleansAndTimes(t *testing.T) {
	s := New()
	defer s.Close()
	f := &gdrive.File{Trashed: true, Starred: false, ModifiedTime: "2026-03-04T09:00:00Z"}
	if !match(t, "trashed = true", f, s) || match(t, "trashed = false", f, s) {
		t.Error("trashed comparison is wrong")
	}
	if !match(t, "starred = false", f, s) {
		t.Error("starred = false should match an unstarred file")
	}
	if !match(t, "modifiedTime > '2026-01-01T00:00:00Z'", f, s) {
		t.Error("modifiedTime > should match a later file")
	}
	if match(t, "modifiedTime < '2026-01-01T00:00:00Z'", f, s) {
		t.Error("modifiedTime < should not match a later file")
	}
}

func TestBooleanOperators(t *testing.T) {
	s := New()
	defer s.Close()
	f := &gdrive.File{Name: "Budget", MimeType: gdrive.MimeFolder, Trashed: false}
	if !match(t, "name = 'Budget' and trashed = false", f, s) {
		t.Error("and should match")
	}
	if match(t, "name = 'Budget' and trashed = true", f, s) {
		t.Error("and should reject when one side fails")
	}
	if !match(t, "name = 'Other' or mimeType = '"+gdrive.MimeFolder+"'", f, s) {
		t.Error("or should match")
	}
	if !match(t, "not trashed = true", f, s) {
		t.Error("not should negate")
	}
	if !match(t, "(name = 'Budget' or name = 'Other') and trashed = false", f, s) {
		t.Error("parentheses should group")
	}
}

func TestEscapesInLiterals(t *testing.T) {
	s := New()
	defer s.Close()
	f := &gdrive.File{Name: `Bob's "notes" \ draft`}
	if !match(t, `name = 'Bob\'s "notes" \\ draft'`, f, s) {
		t.Error("escaped quotes and backslashes should round-trip")
	}
}

func TestUnsupportedSyntaxIsAnError(t *testing.T) {
	for _, q := range []string{"name ~ 'x'", "'x' in nonsense", "name", "(name = 'x'", "unknownField = 'x'", "name = 'x' extra"} {
		if _, err := parseQuery(q); err == nil {
			t.Errorf("parseQuery(%q) should fail rather than match everything", q)
		}
	}
}

func TestEmptyQueryMatchesEverything(t *testing.T) {
	s := New()
	defer s.Close()
	if !match(t, "", &gdrive.File{}, s) {
		t.Error("an empty query should match")
	}
}
