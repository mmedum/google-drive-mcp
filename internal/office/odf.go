package office

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// An OpenDocument file keeps all three kinds in one part, content.xml,
// and spells paragraphs, lists and tables the same way in each. What
// differs is the body: running text, a sheet's rows, or a deck's pages.

const odfContent = "content.xml"

// odt reads an OpenDocument text in order. Tracked deletions, comments,
// footnotes and the templates of an index are left out.
func (p *pkg) odt() (*Result, error) {
	if !p.has(odfContent) {
		return nil, unreadable("it has no %s, which is where such a file keeps its content", odfContent)
	}
	f := newFlow(p.o.MaxText)
	lists := &odfLists{}
	return finish(f.out, p.walk(odfContent, func(w *walker, t xml.Token) error {
		return odfRun(w, t, f, lists)
	}))
}

// odp reads an OpenDocument presentation page by page, each under a
// line naming the slide. Speaker notes are left out.
func (p *pkg) odp() (*Result, error) {
	if !p.has(odfContent) {
		return nil, unreadable("it has no %s, which is where such a file keeps its content", odfContent)
	}
	f := newFlow(p.o.MaxText)
	lists := &odfLists{}
	pages := 0
	res, err := finish(f.out, p.walk(odfContent, func(w *walker, t xml.Token) error {
		if se, ok := t.(xml.StartElement); ok && odf(se.Name, odfDraw, "page") {
			pages++
			return f.marker(fmt.Sprintf("--- slide %d ---", pages))
		}
		return odfRun(w, t, f, lists)
	}))
	if res != nil {
		res.Slides = pages
	}
	return res, err
}

// odfLists tracks the list items a paragraph sits in.
type odfLists struct {
	depth int
	// fresh marks a list item whose first paragraph has not been seen,
	// which is the one that carries the bullet.
	fresh bool
}

// odfSkipped are the elements whose contents are not the text: tracked
// changes (which hold the deleted text), comments, footnotes and
// endnotes, speaker notes, form controls, and an image's alternative
// text.
func odfSkipped(n xml.Name) bool {
	switch n.Space {
	case odfOffice:
		return n.Local == "annotation" || n.Local == "forms"
	case odfText:
		// An index carries the templates it is built from in a -source
		// element; the index itself is ordinary paragraphs.
		return n.Local == "tracked-changes" || n.Local == "note" || strings.HasSuffix(n.Local, "-source")
	case odfPresentation:
		return n.Local == "notes"
	case odfSVG:
		return n.Local == "title" || n.Local == "desc"
	}
	return false
}

// odfRun handles the markup the three kinds share: paragraphs,
// headings, lists, tables and the inline elements that stand for
// spaces, tabs and line breaks.
func odfRun(w *walker, t xml.Token, f *flow, lists *odfLists) error {
	switch t := t.(type) {
	case xml.StartElement:
		if odfSkipped(t.Name) {
			w.skip()
			return nil
		}
		odfStart(t, f, lists)
	case xml.EndElement:
		switch {
		case odf(t.Name, odfText, "p"), odf(t.Name, odfText, "h"):
			return f.end()
		case odf(t.Name, odfText, "list"):
			lists.depth--
		case odf(t.Name, odfTable, "table-cell"), odf(t.Name, odfTable, "covered-table-cell"):
			f.endCell()
		case odf(t.Name, odfTable, "table-row"):
			return f.endRow()
		case odf(t.Name, odfTable, "table"):
			f.endTable()
		}
	case xml.CharData:
		// Text lives in paragraphs; whitespace between elements does not
		// count, which is how ODF says to read it.
		if w.inside("p") || w.inside("h") {
			f.text(string(t))
		}
	}
	return nil
}

func odfStart(t xml.StartElement, f *flow, lists *odfLists) {
	switch {
	case odf(t.Name, odfText, "p"), odf(t.Name, odfText, "h"):
		p := f.begin()
		if t.Name.Local == "h" {
			p.heading = 1
			if n, err := strconv.Atoi(attr(t, "outline-level")); err == nil && n > 0 {
				p.heading = n
			}
		} else if lists.depth > 0 {
			p.list, p.continued = lists.depth, !lists.fresh
			lists.fresh = false
		}
	case odf(t.Name, odfText, "list"):
		lists.depth++
	case odf(t.Name, odfText, "list-item"), odf(t.Name, odfText, "list-header"):
		lists.fresh = true
	case odf(t.Name, odfText, "s"), odf(t.Name, odfText, "tab"), odf(t.Name, odfText, "line-break"):
		f.text(odfSpacing(t))
	case odf(t.Name, odfTable, "table"):
		f.startTable()
	case odf(t.Name, odfTable, "table-row"):
		f.startRow(repeatOf(t, "number-rows-repeated"))
	case odf(t.Name, odfTable, "table-cell"), odf(t.Name, odfTable, "covered-table-cell"):
		f.startCell(repeatOf(t, "number-columns-repeated"))
	}
}

// odfSpacing is what text:s, text:tab and text:line-break stand for:
// ODF collapses runs of spaces, so a run that matters is an element.
func odfSpacing(t xml.StartElement) string {
	switch t.Name.Local {
	case "tab":
		return "\t"
	case "line-break":
		return "\n"
	}
	n, err := strconv.Atoi(attr(t, "c"))
	if err != nil || n < 1 {
		n = 1
	}
	return strings.Repeat(" ", min(n, 1000))
}

// ods reads the first sheet of an OpenDocument spreadsheet as csv or
// tsv, by the same rules as an Excel workbook: a number as stored, a
// date or time as ISO 8601, a boolean as TRUE or FALSE, and a cell of
// text as its paragraphs, one to a line.
func (p *pkg) ods() (*Result, error) {
	if !p.has(odfContent) {
		return nil, unreadable("it has no %s, which is where such a file keeps its content", odfContent)
	}
	s := &odsSheet{grid: &grid{out: &textOut{max: p.o.MaxText}, delim: p.o.Delimiter}, max: p.o.MaxText}
	err := p.walk(odfContent, s.token)
	if errors.Is(err, errDone) {
		err = nil
	}
	res, err := finish(s.grid.out, err)
	if res != nil {
		res.Sheet = s.name
	}
	return res, err
}

// errDone ends a walk early, once the first sheet has been read.
var errDone = errors.New("done")

func orElse(first, second string) string {
	if first != "" {
		return first
	}
	return second
}

// odsSheet is the first sheet being read.
type odsSheet struct {
	grid *grid
	max  int
	name string
	// started is set inside the first table. nested counts tables open
	// inside its cells, whose rows are that cell's text, not the sheet's.
	started bool
	nested  int
	row     []string
	rowNum  int
	repeat  int
	col     int
	// The cell being read: its value when the value is an attribute,
	// its text otherwise, and how many columns it stands for.
	value     string
	hasValue  bool
	text      strings.Builder
	paras     int
	colRepeat int
}

func (s *odsSheet) token(w *walker, t xml.Token) error {
	switch t := t.(type) {
	case xml.StartElement:
		if odfSkipped(t.Name) {
			w.skip()
			return nil
		}
		if odf(t.Name, odfTable, "table") {
			if s.started {
				s.nested++
			}
			s.started, s.name = true, orElse(s.name, attr(t, "name"))
			return nil
		}
		if !s.started {
			return nil
		}
		s.cellStart(t)
	case xml.CharData:
		if s.started && w.inside("p") {
			grow(&s.text, string(t), s.max)
		}
	case xml.EndElement:
		if !s.started {
			return nil
		}
		switch {
		case odf(t.Name, odfTable, "table"):
			if s.nested == 0 {
				return errDone
			}
			s.nested--
		case s.nested > 0:
		case odf(t.Name, odfTable, "table-cell"), odf(t.Name, odfTable, "covered-table-cell"):
			s.endCell()
		case odf(t.Name, odfTable, "table-row"):
			return s.endRow()
		}
	}
	return nil
}

// cellStart handles what opens inside the sheet: a row, a cell, and the
// paragraphs and spacing elements of a cell's text.
func (s *odsSheet) cellStart(t xml.StartElement) {
	switch {
	case s.nested > 0 && !odf(t.Name, odfText, "p"):
	case odf(t.Name, odfTable, "table-row"):
		s.row, s.col = s.row[:0], 0
		s.repeat = repeatOf(t, "number-rows-repeated")
	case odf(t.Name, odfTable, "table-cell"), odf(t.Name, odfTable, "covered-table-cell"):
		s.value, s.hasValue = odsValue(t)
		s.text.Reset()
		s.paras = 0
		s.colRepeat = repeatOf(t, "number-columns-repeated")
	case odf(t.Name, odfText, "p"):
		if s.paras > 0 {
			grow(&s.text, "\n", s.max)
		}
		s.paras++
	case odf(t.Name, odfText, "s"), odf(t.Name, odfText, "tab"), odf(t.Name, odfText, "line-break"):
		grow(&s.text, odfSpacing(t), s.max)
	}
}

// endCell places a cell's value in as many columns as it repeats over.
// An empty cell repeated to the edge of the sheet, which is how
// LibreOffice ends a row, costs nothing.
func (s *odsSheet) endCell() {
	v := s.text.String()
	if s.hasValue {
		v = s.value
	}
	if v == "" {
		s.col += s.colRepeat
		return
	}
	for range clampRepeat(s.colRepeat, maxColumns-s.col) {
		s.row = setCell(s.row, s.col, v)
		s.col++
	}
}

// endRow writes a row as many times as it repeats. An empty row that
// repeats a million times, which is how LibreOffice ends a sheet, costs
// nothing.
func (s *odsSheet) endRow() error {
	empty := true
	for _, c := range s.row {
		if c != "" {
			empty = false
			break
		}
	}
	if empty {
		s.rowNum += s.repeat
		return nil
	}
	for range clampRepeat(s.repeat, maxRows) {
		s.rowNum++
		if err := s.grid.row(s.rowNum, s.row); err != nil {
			return err
		}
	}
	return nil
}

// repeatOf reads a repeat count, at least one.
func repeatOf(se xml.StartElement, name string) int {
	n, err := strconv.Atoi(attr(se, name))
	if err != nil || n < 1 {
		return 1
	}
	return min(n, maxRows)
}

// odsValue is a cell's value when ODF keeps it in an attribute: the
// number as stored for a number, a percentage or an amount of money,
// the date or time as ISO 8601, and TRUE or FALSE. A cell of text has
// none, and its paragraphs are its value.
func odsValue(se xml.StartElement) (string, bool) {
	switch attr(se, "value-type") {
	case "float", "percentage", "currency":
		return attr(se, "value"), true
	case "date":
		return strings.Replace(attr(se, "date-value"), "T", " ", 1), true
	case "time":
		return odfDuration(attr(se, "time-value")), true
	case "boolean":
		switch attr(se, "boolean-value") {
		case "true":
			return "TRUE", true
		case "false":
			return "FALSE", true
		}
	}
	return "", false
}

// odfDuration turns an ODF time, which is an ISO 8601 duration such as
// PT09H30M00S, into hours, minutes and seconds. Anything else is shown
// as it was written.
func odfDuration(v string) string {
	rest, ok := strings.CutPrefix(v, "PT")
	if !ok {
		return v
	}
	var h, m int
	var sec float64
	for _, unit := range []byte{'H', 'M', 'S'} {
		i := strings.IndexByte(rest, unit)
		if i < 0 {
			continue
		}
		n, err := strconv.ParseFloat(rest[:i], 64)
		if err != nil || n < 0 {
			return v
		}
		switch unit {
		case 'H':
			h = int(n)
		case 'M':
			m = int(n)
		case 'S':
			sec = n
		}
		rest = rest[i+1:]
	}
	if rest != "" {
		return v
	}
	return fmt.Sprintf("%02d:%02d:%02d", h, m, int(sec+0.5))
}
