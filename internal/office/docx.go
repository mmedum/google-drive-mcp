package office

import (
	"encoding/xml"
	"strconv"
	"strings"
)

// wordStyle is what a paragraph style says about structure: whether it
// is a heading and at what level, and whether it numbers its paragraphs
// as a list.
type wordStyle struct {
	heading int
	list    bool
}

// docx reads a Word document's body in order. Deleted text and moved-away
// text from tracked changes are left out, as are field codes, comments,
// footnotes, headers and footers.
func (p *pkg) docx() (*Result, error) {
	main, err := p.mainPart("word/document.xml")
	if err != nil {
		return nil, err
	}
	rels, err := p.rels(main)
	if err != nil {
		return nil, err
	}
	styles := map[string]wordStyle{}
	if part := byKind(rels, "/styles"); part != "" && p.has(part) {
		read, err := p.wordStyles(part)
		switch {
		case err == nil:
			styles = read
		case !tolerable(err):
			return nil, aux(part, err)
		}
		// A damaged style sheet costs the headings their marks and
		// nothing else, which is better than refusing the text.
	}
	f := newFlow(p.o.MaxText)
	return finish(f.out, p.wordBody(main, styles, f))
}

// wordBody walks the document part.
func (p *pkg) wordBody(part string, styles map[string]wordStyle, f *flow) error {
	return p.walk(part, func(w *walker, t xml.Token) error {
		switch t := t.(type) {
		case xml.StartElement:
			if len(w.stack) == 1 && !is(t.Name, "document", nsW) {
				return notKind(part, "Word document", t.Name)
			}
			return wordStart(w, t, styles, f)
		case xml.EndElement:
			if !in(t.Name, nsW) {
				return nil
			}
			switch t.Name.Local {
			case "p":
				return f.end()
			case "tc":
				f.endCell()
			case "tr":
				return f.endRow()
			case "tbl":
				f.endTable()
			}
		case xml.CharData:
			if top := w.top(); top.Local == "t" && (in(top, nsW) || in(top, nsMath)) {
				f.text(string(t))
			}
		}
		return nil
	})
}

// wordSkipped are the elements whose contents are not the document's
// text: run and section properties, the old properties a tracked change
// kept, deleted and moved-away text, field codes, and a content
// control's settings.
var wordSkipped = map[string]bool{
	"rPr": true, "sectPr": true, "del": true, "moveFrom": true,
	"instrText": true, "delText": true, "delInstrText": true,
	"sdtPr": true, "sdtEndPr": true, "tblPr": true, "trPr": true, "tcPr": true, "tblGrid": true,
	"footnoteReference": true, "endnoteReference": true, "commentReference": true,
}

func wordStart(w *walker, t xml.StartElement, styles map[string]wordStyle, f *flow) error {
	if fallback(t.Name) {
		w.skip()
		return nil
	}
	if !in(t.Name, nsW) {
		return nil
	}
	local := t.Name.Local
	if wordSkipped[local] || strings.HasSuffix(local, "PrChange") {
		w.skip()
		return nil
	}
	switch local {
	case "p":
		f.begin()
	case "pStyle", "outlineLvl", "ilvl", "numId":
		wordParagraphProperty(w, t, styles, f.current())
	case "tab", "br", "cr", "noBreakHyphen":
		if is(w.parent(), "r", nsW) {
			f.text(wordRunText[local])
		}
	case "tbl":
		f.startTable()
	case "tr":
		f.startRow(1)
	case "tc":
		f.startCell(1)
	}
	return nil
}

// wordRunText is what the empty elements of a run stand for.
var wordRunText = map[string]string{"tab": "\t", "br": "\n", "cr": "\n", "noBreakHyphen": "-"}

// wordParagraphProperty reads what a paragraph's properties say about
// structure: its style, its outline level, and whether it is a list
// item and how deep.
func wordParagraphProperty(w *walker, t xml.StartElement, styles map[string]wordStyle, p *para) {
	if p == nil {
		return
	}
	level, err := strconv.Atoi(attr(t, "val"))
	inRange := err == nil && level >= 0 && level < 9
	switch t.Name.Local {
	case "pStyle":
		if !is(w.parent(), "pPr", nsW) {
			return
		}
		if s, ok := styles[attr(t, "val")]; ok {
			if s.heading > 0 && p.heading == 0 {
				p.heading = s.heading
			}
			if s.list && p.list == 0 {
				p.list = 1
			}
		}
	case "outlineLvl":
		if inRange && is(w.parent(), "pPr", nsW) {
			p.heading = level + 1
		}
	case "ilvl":
		if inRange && is(w.parent(), "numPr", nsW) {
			p.list = level + 1
		}
	case "numId":
		if !is(w.parent(), "numPr", nsW) {
			return
		}
		// Numbering id zero is how Word says "no list", for a paragraph
		// whose style would otherwise make it one.
		if attr(t, "val") == "0" {
			p.list = 0
		} else if p.list == 0 {
			p.list = 1
		}
	}
}

// wordStyles reads which paragraph styles are headings or lists, by the
// style's name, "heading 1" to "heading 9" or "Title", and by an outline
// level on it. A localized Word can translate a style's id, so the id is
// never what decides; whether it keeps the English name is recorded in
// §18 as unverified.
func (p *pkg) wordStyles(part string) (map[string]wordStyle, error) {
	r := &styleReader{part: part, out: map[string]wordStyle{}}
	err := p.walk(part, func(w *walker, t xml.Token) error {
		switch t := t.(type) {
		case xml.StartElement:
			if in(t.Name, nsW) {
				return r.start(w, t)
			}
		case xml.EndElement:
			if is(t.Name, "style", nsW) {
				r.current = ""
			}
		}
		return nil
	})
	return r.out, err
}

// styleReader is a style sheet being read: the styles so far, and the
// paragraph style whose definition is open.
type styleReader struct {
	part    string
	out     map[string]wordStyle
	current string
}

func (r *styleReader) start(w *walker, t xml.StartElement) error {
	if t.Name.Local == "style" {
		r.current = ""
		if attr(t, "type") == "paragraph" {
			r.current = attr(t, "styleId")
		}
		if _, seen := r.out[r.current]; r.current != "" && !seen && len(r.out) >= MaxListed {
			return listed("styles", r.part)
		}
		return nil
	}
	if r.current == "" || !w.inside("style") {
		return nil
	}
	s := r.out[r.current]
	switch t.Name.Local {
	case "name":
		if !is(w.parent(), "style", nsW) {
			return nil
		}
		name := strings.ToLower(strings.TrimSpace(attr(t, "val")))
		if level, ok := strings.CutPrefix(name, "heading "); ok {
			if n, err := strconv.Atoi(level); err == nil && n >= 1 && n <= 9 {
				s.heading = n
			}
		} else if name == "title" {
			s.heading = 1
		}
	case "outlineLvl":
		if n, err := strconv.Atoi(attr(t, "val")); err == nil && n >= 0 && n < 9 {
			s.heading = n + 1
		}
	case "numId":
		s.list = attr(t, "val") != "0"
	default:
		return nil
	}
	r.out[r.current] = s
	return nil
}
