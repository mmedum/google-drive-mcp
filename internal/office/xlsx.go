package office

import (
	"encoding/xml"
	"strconv"
	"strings"
)

// sheetRef is one sheet a workbook lists, in tab order.
type sheetRef struct {
	name   string
	hidden bool
	rel    string
}

// workbook is what the workbook part says about the sheets.
type workbook struct {
	sheets   []sheetRef
	date1904 bool
}

// maxSharedStrings bounds the shared string table, which has to be held
// whole: any cell of the sheet may name any string in it.
const maxSharedStrings = 1 << 20

// xlsx reads the first sheet of an Excel workbook as csv or tsv: the
// first in tab order that is not hidden, which is the one Excel opens
// on. A cell shows its stored value: a number as stored, without its
// display format, except a date or a time, which is shown as ISO 8601;
// a formula's last calculated value; TRUE or FALSE for a boolean.
func (p *pkg) xlsx() (*Result, error) {
	main, err := p.mainPart("xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	rels, err := p.rels(main)
	if err != nil {
		return nil, err
	}
	wb, err := p.workbook(main)
	if err != nil {
		return nil, aux(main, err)
	}
	if len(wb.sheets) == 0 {
		return &Result{}, nil
	}
	first := wb.sheets[0]
	for _, s := range wb.sheets {
		if !s.hidden {
			first = s
			break
		}
	}
	part := rels[first.rel].target
	if !p.has(part) {
		return nil, unreadable("its first sheet, %q, is missing from the file", first.name)
	}
	var kinds []numKind
	if styles := byKind(rels, "/styles"); styles != "" && p.has(styles) {
		read, err := p.cellStyles(styles)
		switch {
		case err == nil:
			kinds = read
		case !tolerable(err):
			return nil, aux(styles, err)
		}
		// A damaged style sheet costs dates their form and nothing
		// else: each shows as its serial number instead.
	}
	var shared []string
	if part := byKind(rels, "/sharedStrings"); part != "" && p.has(part) {
		if shared, err = p.sharedStrings(part); err != nil {
			return nil, aux(part, err)
		}
	}
	g := &grid{out: &textOut{max: p.o.MaxText}, delim: p.o.Delimiter}
	res, err := finish(g.out, p.sheetRows(part, shared, kinds, wb.date1904, g))
	if res != nil {
		res.Sheet, res.Sheets = first.name, len(wb.sheets)
	}
	return res, err
}

func (p *pkg) workbook(part string) (*workbook, error) {
	wb := &workbook{}
	err := p.walk(part, func(w *walker, t xml.Token) error {
		se, ok := t.(xml.StartElement)
		if !ok || !in(se.Name, nsS) {
			return nil
		}
		switch se.Name.Local {
		case "workbookPr":
			switch attr(se, "date1904") {
			case "1", "true", "on":
				wb.date1904 = true
			}
		case "sheet":
			if is(w.parent(), "sheets", nsS) {
				state := attr(se, "state")
				wb.sheets = append(wb.sheets, sheetRef{
					name: attr(se, "name"), hidden: state == "hidden" || state == "veryHidden",
					rel: attrIn(se, "id", nsR),
				})
			}
		}
		return nil
	})
	return wb, err
}

// cellStyles reads, for each cell format a cell may name by its s
// attribute, whether its number format shows a date or a time.
func (p *pkg) cellStyles(part string) ([]numKind, error) {
	custom := map[int]string{}
	var ids []int
	err := p.walk(part, func(w *walker, t xml.Token) error {
		se, ok := t.(xml.StartElement)
		if !ok || !in(se.Name, nsS) {
			return nil
		}
		switch {
		case se.Name.Local == "numFmt" && is(w.parent(), "numFmts", nsS):
			if id, err := strconv.Atoi(attr(se, "numFmtId")); err == nil {
				custom[id] = attr(se, "formatCode")
			}
		case se.Name.Local == "xf" && is(w.parent(), "cellXfs", nsS) && len(ids) < 1<<16:
			id, _ := strconv.Atoi(attr(se, "numFmtId"))
			ids = append(ids, id)
		}
		return nil
	})
	kinds := make([]numKind, len(ids))
	for i, id := range ids {
		if code, ok := custom[id]; ok {
			kinds[i] = formatKind(code)
		} else {
			kinds[i] = builtinKind(id)
		}
	}
	return kinds, err
}

// sharedStrings reads the workbook's string table. A string with runs
// of formatting is its runs put together; the phonetic guide a Japanese
// string may carry is left out, as Excel leaves it out of the cell.
func (p *pkg) sharedStrings(part string) ([]string, error) {
	var out []string
	var cur strings.Builder
	total := 0
	err := p.walk(part, func(w *walker, t xml.Token) error {
		switch t := t.(type) {
		case xml.StartElement:
			switch {
			case is(t.Name, "si", nsS):
				cur.Reset()
			case is(t.Name, "rPh", nsS):
				w.skip()
			}
		case xml.CharData:
			if is(w.top(), "t", nsS) && w.inside("si") {
				grow(&cur, string(t), p.o.MaxText)
			}
		case xml.EndElement:
			if is(t.Name, "si", nsS) {
				if len(out) >= maxSharedStrings {
					return overLimit("its shared string table holds more than %d strings", maxSharedStrings)
				}
				total += cur.Len()
				if total > p.o.MaxText {
					return overLimit("its shared strings hold more than %d bytes of text", p.o.MaxText)
				}
				out = append(out, cur.String())
			}
		}
		return nil
	})
	return out, err
}

// cell is the cell being read.
type cell struct {
	col    int
	kind   string
	style  int
	value  strings.Builder
	inline strings.Builder
}

func (p *pkg) sheetRows(part string, shared []string, kinds []numKind, date1904 bool, g *grid) error {
	var (
		row     []string
		rowNum  int
		nextCol int
		c       cell
	)
	return p.walk(part, func(w *walker, t xml.Token) error {
		switch t := t.(type) {
		case xml.StartElement:
			if !in(t.Name, nsS) {
				return nil
			}
			switch t.Name.Local {
			case "row":
				if n, err := strconv.Atoi(attr(t, "r")); err == nil && n > rowNum {
					rowNum = n
				} else {
					rowNum++
				}
				row, nextCol = row[:0], 0
			case "c":
				c.col = nextCol
				if col, _, ok := cellRef(attr(t, "r")); ok {
					c.col = col
				}
				c.kind = attr(t, "t")
				c.style = -1
				if s, err := strconv.Atoi(attr(t, "s")); err == nil {
					c.style = s
				}
				c.value.Reset()
				c.inline.Reset()
			case "f", "rPh", "extLst":
				w.skip()
			}
		case xml.CharData:
			top := w.top()
			switch {
			case is(top, "v", nsS):
				grow(&c.value, string(t), p.o.MaxText)
			case is(top, "t", nsS) && w.inside("is"):
				grow(&c.inline, string(t), p.o.MaxText)
			}
		case xml.EndElement:
			switch {
			case is(t.Name, "c", nsS):
				v, err := c.text(shared, kinds, date1904)
				if err != nil {
					return err
				}
				row = setCell(row, c.col, v)
				nextCol = c.col + 1
			case is(t.Name, "row", nsS):
				return g.row(rowNum, row)
			}
		}
		return nil
	})
}

// text is what the cell shows, by its type.
func (c *cell) text(shared []string, kinds []numKind, date1904 bool) (string, error) {
	v := strings.TrimSpace(c.value.String())
	switch c.kind {
	case "s":
		i, err := strconv.Atoi(v)
		if err != nil || i < 0 || i >= len(shared) {
			return "", unreadable("a cell names shared string %q, which the file does not have", v)
		}
		return shared[i], nil
	case "inlineStr":
		return c.inline.String(), nil
	case "b":
		switch v {
		case "1":
			return "TRUE", nil
		case "0":
			return "FALSE", nil
		}
		return v, nil
	case "str", "e", "d":
		return c.value.String(), nil
	}
	if v == "" || c.style < 0 || c.style >= len(kinds) || kinds[c.style] == numPlain {
		return v, nil
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		if s, ok := serialDate(n, kinds[c.style], date1904); ok {
			return s, nil
		}
	}
	return v, nil
}
