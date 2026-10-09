package office

import (
	"encoding/xml"
	"fmt"
)

// pptx reads a PowerPoint deck slide by slide, in the order the deck
// shows them, each under a line naming the slide. Speaker notes, the
// layouts and masters behind a slide, and every image are left out.
func (p *pkg) pptx() (*Result, error) {
	main, err := p.mainPart("ppt/presentation.xml")
	if err != nil {
		return nil, err
	}
	rels, err := p.rels(main)
	if err != nil {
		return nil, err
	}
	var order []string
	err = p.walk(main, func(w *walker, t xml.Token) error {
		se, ok := t.(xml.StartElement)
		switch {
		case !ok:
		case len(w.stack) == 1 && !is(se.Name, "presentation", nsP):
			return notKind(main, "PowerPoint presentation", se.Name)
		case is(se.Name, "sldId", nsP):
			if len(order) >= MaxListed {
				return listed("slides", main)
			}
			order = append(order, attrIn(se, "id", nsR))
		}
		return nil
	})
	if err != nil {
		return nil, aux(main, err)
	}
	var slides []string
	for _, id := range order {
		if r, ok := rels[id]; ok && p.has(r.target) {
			slides = append(slides, r.target)
		}
	}
	f := newFlow(p.o.MaxText)
	res, err := finish(f.out, p.slides(slides, f))
	if res != nil {
		res.Slides = len(slides)
	}
	return res, err
}

func (p *pkg) slides(parts []string, f *flow) error {
	for i, part := range parts {
		if err := p.slide(part, i+1, f); err != nil {
			return err
		}
	}
	return nil
}

// slide reads one slide's text. A slide hidden from the show says so in
// its line, since a model asked about the deck may need to know which
// slides a viewer never sees.
func (p *pkg) slide(part string, n int, f *flow) error {
	return p.walk(part, func(w *walker, t xml.Token) error {
		switch t := t.(type) {
		case xml.StartElement:
			if len(w.stack) == 1 {
				hidden := ""
				if v := attr(t, "show"); v == "0" || v == "false" {
					hidden = " (hidden)"
				}
				return f.marker(fmt.Sprintf("--- slide %d%s ---", n, hidden))
			}
			return drawingStart(w, t, f)
		case xml.EndElement:
			return drawingEnd(t, f)
		case xml.CharData:
			if is(w.top(), "t", nsA) {
				f.text(string(t))
			}
		}
		return nil
	})
}

// drawingStart handles DrawingML, where a slide keeps its text: a:p is
// a paragraph, a:br a line break, and a:tbl a table.
func drawingStart(w *walker, t xml.StartElement, f *flow) error {
	if fallback(t.Name) {
		w.skip()
		return nil
	}
	if !in(t.Name, nsA) {
		return nil
	}
	switch t.Name.Local {
	case "p":
		f.begin()
	case "br":
		f.text("\n")
	case "tbl":
		f.startTable()
	case "tr":
		f.startRow(1)
	case "tc":
		f.startCell(1)
	case "rPr", "pPr", "endParaRPr", "tblPr", "tcPr", "tblGrid":
		w.skip()
	}
	return nil
}

func drawingEnd(t xml.EndElement, f *flow) error {
	if !in(t.Name, nsA) {
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
	return nil
}
