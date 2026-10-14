package mdcomment

import (
	"errors"
	"strings"
	"testing"
)

// file is synthetic, in the shape the Docs editor writes a Markdown file
// holding two comments: markers around each passage, one passage across
// two paragraphs, and the block ending the file after a blank line.
const file = "# Notes\n\n" +
	"We <!-- gdoc_comment_begin: c1 -->ship in May<!-- gdoc_comment_end: c1 -->.\n\n" +
	"<!-- gdoc_comment_begin: c2 -->First line.\n\nSecond line.<!-- gdoc_comment_end: c2 -->\n\n" + block

// block is the gdoc_comments block that ends file.
const block = "---\ngdoc_comments:\n" +
	"  c1:\n    status: ACTIVE\n    token: opaque-one\n    head:\n      author: Ada Example\n" +
	"      body: is May real?\n      quote: |-\n        ship in May\n" +
	"  c2:\n    status: ACTIVE\n    head:\n      author: Ada Example\n      body: two paragraphs\n" +
	"    replies:\n      - author: Grace Example\n        body: agreed\n" +
	"---\n"

func scan(t *testing.T, text string) *Markup {
	t.Helper()
	m, err := Scan(strings.NewReader(text))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return m
}

func TestScanFindsTheEditorsComments(t *testing.T) {
	m := scan(t, file)
	if got := m.Comments(); got != 2 {
		t.Errorf("Comments() = %d, want 2", got)
	}
	if m.displaced {
		t.Error("the block ending the file was read as displaced")
	}
	if lost := m.Lost(scan(t, file)); lost != nil {
		t.Errorf("the same text loses %q", lost)
	}
}

func TestLostNamesWhatAReplacementLoses(t *testing.T) {
	cases := []struct {
		name, next, want string
	}{
		{"an entry dropped", strings.Replace(file, "  c2:\n    status: ACTIVE\n", "", 1),
			"comment c2 is no longer in the gdoc_comments block"},
		{"a begin marker dropped", strings.Replace(file, "<!-- gdoc_comment_begin: c1 -->", "", 1),
			"comment c1 lost a marker"},
		{"an end marker dropped", strings.Replace(file, "<!-- gdoc_comment_end: c2 -->", "", 1),
			"comment c2 lost a marker"},
		{"text after the block", file + "\nA new last paragraph.\n",
			"goes on after the gdoc_comments block"},
		{"a resolve written into the text", strings.Replace(file, "status: ACTIVE\n    token", "status: RESOLVED\n    token", 1),
			"comment c1 changes status from ACTIVE to RESOLVED"},
		{"the whole block dropped", strings.TrimSuffix(file, block),
			"comment c1 is no longer in the gdoc_comments block"},
	}
	before := scan(t, file)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lost := before.Lost(scan(t, c.next))
			if len(lost) == 0 || !strings.Contains(strings.Join(lost, "; "), c.want) {
				t.Errorf("Lost = %q, want a phrase containing %q", lost, c.want)
			}
		})
	}
}

func TestLostIsEmptyWhenEveryCommentSurvives(t *testing.T) {
	cases := map[string]string{
		"the text edited around the passages": strings.Replace(file, "# Notes", "# Release notes", 1),
		"the commented passage edited":        strings.Replace(file, "ship in May", "ship in June", 1),
		"a comment added":                     strings.Replace(file, "# Notes", "<!-- gdoc_comment_begin: c3 -->#<!-- gdoc_comment_end: c3 --> Notes", 1),
		"blank lines after the block":         file + "\n\n",
		"a block-shaped passage before it":    strings.Replace(file, "# Notes", "# Notes\n\n---\ngdoc_comments:\n  x1:\n---\nText.", 1),
		"a reply added":                       strings.Replace(file, "        body: agreed\n", "        body: agreed\n      - author: Ada Example\n        body: done\n", 1),
	}
	before := scan(t, file)
	for name, next := range cases {
		if lost := before.Lost(scan(t, next)); lost != nil {
			t.Errorf("%s: Lost = %q, want nothing", name, lost)
		}
	}
}

func TestABlockWithTextAfterItIsBodyText(t *testing.T) {
	// Docs reads a block that does not end the file as text, so a marker
	// written on its lines is the body's, and only the last block, the
	// one ending the file, names comments.
	text := "---\ngdoc_comments:\n  old:\n    status: ACTIVE\n    <!-- gdoc_comment_begin: k -->x<!-- gdoc_comment_end: k -->\n---\n" +
		"Text after it.\n\n---\ngdoc_comments:\n  c1:\n    status: ACTIVE\n---\n"
	m := scan(t, text)
	if m.displaced {
		t.Error("a block ends the file, but the earlier one was reported as displacing it")
	}
	if _, ok := m.entries["old"]; ok {
		t.Error("the displaced block's entry was read as a comment")
	}
	if _, ok := m.entries["c1"]; !ok {
		t.Error("the block ending the file was not read")
	}
	if !m.begins["k"] || !m.ends["k"] {
		t.Error("markers on the displaced block's lines were not counted as body text")
	}
}

func TestRulesAndFrontMatterAreNotABlock(t *testing.T) {
	cases := map[string]string{
		"front matter":         "---\ntitle: Notes\n---\n\n# Notes\n",
		"a thematic break":     "One.\n\n---\n\nTwo.\n",
		"a block never closed": "Text.\n\n---\ngdoc_comments:\n  c1:\n    status: ACTIVE\n",
		"an unindented line":   "---\ngdoc_comments:\nc1:\n---\n",
		"the key with a value": "---\ngdoc_comments: none\n  c1:\n---\n",
	}
	for name, text := range cases {
		if got := scan(t, text).Comments(); got != 0 {
			t.Errorf("%s: Comments() = %d, want 0", name, got)
		}
	}
}

func TestScanRefusesWhatItCannotCheck(t *testing.T) {
	long := strings.Repeat("a", maxLine+1)
	many := strings.Repeat("<!-- gdoc_comment_begin: k -->", maxMarkup+1)
	for name, text := range map[string]string{"a line too long": long, "too many markers": many} {
		if _, err := Scan(strings.NewReader(text)); !errors.Is(err, ErrUncheckable) {
			t.Errorf("%s: err = %v, want ErrUncheckable", name, err)
		}
	}
	if _, err := Scan(strings.NewReader(strings.Repeat("<!-- gdoc_comment_begin: k -->", maxMarkup))); err != nil {
		t.Errorf("exactly maxMarkup markers: %v", err)
	}
}

func FuzzScan(f *testing.F) {
	f.Add(file)
	f.Add("---\ngdoc_comments:\n---\n")
	f.Add("<!-- gdoc_comment_end: a -->\n---\n")
	f.Fuzz(func(t *testing.T, text string) {
		m, err := Scan(strings.NewReader(text))
		if err != nil {
			return
		}
		if lost := m.Lost(m); lost != nil {
			t.Fatalf("a file loses %q against itself", lost)
		}
	})
}
