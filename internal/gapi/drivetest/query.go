package drivetest

import (
	"fmt"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// The fake understands the part of Drive's query language this server
// emits, and it enforces the semantics the reference documents rather
// than the ones people assume: `name contains 'x'` matches names whose
// *words start with* x, not any substring, and `fullText contains 'x'`
// matches whole tokens unless x is a double-quoted phrase. A fake that
// did substring matching would let a wrong query pass every test.

type token struct {
	kind int // tokIdent, tokString, tokOp, tokLParen, tokRParen
	text string
}

const (
	tokIdent = iota
	tokString
	tokOp
	tokLParen
	tokRParen
)

func lex(q string) ([]token, error) {
	var out []token
	for i := 0; i < len(q); {
		c := q[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '(':
			out = append(out, token{tokLParen, "("})
			i++
		case c == ')':
			out = append(out, token{tokRParen, ")"})
			i++
		case c == '\'':
			// Single quotes and backslashes inside a literal are escaped
			// with a backslash, per the search guide.
			var sb strings.Builder
			i++
			for i < len(q) && q[i] != '\'' {
				if q[i] == '\\' && i+1 < len(q) {
					i++
				}
				sb.WriteByte(q[i])
				i++
			}
			if i >= len(q) {
				return nil, fmt.Errorf("unterminated string literal")
			}
			i++
			out = append(out, token{tokString, sb.String()})
		case strings.ContainsRune("<>=!", rune(c)):
			j := i + 1
			if j < len(q) && q[j] == '=' {
				j++
			}
			out = append(out, token{tokOp, q[i:j]})
			i = j
		default:
			j := i
			for j < len(q) && (isWordByte(q[j])) {
				j++
			}
			if j == i {
				return nil, fmt.Errorf("unexpected character %q in query", string(c))
			}
			out = append(out, token{tokIdent, q[i:j]})
			i = j
		}
	}
	return out, nil
}

func isWordByte(b byte) bool {
	return b == '_' || b == '.' || b == '-' || b == '@' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() (token, bool) {
	if p.pos >= len(p.toks) {
		return token{}, false
	}
	return p.toks[p.pos], true
}

func (p *parser) next() (token, bool) {
	t, ok := p.peek()
	if ok {
		p.pos++
	}
	return t, ok
}

type predicate func(f *gdrive.File, s *Server) bool

// parseQuery turns a Drive query into a predicate. An unsupported
// construct is an error rather than a silent match, so a test that
// exercises new syntax fails loudly here instead of passing wrongly.
func parseQuery(q string) (predicate, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return func(*gdrive.File, *Server) bool { return true }, nil
	}
	toks, err := lex(q)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	pred, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("trailing tokens in query at %q", p.toks[p.pos].text)
	}
	return pred, nil
}

func (p *parser) parseOr() (predicate, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind != tokIdent || !strings.EqualFold(t.text, "or") {
			return left, nil
		}
		p.pos++
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(f *gdrive.File, s *Server) bool { return l(f, s) || r(f, s) }
	}
}

func (p *parser) parseAnd() (predicate, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind != tokIdent || !strings.EqualFold(t.text, "and") {
			return left, nil
		}
		p.pos++
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(f *gdrive.File, s *Server) bool { return l(f, s) && r(f, s) }
	}
}

func (p *parser) parseUnary() (predicate, error) {
	if t, ok := p.peek(); ok && t.kind == tokIdent && strings.EqualFold(t.text, "not") {
		p.pos++
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return func(f *gdrive.File, s *Server) bool { return !inner(f, s) }, nil
	}
	return p.parseTerm()
}

func (p *parser) parseTerm() (predicate, error) {
	t, ok := p.next()
	if !ok {
		return nil, fmt.Errorf("query ended early")
	}
	if t.kind == tokLParen {
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if c, ok := p.next(); !ok || c.kind != tokRParen {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		return inner, nil
	}

	// `'value' in field`, the collection form.
	if t.kind == tokString {
		op, ok := p.next()
		if !ok || op.kind != tokIdent || !strings.EqualFold(op.text, "in") {
			return nil, fmt.Errorf("expected `in` after a literal, got %q", op.text)
		}
		field, ok := p.next()
		if !ok || field.kind != tokIdent {
			return nil, fmt.Errorf("expected a field after `in`")
		}
		return inPredicate(field.text, t.text)
	}

	if t.kind != tokIdent {
		return nil, fmt.Errorf("unexpected token %q", t.text)
	}
	field := t.text
	op, ok := p.next()
	if !ok {
		return nil, fmt.Errorf("field %q without an operator", field)
	}
	opText := op.text
	if op.kind == tokIdent {
		if !strings.EqualFold(opText, "contains") {
			return nil, fmt.Errorf("unsupported operator %q", opText)
		}
		opText = "contains"
	} else if op.kind != tokOp {
		return nil, fmt.Errorf("unsupported operator %q", opText)
	}
	val, ok := p.next()
	if !ok {
		return nil, fmt.Errorf("operator %q without a value", opText)
	}
	return comparePredicate(field, opText, val)
}

func inPredicate(field, value string) (predicate, error) {
	switch field {
	case "parents":
		return func(f *gdrive.File, s *Server) bool {
			for _, p := range f.Parents {
				if p == value || (value == "root" && p == s.RootID) {
					return true
				}
			}
			return false
		}, nil
	case "owners":
		return func(f *gdrive.File, _ *Server) bool {
			for _, o := range f.Owners {
				if o.EmailAddress == value || (value == "me" && o.Me) {
					return true
				}
			}
			return false
		}, nil
	case "writers", "readers":
		return func(f *gdrive.File, s *Server) bool {
			for _, p := range s.Permissions[f.ID] {
				if p.EmailAddress != value {
					continue
				}
				if field == "writers" && (p.Role == "writer" || p.Role == "owner") {
					return true
				}
				if field == "readers" {
					return true
				}
			}
			return false
		}, nil
	}
	return nil, fmt.Errorf("unsupported collection field %q", field)
}

func comparePredicate(field, op string, val token) (predicate, error) {
	switch field {
	case "name", "mimeType", "fullText":
		want := val.text
		return func(f *gdrive.File, s *Server) bool {
			return matchString(field, op, f, want, s)
		}, nil //nolint:gocritic // s is used through matchString
	case "trashed", "starred", "sharedWithMe":
		want := strings.EqualFold(val.text, "true")
		return func(f *gdrive.File, _ *Server) bool {
			var have bool
			switch field {
			case "trashed":
				have = f.Trashed
			case "starred":
				have = f.Starred
			case "sharedWithMe":
				have = f.SharedWithMeTime != ""
			}
			if op == "!=" {
				return have != want
			}
			return have == want
		}, nil
	case "modifiedTime", "createdTime", "viewedByMeTime":
		want, err := time.Parse(time.RFC3339, val.text)
		if err != nil {
			return nil, fmt.Errorf("bad timestamp %q: %w", val.text, err)
		}
		return func(f *gdrive.File, _ *Server) bool {
			var raw string
			switch field {
			case "modifiedTime":
				raw = f.ModifiedTime
			case "createdTime":
				raw = f.CreatedTime
			case "viewedByMeTime":
				raw = f.ViewedByMeTime
			}
			have, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return false
			}
			switch op {
			case ">":
				return have.After(want)
			case ">=":
				return !have.Before(want)
			case "<":
				return have.Before(want)
			case "<=":
				return !have.After(want)
			case "=":
				return have.Equal(want)
			case "!=":
				return !have.Equal(want)
			}
			return false
		}, nil
	}
	return nil, fmt.Errorf("unsupported field %q", field)
}

func matchString(field, op string, f *gdrive.File, want string, s *Server) bool {
	switch field {
	case "name":
		switch op {
		case "=":
			// Observed live (spike A): `name =` is a whole-name match but
			// it is NOT case-sensitive. Conferences, conferences and
			// CONFERENCES all return the same single item. Path
			// resolution depends on this: a folder holding both "Budget"
			// and "budget" makes `name = 'Budget'` return two, which is
			// how the ambiguity guard is reached.
			return strings.EqualFold(f.Name, want)
		case "!=":
			return !strings.EqualFold(f.Name, want)
		case "contains":
			return wordPrefixMatch(f.Name, want)
		}
	case "mimeType":
		switch op {
		case "=":
			return f.MimeType == want
		case "!=":
			return f.MimeType != want
		case "contains":
			return strings.Contains(f.MimeType, want)
		}
	case "fullText":
		if op != "contains" {
			return false
		}
		return fullTextMatch(f, want, s)
	}
	return false
}

// windowMatch reports whether want appears as a run of consecutive words
// in hay, with eq deciding what counts as a match for one word. Both of
// Drive's `contains` operators are this loop; only eq differs.
func windowMatch(hay, want []string, eq func(have, want string) bool) bool {
	if len(want) == 0 {
		return true
	}
	for i := 0; i+len(want) <= len(hay); i++ {
		ok := true
		for j, w := range want {
			if !eq(hay[i+j], w) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// wordPrefixMatch implements `name contains 'x'`. Observed live (spike
// A): every word of x must prefix SOME word of the name, and the order
// is irrelevant — "Decentralized Identity" and "Identity Decentralized"
// return the same set. "Bud" matches "Budget 2026"; "udget" matches
// nothing, because it is a prefix match and not a substring one.
func wordPrefixMatch(name, want string) bool {
	haystack := words(name)
	for _, w := range words(want) {
		found := false
		for _, have := range haystack {
			if strings.HasPrefix(have, w) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// fullTextMatch implements `fullText contains 'x'`: whole tokens, or a
// consecutive phrase when x arrives wrapped in double quotes.
func fullTextMatch(f *gdrive.File, want string, s *Server) bool {
	phrase := false
	if len(want) >= 2 && want[0] == '"' && want[len(want)-1] == '"' {
		want = want[1 : len(want)-1]
		phrase = true
	}
	haystack := words(f.Name + " " + f.Description + " " + s.Content[f.ID])
	wantWords := words(want)
	// A bare term matches one whole token anywhere; a quoted phrase must
	// appear as consecutive tokens.
	if !phrase && len(wantWords) > 1 {
		wantWords = wantWords[:1]
	}
	return windowMatch(haystack, wantWords, func(have, want string) bool { return have == want })
}

// words lower-cases and splits on everything that is not a letter or
// digit, which is close enough to Drive's tokenisation for a fake.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
}
