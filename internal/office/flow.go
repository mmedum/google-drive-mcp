package office

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// errFull stops a parse once the text has reached its cap.
var errFull = errors.New("the text reached its cap")

// textOut is the text being built. Writing past the cap keeps what
// fits, ending on a whole character, and reports errFull.
type textOut struct {
	b   strings.Builder
	max int
}

func (t *textOut) write(s string) error {
	room := t.max - t.b.Len()
	if len(s) <= room {
		t.b.WriteString(s)
		return nil
	}
	if room > 0 {
		cut := room
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		t.b.WriteString(s[:cut])
	}
	return errFull
}

func (t *textOut) String() string { return t.b.String() }

// grow appends s to b without letting b pass max bytes, so one
// paragraph or one cell cannot hold more than the whole text may. It
// reports whether anything was left out.
func grow(b *strings.Builder, s string, max int) bool {
	room := max - b.Len()
	if len(s) <= room {
		b.WriteString(s)
		return false
	}
	if room > 0 {
		for room > 0 && !utf8.RuneStart(s[room]) {
			room--
		}
		b.WriteString(s[:room])
	}
	return true
}

// flow lays out running text: paragraphs one to a line, headings marked
// with #, list items with -, and table rows with | between the cells.
// The document formats differ in their markup and agree on these, so
// each parser says what it found and this decides how it reads.
type flow struct {
	out    *textOut
	paras  []*para
	tables []*table
	// blank is a blank line owed before the next line, for an empty
	// paragraph between two that are not. Runs of them collapse to one,
	// and one at the very start or end is never written.
	blank bool
	lines int
	// cut records text left out of a paragraph or a cell that grew past
	// the cap, which ends the text as surely as the cap itself.
	cut bool
}

func newFlow(max int) *flow { return &flow{out: &textOut{max: max}} }

// para is one paragraph being read.
type para struct {
	b strings.Builder
	// heading is its outline level, 1 for the top; zero for body text.
	heading int
	// list is its depth in a list, 1 for the outermost; zero for none.
	list int
	// continued marks a second paragraph of one list item, which is
	// indented but carries no second bullet.
	continued bool
}

// table is one table being read.
type table struct {
	row    []string
	cell   strings.Builder
	inCell bool
	// rowRepeat and cellRepeat are how many rows or columns the open
	// row or cell stands for, in a format that writes one for several.
	rowRepeat  int
	cellRepeat int
}

func (f *flow) begin() *para {
	p := &para{}
	f.paras = append(f.paras, p)
	return p
}

func (f *flow) current() *para {
	if len(f.paras) == 0 {
		return nil
	}
	return f.paras[len(f.paras)-1]
}

func (f *flow) currentTable() *table {
	if len(f.tables) == 0 {
		return nil
	}
	return f.tables[len(f.tables)-1]
}

// text adds characters to whatever is open: the paragraph, or failing
// that the table cell.
func (f *flow) text(s string) {
	if p := f.current(); p != nil {
		f.cut = grow(&p.b, s, f.out.max) || f.cut
		return
	}
	if t := f.currentTable(); t != nil && t.inCell {
		f.cut = grow(&t.cell, s, f.out.max) || f.cut
	}
}

// end closes the open paragraph: into its table cell when it is in one,
// otherwise onto a line of its own.
func (f *flow) end() error {
	p := f.current()
	if p == nil {
		return nil
	}
	f.paras = f.paras[:len(f.paras)-1]
	s := strings.TrimRight(p.b.String(), " \t\n")
	if t := f.currentTable(); t != nil && t.inCell {
		f.cut = addToCell(t, s, f.out.max) || f.cut
		return f.full()
	}
	if strings.TrimSpace(s) == "" {
		f.blank = f.lines > 0
		return nil
	}
	if err := f.line(prefix(p) + s); err != nil {
		return err
	}
	return f.full()
}

// full is errFull once text has been left out anywhere.
func (f *flow) full() error {
	if f.cut {
		return errFull
	}
	return nil
}

// prefix marks a heading or a list item.
func prefix(p *para) string {
	switch {
	case p.heading > 0:
		return strings.Repeat("#", min(p.heading, 6)) + " "
	case p.list > 0 && p.continued:
		return strings.Repeat("  ", p.list)
	case p.list > 0:
		return strings.Repeat("  ", p.list-1) + "- "
	}
	return ""
}

// line writes one line, with the blank line owed before it.
func (f *flow) line(s string) error {
	if f.blank && f.lines > 0 {
		if err := f.out.write("\n"); err != nil {
			return err
		}
	}
	f.blank = false
	f.lines++
	return f.out.write(s + "\n")
}

// marker writes a line that heads a section, such as a slide, with a
// blank line before it.
func (f *flow) marker(s string) error {
	f.blank = true
	return f.line(s)
}

func (f *flow) startTable() { f.tables = append(f.tables, &table{}) }

// startRow opens a row standing for repeat rows; a format without
// repeats passes one.
func (f *flow) startRow(repeat int) {
	if t := f.currentTable(); t != nil {
		t.row = t.row[:0]
		t.rowRepeat = repeat
	}
}

// startCell opens a cell standing for repeat columns.
func (f *flow) startCell(repeat int) {
	if t := f.currentTable(); t != nil {
		t.cell.Reset()
		t.inCell = true
		t.cellRepeat = repeat
	}
}

// endCell closes a cell. An empty one is kept as a place: the cells
// after it are still in their columns.
func (f *flow) endCell() {
	t := f.currentTable()
	if t == nil || !t.inCell {
		return
	}
	t.inCell = false
	s := oneLine(t.cell.String())
	for range clampRepeat(t.cellRepeat, maxColumns-len(t.row)) {
		t.row = append(t.row, s)
	}
}

// endRow writes a row, as many times as it repeats. A row of nothing is
// not written. A row of a table inside a cell becomes text of that cell.
func (f *flow) endRow() error {
	t := f.currentTable()
	if t == nil {
		return nil
	}
	repeat := t.rowRepeat
	cells := t.row
	for len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	if len(cells) == 0 {
		return nil
	}
	if len(f.tables) > 1 {
		outer := f.tables[len(f.tables)-2]
		for range clampRepeat(repeat, maxRows) {
			f.cut = addToCell(outer, strings.Join(cells, " | "), f.out.max) || f.cut
		}
		return f.full()
	}
	line := "| " + strings.Join(cells, " | ") + " |"
	for range clampRepeat(repeat, maxRows) {
		if err := f.line(line); err != nil {
			return err
		}
	}
	return nil
}

func (f *flow) endTable() {
	if len(f.tables) > 0 {
		f.tables = f.tables[:len(f.tables)-1]
	}
}

// addToCell appends a paragraph's text to a cell, a space between, and
// reports whether any of it was left out.
func addToCell(t *table, s string, max int) bool {
	s = oneLine(s)
	if s == "" {
		return false
	}
	if t.cell.Len() > 0 && grow(&t.cell, " ", max) {
		return true
	}
	return grow(&t.cell, s, max)
}

// oneLine folds a cell's text onto one line, so a row stays a row.
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\n\r\t") {
		return strings.TrimSpace(s)
	}
	return strings.Join(strings.Fields(s), " ")
}

// The largest sheet Excel and LibreOffice make, which bounds what a
// repeat count may stand for.
const (
	maxRows    = 1 << 20
	maxColumns = 1 << 14
)

// clampRepeat is a repeat count a file asked for, at least one and at
// most limit.
func clampRepeat(n, limit int) int {
	switch {
	case limit < 1:
		return 0
	case n < 1:
		return 1
	case n > limit:
		return limit
	}
	return n
}
