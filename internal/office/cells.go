package office

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// grid writes a sheet's rows as csv or tsv. A row ends at its last
// value, rather than being padded to the widest row, and a run of empty
// rows is written only when a row with values follows it, so a sheet
// whose formatting reaches row 1 000 000 does not read as a million
// blank lines.
type grid struct {
	out   *textOut
	delim byte
	// next is the row number a row written now would have, 1-based.
	next int
}

// room is how many more bytes of text fit under the cap.
func (g *grid) room() int { return g.out.max - g.out.b.Len() }

// row writes the row numbered num, after a blank line for every row
// skipped since the last one written.
func (g *grid) row(num int, cells []string) error {
	for len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	if len(cells) == 0 {
		return nil
	}
	if g.next == 0 {
		g.next = 1
	}
	if gap := num - g.next; gap > 0 {
		// Bounded by the text cap: each pass writes something or stops.
		for gap > 0 {
			n := min(gap, 4096)
			if err := g.out.write(strings.Repeat("\n", n)); err != nil {
				return err
			}
			gap -= n
		}
	}
	var b strings.Builder
	for i, c := range cells {
		if i > 0 {
			b.WriteByte(g.delim)
		}
		b.WriteString(field(c, g.delim))
	}
	b.WriteByte('\n')
	g.next = max(num, g.next) + 1
	return g.out.write(b.String())
}

// field quotes a value the way RFC 4180 does, when it holds the
// delimiter, a quote or a line break.
func field(s string, delim byte) string {
	if !strings.ContainsAny(s, "\"\r\n") && strings.IndexByte(s, delim) < 0 {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// cells is a row being read: its values by column, and the bytes of text
// they hold. An empty value past the last one is not held, so a row of
// sixteen thousand empty cells costs what an empty row does; and the
// text is counted as it grows, so a row too long for the cap is noticed
// while it is read rather than once it is whole.
type cells struct {
	vals []string
	size int
}

func (r *cells) reset() { r.vals, r.size = r.vals[:0], 0 }

// set puts a value at a 0-based column, filling the gap before it with
// empty cells. A column past the widest sheet is dropped.
func (r *cells) set(col int, v string) {
	if col < 0 || col >= maxColumns || (v == "" && col >= len(r.vals)) {
		return
	}
	if col < len(r.vals) {
		r.size += len(v) - len(r.vals[col])
		r.vals[col] = v
		return
	}
	for len(r.vals) < col {
		r.vals = append(r.vals, "")
	}
	r.vals = append(r.vals, v)
	r.size += len(v)
}

// cellRef reads the column and row out of a reference like "AB12",
// 0-based column and 1-based row. ok is false for anything else.
func cellRef(ref string) (col, row int, ok bool) {
	i := 0
	for i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z' {
		col = col*26 + int(ref[i]-'A'+1)
		if col > maxColumns {
			return 0, 0, false
		}
		i++
	}
	if i == 0 || i == len(ref) {
		return 0, 0, false
	}
	row, err := strconv.Atoi(ref[i:])
	if err != nil || row < 1 {
		return 0, 0, false
	}
	return col - 1, row, true
}

// numKind is what a number format makes of a number.
type numKind uint8

const (
	numPlain numKind = iota
	numDate
	numTime
	numDateTime
	// numElapsed counts hours past 24, as [h]:mm:ss does.
	numElapsed
)

// builtinKind is what one of the number formats ECMA-376 builds in
// shows: 14 to 17 dates, 18 to 21 times, 22 both, 45 to 47 minutes and
// seconds, of which 46 counts hours past a day. Every other built-in is
// a number, a percentage, a fraction or text.
func builtinKind(id int) numKind {
	switch {
	case id >= 14 && id <= 17:
		return numDate
	case id >= 18 && id <= 21, id == 45, id == 47:
		return numTime
	case id == 22:
		return numDateTime
	case id == 46:
		return numElapsed
	}
	return numPlain
}

// formatKind reads a custom number format code and says whether it
// shows a date, a time or both. It follows the test Apache POI uses:
// strip what is quoted, escaped or in brackets, and what is left has to
// be date and time letters with their separators and nothing that
// places a digit.
func formatKind(code string) numKind {
	var rest strings.Builder
	elapsed := false
	quoted := false
	// Only the first section counts: the others are for negative
	// numbers, zero and text, and a date has no sign.
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case quoted:
			quoted = c != '"'
		case c == '"':
			quoted = true
		case c == '\\', c == '_', c == '*':
			// Each stands before one character, which may take more
			// than one byte.
			_, n := utf8.DecodeRuneInString(code[i+1:])
			i += n
		case c == ';':
			i = len(code)
		case c == '[':
			end := strings.IndexByte(code[i:], ']')
			if end < 0 {
				return numPlain
			}
			inner := strings.ToLower(code[i+1 : i+end])
			if inner != "" && strings.Trim(inner, "hms") == "" {
				elapsed = true
				rest.WriteByte(inner[0])
			}
			i += end
		default:
			rest.WriteByte(c)
		}
	}
	s := strings.ToLower(rest.String())
	ampm := strings.Contains(s, "am/pm") || strings.Contains(s, "a/p")
	s = strings.NewReplacer("am/pm", "", "a/p", "").Replace(s)
	if strings.IndexFunc(s, func(r rune) bool { return !strings.ContainsRune(dateRunes, r) }) >= 0 {
		return numPlain
	}
	hasDate := strings.ContainsAny(s, "yd")
	hasTime := strings.ContainsAny(s, "hs") || ampm || elapsed
	switch {
	case elapsed:
		return numElapsed
	case hasDate && hasTime:
		return numDateTime
	case hasDate, strings.Contains(s, "m") && !hasTime:
		return numDate
	case hasTime:
		return numTime
	}
	return numPlain
}

// dateRunes are what a date or time format may consist of once its
// quoted text and bracketed codes are gone: the letters, their
// separators, and the zeros of fractional seconds.
const dateRunes = "ymdhs-/.,: 0年月日"

// Serial number ranges, from ECMA-376's date systems: the 1900 base
// runs from serial 1 to 2 958 465 (31 December 9999), the 1904 base
// from 0 to 2 957 003.
const (
	maxSerial1900 = 2958465
	maxSerial1904 = 2957003
)

// serialDate turns a stored serial number into the text a date format
// shows, as ISO 8601. ok is false when the serial is not a day the
// format can show: out of range, or 29 February 1900, which the 1900
// base keeps from Lotus 1-2-3 and which never existed.
func serialDate(v float64, kind numKind, date1904 bool) (string, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return "", false
	}
	if kind == numElapsed {
		secs := int64(math.Round(v * 86400))
		return strconv.FormatInt(secs/3600, 10) + ":" + two(secs/60%60) + ":" + two(secs%60), true
	}
	days := math.Floor(v)
	secs := math.Round((v - days) * 86400)
	if secs >= 86400 {
		days++
		secs -= 86400
	}
	clock := time.Duration(secs) * time.Second
	if kind == numTime {
		return time.Time{}.Add(clock).Format("15:04:05"), true
	}
	var day time.Time
	switch {
	case date1904 && days <= maxSerial1904:
		day = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(days))
	case date1904, days < 1, days == 60, days > maxSerial1900:
		return "", false
	case days < 60:
		day = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(days))
	default:
		// Past the day that never was, every serial is one day ahead.
		day = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(days))
	}
	if kind == numDate {
		return day.Format("2006-01-02"), true
	}
	return day.Add(clock).Format("2006-01-02 15:04:05"), true
}

func two(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}
