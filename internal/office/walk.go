package office

import (
	"encoding/xml"
	"errors"
	"io"
	"slices"
)

// walker is one part being parsed: the decoder and the elements open
// around the token being handled.
type walker struct {
	part  string
	stack []xml.Name
	// skipAt is the depth of an element whose contents are being passed
	// over, or zero.
	skipAt int
}

// walk parses one part and hands fn every token that is not inside an
// element fn chose to skip. A start element is on the stack when fn
// sees it, and an end element is still on it.
//
// encoding/xml expands only the five predefined entities and refuses
// any other, so a part cannot define the entity that expands into a
// billion copies of itself. Nesting is bounded here, and the bytes it
// reads are bounded by the part's reader.
func (p *pkg) walk(name string, fn func(w *walker, t xml.Token) error) error {
	rc, err := p.open(name)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	dec := xml.NewDecoder(rc)
	w := &walker{part: name}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return w.fail(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(w.stack) >= MaxDepth {
				return overLimit("its part %s nests more than %d levels deep", name, MaxDepth)
			}
			w.stack = append(w.stack, t.Name)
			if w.skipAt == 0 {
				if err := fn(w, t); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if w.skipAt == len(w.stack) {
				w.skipAt = 0
			} else if w.skipAt == 0 {
				if err := fn(w, t); err != nil {
					return err
				}
			}
			w.stack = w.stack[:len(w.stack)-1]
		case xml.CharData:
			if w.skipAt == 0 {
				if err := fn(w, t); err != nil {
					return err
				}
			}
		}
	}
}

// skip passes over the contents of the element just started.
func (w *walker) skip() { w.skipAt = len(w.stack) }

// top is the innermost open element.
func (w *walker) top() xml.Name {
	if len(w.stack) == 0 {
		return xml.Name{}
	}
	return w.stack[len(w.stack)-1]
}

// parent is the element around the innermost one.
func (w *walker) parent() xml.Name {
	if len(w.stack) < 2 {
		return xml.Name{}
	}
	return w.stack[len(w.stack)-2]
}

// inside reports whether an element of that local name is open
// anywhere around the current token.
func (w *walker) inside(local string) bool {
	return slices.ContainsFunc(w.stack, func(n xml.Name) bool { return n.Local == local })
}

func (w *walker) fail(err error) error {
	var syntax *xml.SyntaxError
	if errors.As(err, &syntax) {
		return unreadable("its part %s is not well-formed XML: %v", w.part, syntax.Msg)
	}
	var oe *Error
	if errors.As(err, &oe) || stopped(err) {
		return err
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return unreadable("its part %s ends in the middle", w.part)
	}
	return err
}

// attr is an attribute by local name, whatever its namespace.
func attr(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local && a.Name.Space != "xmlns" {
			return a.Value
		}
	}
	return ""
}

// attrIn is an attribute by local name in one of the given namespaces,
// for an element that carries two attributes of one local name.
func attrIn(se xml.StartElement, local string, spaces []string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local && slices.Contains(spaces, a.Name.Space) {
			return a.Value
		}
	}
	return ""
}

// Namespaces. OOXML has two spellings of each, transitional and strict;
// a file uses one or the other.
var (
	nsW = []string{
		"http://schemas.openxmlformats.org/wordprocessingml/2006/main",
		"http://purl.oclc.org/ooxml/wordprocessingml/main",
	}
	nsMath = []string{
		"http://schemas.openxmlformats.org/officeDocument/2006/math",
		"http://purl.oclc.org/ooxml/officeDocument/math",
	}
	nsA = []string{
		"http://schemas.openxmlformats.org/drawingml/2006/main",
		"http://purl.oclc.org/ooxml/drawingml/main",
	}
	nsP = []string{
		"http://schemas.openxmlformats.org/presentationml/2006/main",
		"http://purl.oclc.org/ooxml/presentationml/main",
	}
	nsS = []string{
		"http://schemas.openxmlformats.org/spreadsheetml/2006/main",
		"http://purl.oclc.org/ooxml/spreadsheetml/main",
	}
	nsR = []string{
		"http://schemas.openxmlformats.org/officeDocument/2006/relationships",
		"http://purl.oclc.org/ooxml/officeDocument/relationships",
	}
	nsMC = []string{"http://schemas.openxmlformats.org/markup-compatibility/2006"}
)

// ODF namespaces, which have one spelling each.
const (
	odfOffice       = "urn:oasis:names:tc:opendocument:xmlns:office:1.0"
	odfText         = "urn:oasis:names:tc:opendocument:xmlns:text:1.0"
	odfTable        = "urn:oasis:names:tc:opendocument:xmlns:table:1.0"
	odfDraw         = "urn:oasis:names:tc:opendocument:xmlns:drawing:1.0"
	odfPresentation = "urn:oasis:names:tc:opendocument:xmlns:presentation:1.0"
	odfSVG          = "urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0"
)

// is reports whether a name is local in one of the namespaces.
func is(n xml.Name, local string, spaces []string) bool {
	return n.Local == local && slices.Contains(spaces, n.Space)
}

// in reports whether a name is in one of the namespaces.
func in(n xml.Name, spaces []string) bool { return slices.Contains(spaces, n.Space) }

// odf reports whether a name is local in one ODF namespace.
func odf(n xml.Name, space, local string) bool { return n.Space == space && n.Local == local }

// fallback reports an mc:Fallback, the second copy of content a file
// carries for readers that do not know the first. Reading both would
// say everything in a text box twice.
func fallback(n xml.Name) bool { return is(n, "Fallback", nsMC) }
