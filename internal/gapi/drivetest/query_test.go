package drivetest

import (
	"testing"

	"github.com/mmedum/google-drive-mcp/v2/internal/gdrive"
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
		// Observed live (spike A): the order of the words does not
		// matter, and they need not be adjacent.
		"name contains 'final 2026'":   true,
		"name contains 'Budget final'": true,
		"name contains 'Bud fin'":      true,
		"name contains 'Bud nope'":     false,
	}
	for q, want := range cases {
		if got := match(t, q, f, s); got != want {
			t.Errorf("%s = %t, want %t", q, got, want)
		}
	}
}

func TestNameEqualsMatchesTheWholeNameIgnoringCase(t *testing.T) {
	s := New()
	defer s.Close()
	f := &gdrive.File{Name: "Q3"}
	// Observed live (spike A): `name =` matches the whole name but is not
	// case-sensitive. Path resolution leans on this — a folder holding
	// "Budget" and "budget" makes one lookup return two, which is how the
	// ambiguity guard gets reached rather than one silently winning.
	for _, q := range []string{"name = 'Q3'", "name = 'q3'", "name = 'Q3'"} {
		if !match(t, q, f, s) {
			t.Errorf("%s should match", q)
		}
	}
	if match(t, "name = 'Q'", f, s) {
		t.Error("name = matches the whole name, not a prefix of it")
	}
	if match(t, "name = 'Q4'", f, s) {
		t.Error("a different name should not match")
	}
	if !match(t, "name != 'Q4'", f, s) {
		t.Error("!= should match a different name")
	}
	if match(t, "name != 'q3'", f, s) {
		t.Error("!= should agree with = about case")
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

// The fake refuses an orderBy key the reference does not call valid,
// rather than sorting as if it were not there.
func TestUnknownOrderKey(t *testing.T) {
	for orderBy, want := range map[string]string{
		"":                                 "",
		"folder,name_natural":              "",
		"sharedWithMeTime desc":            "",
		"modifiedTime desc,relevance":      "relevance",
		"viewedByMeTime desc,sharedWithMe": "sharedWithMe",
	} {
		key, ok := unknownOrderKey(orderBy)
		if key != want || ok != (want == "") {
			t.Errorf("unknownOrderKey(%q) = %q, %t; want %q", orderBy, key, ok, want)
		}
	}
}

// A file's visibility is the widest grant it carries to people it does
// not name, and the fake refuses what the reference does not document.
func TestVisibility(t *testing.T) {
	s := New()
	defer s.Close()
	f := s.AddFile("id-vis-fixture", "Vis", gdrive.MimeDocument, s.RootID)
	if !match(t, "visibility = 'limited'", f, s) {
		t.Error("a file with no anyone or domain grant is not limited")
	}
	s.Grant(f.ID, &gdrive.Permission{Type: "anyone", Role: "reader"})
	if !match(t, "visibility = 'anyoneWithLink'", f, s) || match(t, "visibility != 'anyoneWithLink'", f, s) {
		t.Error("an anyone grant is not anyoneWithLink")
	}
	s.Grant(f.ID, &gdrive.Permission{Type: "domain", Role: "reader", Domain: "example.com", AllowFileDiscovery: true})
	if !match(t, "visibility = 'anyoneWithLink'", f, s) {
		t.Error("a domain grant added beside an anyone grant narrowed the visibility")
	}
	g := s.AddFile("id-vis-domain-fixture", "Vis domain", gdrive.MimeDocument, s.RootID)
	s.Grant(g.ID, &gdrive.Permission{Type: "domain", Role: "reader", Domain: "example.com", AllowFileDiscovery: true})
	if !match(t, "visibility = 'domainCanFind'", g, s) {
		t.Error("a discoverable domain grant is not domainCanFind")
	}
	for _, q := range []string{"visibility = 'public'", "visibility contains 'anyone'", "visibility > 'limited'"} {
		if _, err := parseQuery(q); err == nil {
			t.Errorf("%s is accepted", q)
		}
	}
}

// readers is read narrowly: an editor is a writer and not a reader.
func TestReadersAndWritersByRole(t *testing.T) {
	s := New()
	defer s.Close()
	f := s.AddFile("id-roles-fixture", "Roles", gdrive.MimeDocument, s.RootID)
	s.Grant(f.ID, &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: "jane@example.com"})
	s.Grant(f.ID, &gdrive.Permission{Type: "user", Role: "commenter", EmailAddress: "john@example.com"})
	for q, want := range map[string]bool{
		"'jane@example.com' in writers": true,
		"'jane@example.com' in readers": false,
		"'john@example.com' in readers": true,
		"'john@example.com' in writers": false,
	} {
		if got := match(t, q, f, s); got != want {
			t.Errorf("%s = %t, want %t", q, got, want)
		}
	}
}
