// Package mdcomment finds the comments Google Docs keeps inside a
// Markdown file. Docs opens a .md stored in Drive and edits it in place,
// and a comment made in its editor lives in the file's own text rather
// than among Drive's comments: a marker on each side of the passage,
//
//	<!-- gdoc_comment_begin: c1 -->a passage<!-- gdoc_comment_end: c1 -->
//
// and an entry under the same key in a gdoc_comments block, which must
// end the file. Replacing the whole text with one that drops a marker
// unpins the comment, one that drops the entry deletes it, and text
// after the block turns the block into body text (architecture §18).
//
// It reads a stream a line at a time and keeps only the keys it finds,
// so a file of any size is checked without being held in memory, and
// both a line and the number of markers are bounded. It imports nothing
// from this repository.
package mdcomment

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// maxLine is the longest line Scan reads. A paragraph is one line in
// Markdown, so this is far past any a person writes, and it bounds what
// one line can make Scan hold.
const maxLine = 4 << 20

// maxMarkup is how many markers and entries Scan counts before it gives
// up: three for each of ten thousand comments.
const maxMarkup = 30_000

// ErrUncheckable is a file Scan cannot check: a line longer than 4 MiB,
// or more than 30 000 markers and entries.
var ErrUncheckable = errors.New("too large to check for comments")

var (
	markerRE = regexp.MustCompile(`<!-- gdoc_comment_(begin|end): ([A-Za-z0-9_.:-]+) -->`)
	entryRE  = regexp.MustCompile(`^  ([A-Za-z0-9_.:-]+):$`)
	statusRE = regexp.MustCompile(`^    status: (\S+)$`)
)

// The two lines that open the block. Docs writes each on its own line,
// and a rule closes the block.
const (
	rule     = "---"
	blockKey = "gdoc_comments:"
)

// Markup is the comment markup one file holds.
type Markup struct {
	begins, ends map[string]bool
	// entries maps each key the block names to its status.
	entries map[string]string
	// displaced reports a gdoc_comments block with text after it and none
	// ending the file, which Docs reads as body text rather than as
	// comments.
	displaced bool
	// keys is every key the markup names, sorted, filled in by Scan.
	keys []string
}

func newMarkup() *Markup {
	return &Markup{begins: map[string]bool{}, ends: map[string]bool{}, entries: map[string]string{}}
}

// Comments counts the comments the markup names, by marker or by entry.
func (m *Markup) Comments() int { return len(m.keys) }

// Lost says, one phrase each, what next no longer holds of the comments
// in m, and nothing when every one survives: its entry, both of its
// markers, and the status the entry had. A status is changed in Google
// Docs, never in the text: a resolve written into the text made Docs
// fail to read the whole file (§18).
func (m *Markup) Lost(next *Markup) []string {
	var lost []string
	if next.displaced && !m.displaced {
		lost = append(lost, "the new text goes on after the gdoc_comments block, so Google Docs would show the "+
			"block as text and keep none of the comments in it")
	}
	for _, k := range m.keys {
		status, had := m.entries[k]
		now, has := next.entries[k]
		switch {
		case had && !has && !next.displaced:
			lost = append(lost, "comment "+k+" is no longer in the gdoc_comments block, which deletes it")
		case m.begins[k] && !next.begins[k], m.ends[k] && !next.ends[k]:
			lost = append(lost, "comment "+k+" lost a marker, which unpins it from its passage")
		case had && has && status != now:
			lost = append(lost, fmt.Sprintf("comment %s changes status from %s to %s; resolve or reopen it in "+
				"Google Docs instead", k, orNone(status), orNone(now)))
		}
	}
	return lost
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// scanState is where Scan is: in the body, on the line after a rule, in
// a block, or past a block's closing rule.
type scanState int

const (
	inBody scanState = iota
	afterRule
	inBlock
	afterBlock
)

// scanner holds what Scan has read so far.
type scanner struct {
	m     *Markup
	state scanState
	// block is a gdoc_comments block not yet known to end the file. If it
	// turns out not to, which is how Docs reads one with text after it,
	// the markers on its lines are the body's.
	block   *Markup
	current string
	items   int
}

// Scan reads a file and returns the markup in it. The block is the last
// one, and only if nothing but blank lines follows it; a marker counts
// wherever it is outside that block.
func Scan(r io.Reader) (*Markup, error) {
	s := &scanner{m: newMarkup()}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	for sc.Scan() {
		if err := s.line(sc.Text()); err != nil {
			return nil, err
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, fmt.Errorf("%w: a line is longer than %d MiB", ErrUncheckable, maxLine>>20)
		}
		return nil, err
	}
	switch s.state {
	case afterBlock:
		// A block ends the file, so an earlier one with text after it
		// is only body text, and the comments are all in this one.
		s.m.entries = s.block.entries
		s.m.displaced = false
	case inBlock:
		s.abandon()
	}
	seen := map[string]bool{}
	for _, set := range []map[string]bool{s.m.begins, s.m.ends} {
		maps.Copy(seen, set)
	}
	for k := range s.m.entries {
		seen[k] = true
	}
	s.m.keys = slices.Sorted(maps.Keys(seen))
	return s.m, nil
}

func (s *scanner) line(line string) error {
	switch s.state {
	case afterRule:
		if line == blockKey {
			s.block, s.current = newMarkup(), ""
			s.state = inBlock
			return nil
		}
		s.state = inBody
	case inBlock:
		switch {
		case line == rule:
			s.state = afterBlock
			return nil
		case line == "" || strings.HasPrefix(line, " "):
			return s.blockLine(line)
		}
		s.abandon()
	case afterBlock:
		if strings.TrimSpace(line) == "" {
			return nil
		}
		s.m.displaced = true
		s.abandon()
	}
	if line == rule {
		s.state = afterRule
		return nil
	}
	return s.markers(s.m, line)
}

func (s *scanner) blockLine(line string) error {
	if sub := entryRE.FindStringSubmatch(line); sub != nil {
		s.current = sub[1]
		s.block.entries[s.current] = ""
		return s.count()
	}
	if sub := statusRE.FindStringSubmatch(line); sub != nil && s.current != "" {
		s.block.entries[s.current] = sub[1]
		return nil
	}
	return s.markers(s.block, line)
}

// markers records the markers on one line in m.
func (s *scanner) markers(m *Markup, line string) error {
	if !strings.Contains(line, "gdoc_comment_") {
		return nil
	}
	for _, sub := range markerRE.FindAllStringSubmatch(line, -1) {
		if err := s.count(); err != nil {
			return err
		}
		if sub[1] == "begin" {
			m.begins[sub[2]] = true
		} else {
			m.ends[sub[2]] = true
		}
	}
	return nil
}

// abandon turns the candidate block's lines back into body text.
func (s *scanner) abandon() {
	maps.Copy(s.m.begins, s.block.begins)
	maps.Copy(s.m.ends, s.block.ends)
	s.block = nil
	s.state = inBody
}

func (s *scanner) count() error {
	s.items++
	if s.items > maxMarkup {
		return fmt.Errorf("%w: more than %d comment markers and entries", ErrUncheckable, maxMarkup)
	}
	return nil
}
