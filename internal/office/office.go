// Package office reads the text out of Word, Excel and PowerPoint files
// and their OpenDocument counterparts, with nothing but the standard
// library: archive/zip for the container and encoding/xml for the parts
// inside it.
//
// It reads through an io.ReaderAt, so a caller whose file sits elsewhere
// — in Drive, behind byte ranges — fetches only the zip's directory and
// the parts that carry text, never the images beside them. Everything it
// reads is bounded: the text it returns, the entries it accepts, how far
// one part may expand, how much XML it parses, how long one token of it
// may run and how deeply it nests. A row, a cell and every list a file
// keeps is counted as it grows, never once it is whole. A file past a
// limit is refused, or its text is cut short and the result says so.
// Nothing a file contains can make it hold more than a few tens of
// megabytes, and a canceled context stops a read between two tokens.
//
// It imports nothing from this repository, so it can be fuzzed and
// tested on its own.
package office

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// Kind is one of the formats this package reads.
type Kind int

// The formats, by the extension people know them by.
const (
	None Kind = iota
	Docx
	Xlsx
	Pptx
	Odt
	Ods
	Odp
)

// kinds maps the media types Drive gives these files onto a Kind.
var kinds = map[string]Kind{
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   Docx,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         Xlsx,
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": Pptx,
	"application/vnd.oasis.opendocument.text":                                   Odt,
	"application/vnd.oasis.opendocument.spreadsheet":                            Ods,
	"application/vnd.oasis.opendocument.presentation":                           Odp,
}

// KindOf is the format a media type names, or None.
func KindOf(mime string) Kind {
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = mime[:i]
	}
	return kinds[strings.ToLower(strings.TrimSpace(mime))]
}

// Spreadsheet reports whether the text of this kind is a sheet of cells.
func (k Kind) Spreadsheet() bool { return k == Xlsx || k == Ods }

// Presentation reports whether this kind is a deck of slides.
func (k Kind) Presentation() bool { return k == Pptx || k == Odp }

// Options bound one extraction. The zero value is the defaults.
type Options struct {
	// Delimiter separates a spreadsheet's cells: ',' for csv, '\t' for
	// tsv. Zero means ','.
	Delimiter byte
	// MaxText is the most text returned. Past it the text is cut short
	// and the result says so. Zero means DefaultMaxText.
	MaxText int
	// MaxRead is the most XML parsed, across every part, before the text
	// is cut short. Zero means DefaultMaxRead.
	MaxRead int64
	// MaxToken is the longest one token of XML may run: a run of text,
	// or a tag with its attributes. encoding/xml holds a token whole
	// before handing it over, so this is what one costs. Past it the
	// text is cut short. Zero means DefaultMaxToken.
	MaxToken int64
}

// The limits. Each one is what keeps a file that was made to hurt from
// costing more than an ordinary large one.
const (
	// DefaultMaxText matches the cap Google puts on an export, so an
	// Office file and a Google document are read under the same ceiling.
	DefaultMaxText = 10 << 20
	// DefaultMaxRead bounds the XML parsed. A sheet's markup runs to ten
	// times its text, so this is room for the text cap and then some.
	DefaultMaxRead = 256 << 20
	// DefaultMaxToken bounds one token. Excel holds at most 32 767
	// characters in a cell, and a megabyte is thirty times that.
	DefaultMaxToken = 1 << 20
	// MaxEntries is the most entries a zip may list. A Word document
	// has a few dozen; a large deck a few thousand.
	MaxEntries = 10000
	// MaxDirectory bounds the zip's table of contents, which archive/zip
	// reads whole before anything else happens.
	MaxDirectory = 8 << 20
	// MaxRatio is how far one part may expand: Apache POI refuses a part
	// that compresses better than 1%, and so does this.
	MaxRatio = 100
	// ratioGrace is the size below which a part's ratio is not checked:
	// a small part cannot cost much however well it compresses.
	ratioGrace = 1 << 20
	// MaxDepth is how deeply the XML may nest. A table in a table in a
	// text box is a dozen levels; this is twenty times that.
	MaxDepth = 256
	// MaxListed is the most relationships, styles, number formats,
	// sheets or slides one file may list. Word allows about four
	// thousand styles and Excel about two hundred and fifty formats.
	MaxListed = 1 << 16
)

func (o Options) withDefaults() Options {
	if o.Delimiter == 0 {
		o.Delimiter = ','
	}
	if o.MaxText <= 0 {
		o.MaxText = DefaultMaxText
	}
	if o.MaxRead <= 0 {
		o.MaxRead = DefaultMaxRead
	}
	if o.MaxToken <= 0 {
		o.MaxToken = DefaultMaxToken
	}
	return o
}

// Result is the text of one file.
type Result struct {
	Text string
	// Truncated says the text stops short of the end of the file,
	// because a limit was reached.
	Truncated bool
	// Sheet is the name of the sheet a spreadsheet's text is.
	Sheet string
	// Sheets is how many sheets the workbook has; zero when the format
	// does not say without reading every one of them.
	Sheets int
	// Slides is how many slides a presentation has.
	Slides int
}

// Error says why a file could not be read, in words a person can act
// on.
type Error struct {
	// Limit marks a file over one of this package's limits, as opposed
	// to one that is damaged or is not what its type says.
	Limit  bool
	Reason string
}

func (e *Error) Error() string { return e.Reason }

func unreadable(format string, args ...any) error {
	return &Error{Reason: fmt.Sprintf(format, args...)}
}

func overLimit(format string, args ...any) error {
	return &Error{Limit: true, Reason: fmt.Sprintf(format, args...)}
}

// Extract reads the text of one file of the given kind. It returns the
// context's error when the context ends before the read does.
func Extract(ctx context.Context, r io.ReaderAt, size int64, kind Kind, o Options) (*Result, error) {
	o = o.withDefaults()
	if kind == None {
		return nil, unreadable("it is not a format this reader knows")
	}
	if err := checkDirectory(r, size); err != nil {
		return nil, err
	}
	src := &source{r: r}
	zr, err := zip.NewReader(src, size)
	if err != nil {
		return nil, notAZip(r, err)
	}
	if len(zr.File) > MaxEntries {
		return nil, overLimit("it holds %d entries, and this server reads files of at most %d", len(zr.File), MaxEntries)
	}
	p := &pkg{ctx: ctx, o: o, src: src, files: make(map[string]*zip.File, len(zr.File))}
	for _, f := range zr.File {
		if _, seen := p.files[f.Name]; !seen {
			p.files[f.Name] = f
		}
	}
	switch kind {
	case Docx:
		return p.docx()
	case Xlsx:
		return p.xlsx()
	case Pptx:
		return p.pptx()
	case Odt:
		return p.odt()
	case Ods:
		return p.ods()
	case Odp:
		return p.odp()
	}
	return nil, unreadable("it is not a format this reader knows")
}

// directoryEnd is the fixed part of the zip's end-of-directory record.
const directoryEnd = 22

// checkDirectory refuses a directory too large to hold, before
// archive/zip reads all of it into memory. It reads the end record,
// which sits in the last 64 KiB of the file, the first thing archive/zip
// reads anyway; then it counts the headers the way archive/zip will
// read them, since archive/zip reads headers until one is not a header
// and checks only the low 16 bits of the count the end record gives.
func checkDirectory(r io.ReaderAt, size int64) error {
	if size < directoryEnd {
		return unreadable("it is %d bytes long, too short to be the zip archive every such file is", size)
	}
	tailLen := min(size, directoryEnd+65535)
	tail := make([]byte, tailLen)
	if _, err := r.ReadAt(tail, size-tailLen); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	at := findDirectoryEnd(tail)
	if at < 0 {
		return notAZip(r, zip.ErrFormat)
	}
	rec := tail[at:]
	endAt := size - tailLen + int64(at)
	records := uint64(binary.LittleEndian.Uint16(rec[10:]))
	dirSize := uint64(binary.LittleEndian.Uint32(rec[12:]))
	dirOffset := uint64(binary.LittleEndian.Uint32(rec[16:]))
	if records == 0xffff || dirSize == 0xffffffff || dirOffset == 0xffffffff {
		// A zip64 archive keeps the real numbers in a second record,
		// which a locator just before this one points at.
		var err error
		if endAt, records, dirSize, dirOffset, err = zip64Directory(r, size, endAt); err != nil {
			return err
		}
	}
	if records > MaxEntries {
		return overLimit("it lists %d entries, and this server reads files of at most %d", records, MaxEntries)
	}
	if dirSize > MaxDirectory {
		return overLimit("its table of contents is %d bytes, and this server reads at most %d", dirSize, MaxDirectory)
	}
	// archive/zip reads the directory from just before the end record,
	// or from the offset the record gives when a header is there, for a
	// file with something in front of it. Both are counted rather than
	// guessing which it will take.
	start := endAt - int64(dirSize)
	if err := countHeaders(r, size, start); err != nil {
		return err
	}
	if dirOffset < uint64(start) {
		return countHeaders(r, size, int64(dirOffset))
	}
	return nil
}

// headerLen is the fixed part of a central directory header.
const headerLen = 46

// countHeaders reads the directory header by header from start, as
// archive/zip will, until one is not a header, and refuses more entries
// or more bytes than this server reads, whatever the end record says. It
// reads at most MaxDirectory bytes and one header past them.
func countHeaders(r io.ReaderAt, size, start int64) error {
	if start < 0 || start >= size {
		return unreadable("its table of contents points outside the file")
	}
	br := bufio.NewReader(io.NewSectionReader(r, start, min(size-start, MaxDirectory+headerLen+1)))
	h := make([]byte, headerLen)
	var count, read int64
	for {
		if _, err := io.ReadFull(br, h); err != nil {
			return endOfHeaders(err)
		}
		if !bytes.Equal(h[:4], []byte("PK\x01\x02")) {
			return nil
		}
		count++
		read += headerLen
		if count > MaxEntries {
			return overLimit("it holds more than %d entries, and this server reads files of at most %d", MaxEntries, MaxEntries)
		}
		rest := int64(binary.LittleEndian.Uint16(h[28:])) + int64(binary.LittleEndian.Uint16(h[30:])) +
			int64(binary.LittleEndian.Uint16(h[32:]))
		if read += rest; read > MaxDirectory {
			return overLimit("its table of contents runs past %d bytes, and this server reads at most that", MaxDirectory)
		}
		if _, err := br.Discard(int(rest)); err != nil {
			return endOfHeaders(err)
		}
	}
}

// endOfHeaders is what running out of bytes means while counting
// headers: the end of the file ends the directory, as it does for
// archive/zip; a failure to fetch is that failure.
func endOfHeaders(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	return err
}

// findDirectoryEnd finds the end record in the tail of a file, the way
// archive/zip does: the last signature whose comment fits in what is
// left.
func findDirectoryEnd(b []byte) int {
	for i := len(b) - directoryEnd; i >= 0; i-- {
		if b[i] == 'P' && b[i+1] == 'K' && b[i+2] == 0x05 && b[i+3] == 0x06 {
			n := int(b[i+20]) | int(b[i+21])<<8
			if n+directoryEnd+i <= len(b) {
				return i
			}
		}
	}
	return -1
}

// zip64Directory reads where the zip64 end record is, and the entry
// count, directory size and directory offset it gives.
func zip64Directory(r io.ReaderAt, size, endAt int64) (at int64, records, dirSize, dirOffset uint64, err error) {
	const locatorLen, recordLen = 20, 56
	if endAt < locatorLen {
		return 0, 0, 0, 0, unreadable("its zip64 directory is missing")
	}
	loc := make([]byte, locatorLen)
	if _, err := r.ReadAt(loc, endAt-locatorLen); err != nil && !errors.Is(err, io.EOF) {
		return 0, 0, 0, 0, err
	}
	if !bytes.Equal(loc[:4], []byte("PK\x06\x07")) {
		return 0, 0, 0, 0, unreadable("its zip64 directory is missing")
	}
	where := binary.LittleEndian.Uint64(loc[8:])
	if size < recordLen || where > uint64(size-recordLen) {
		return 0, 0, 0, 0, unreadable("its zip64 directory points outside the file")
	}
	rec := make([]byte, recordLen)
	if _, err := r.ReadAt(rec, int64(where)); err != nil && !errors.Is(err, io.EOF) {
		return 0, 0, 0, 0, err
	}
	if !bytes.Equal(rec[:4], []byte("PK\x06\x06")) {
		return 0, 0, 0, 0, unreadable("its zip64 directory is damaged")
	}
	return int64(where), binary.LittleEndian.Uint64(rec[32:]), binary.LittleEndian.Uint64(rec[40:]),
		binary.LittleEndian.Uint64(rec[48:]), nil
}

// compoundFile is the signature of an OLE compound file. Office keeps a
// password-protected document in one, and its binary formats from before
// 2007 are one, so a file of an Office type that starts with it is
// either encrypted or older than its type says.
var compoundFile = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

func notAZip(r io.ReaderAt, err error) error {
	head := make([]byte, len(compoundFile))
	if n, _ := r.ReadAt(head, 0); n == len(head) && bytes.Equal(head, compoundFile) {
		return unreadable("it is password-protected, or saved in the binary format Office used before 2007")
	}
	var oe *Error
	if errors.As(err, &oe) {
		return oe
	}
	if errors.Is(err, zip.ErrFormat) || errors.Is(err, zip.ErrAlgorithm) || errors.Is(err, io.ErrUnexpectedEOF) {
		return unreadable("it is not a readable zip archive, which every such file is; it may be damaged")
	}
	return err
}

// pkg is one opened file: its entries by name and what has been read
// of them.
type pkg struct {
	// ctx is the read's context, kept here because every parse goes
	// through walk and nothing outlives the one call to Extract.
	ctx   context.Context
	o     Options
	src   *source
	files map[string]*zip.File
	// read is the XML parsed so far, across every part.
	read int64
}

// source counts the bytes archive/zip reads from the file. What a part
// pulls through it while it inflates is the part's real compressed
// size, which its declared one only bounds from above.
type source struct {
	r io.ReaderAt
	n int64
}

func (s *source) ReadAt(b []byte, off int64) (int, error) {
	n, err := s.r.ReadAt(b, off)
	s.n += int64(n)
	return n, err
}

// The errors that stop a parse and cut the text short.
var (
	// errReadCap stops a parse once MaxRead is spent.
	errReadCap = errors.New("the XML read limit was reached")
	// errTokenCap stops a parse at a token longer than MaxToken.
	errTokenCap = errors.New("one token of the XML runs past the limit")
)

// has reports whether the package holds a part.
func (p *pkg) has(name string) bool { return p.files[name] != nil }

// open opens one part for reading, refusing one that would expand past
// the ratio Apache POI calls a zip bomb: before reading, by the sizes
// the part declares, and while reading, by the bytes it has actually
// pulled from the file, since a declared compressed size can be a lie
// in the direction that hides the ratio.
func (p *pkg) open(name string) (io.ReadCloser, error) {
	f := p.files[name]
	if f == nil {
		return nil, unreadable("it has no %s", name)
	}
	if f.UncompressedSize64 > ratioGrace && f.UncompressedSize64 > MaxRatio*max(f.CompressedSize64, 1) {
		return nil, bomb(name, f.CompressedSize64, f.UncompressedSize64)
	}
	from := p.src.n
	rc, err := f.Open()
	if err != nil {
		return nil, unreadable("its part %s cannot be opened: %v", name, err)
	}
	return &counted{rc: rc, p: p, name: name, from: from}, nil
}

func bomb(name string, compressed, uncompressed uint64) error {
	return overLimit("its part %s expands from %d bytes to %d, more than %d times over, which is how "+
		"a file built to exhaust a reader looks", name, compressed, uncompressed, MaxRatio)
}

// counted charges what a part yields against the package's read limit,
// and holds the part to the ratio by what it has really cost.
type counted struct {
	rc   io.ReadCloser
	p    *pkg
	name string
	// from is where the file's byte count stood when the part opened,
	// and yielded is what the part has given so far.
	from    int64
	yielded int64
}

func (c *counted) Read(b []byte) (int, error) {
	if c.p.read >= c.p.o.MaxRead {
		return 0, errReadCap
	}
	if room := c.p.o.MaxRead - c.p.read; int64(len(b)) > room {
		b = b[:room]
	}
	n, err := c.rc.Read(b)
	c.p.read += int64(n)
	c.yielded += int64(n)
	if pulled := c.p.src.n - c.from; c.yielded > ratioGrace && c.yielded > MaxRatio*max(pulled, 1) {
		return n, bomb(c.name, uint64(pulled), uint64(c.yielded))
	}
	if errors.Is(err, zip.ErrFormat) || errors.Is(err, zip.ErrChecksum) {
		err = unreadable("a part of it is damaged: %v", err)
	}
	return n, err
}

func (c *counted) Close() error { return c.rc.Close() }

// resolve turns a relationship target into a part name: relative to the
// part that names it, or from the root when it starts with a slash.
func resolve(from, target string) string {
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(path.Clean(target), "/")
	}
	return strings.TrimPrefix(path.Clean(path.Join(path.Dir(from), target)), "/")
}

// rel is one entry of a part's relationships.
type rel struct {
	kind   string
	target string
}

// rels reads the relationships of one part. A part without any has none
// to read, which is not an error.
func (p *pkg) rels(part string) (map[string]rel, error) {
	name := path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")
	if part == "" {
		name = "_rels/.rels"
	}
	out := map[string]rel{}
	if !p.has(name) {
		return out, nil
	}
	err := p.walk(name, func(_ *walker, t xml.Token) error {
		if se, ok := t.(xml.StartElement); ok && se.Name.Local == "Relationship" {
			if attr(se, "TargetMode") == "External" {
				return nil
			}
			if len(out) >= MaxListed {
				return listed("relationships", name)
			}
			out[attr(se, "Id")] = rel{kind: attr(se, "Type"), target: resolve(part, attr(se, "Target"))}
		}
		return nil
	})
	return out, err
}

// byKind is the first relationship whose type ends in suffix, such as
// "/styles"; the transitional and strict namespaces share their ends.
func byKind(rels map[string]rel, suffix string) string {
	best := ""
	for _, r := range rels {
		// The smallest name wins, so a part that somehow names two is
		// read the same way every time rather than in map order.
		if strings.HasSuffix(r.kind, suffix) && (best == "" || r.target < best) {
			best = r.target
		}
	}
	return best
}

// mainPart is the part the package's own relationships call the office
// document, or the conventional name when they do not say.
func (p *pkg) mainPart(conventional string) (string, error) {
	rels, err := p.rels("")
	if err != nil {
		return "", err
	}
	if main := byKind(rels, "/officeDocument"); main != "" && p.has(main) {
		return main, nil
	}
	if p.has(conventional) {
		return conventional, nil
	}
	return "", unreadable("it has no %s, which is where such a file keeps its content", conventional)
}

// finish turns the end of a parse into a result: a stop at a limit cuts
// the text short, anything else is the error it was.
func finish(out *textOut, err error) (*Result, error) {
	switch {
	case err == nil:
		return &Result{Text: out.String()}, nil
	case stopped(err):
		return &Result{Text: out.String(), Truncated: true}, nil
	}
	return nil, err
}

// stopped reports whether a parse ended at one of the limits that cut
// the text short rather than refuse the file.
func stopped(err error) bool {
	return errors.Is(err, errFull) || errors.Is(err, errReadCap) || errors.Is(err, errTokenCap) ||
		errors.Is(err, ErrFetchLimit)
}

// notKind refuses a file whose content is another kind than its type
// says, such as a workbook given a Word document's type, rather than
// read it as empty. want is the kind the type names, and root is the
// element the part opened with.
func notKind(part, want string, root xml.Name) error {
	if got := kindOf(root); got != "" && got != want {
		return unreadable("its content is %s %s, not the %s its type says", article(got), got, want)
	}
	return unreadable("its content is not the %s its type says: its part %s holds %s", want, part, root.Local)
}

// kindOf names the kind of file a root element, or the element inside an
// OpenDocument body, belongs to.
func kindOf(n xml.Name) string {
	switch {
	case is(n, "document", nsW):
		return "Word document"
	case is(n, "workbook", nsS):
		return "Excel workbook"
	case is(n, "presentation", nsP):
		return "PowerPoint presentation"
	case n.Space == odfOffice:
		return odfKinds[n.Local]
	}
	return ""
}

func article(s string) string {
	if strings.ContainsRune("AEIOU", rune(s[0])) {
		return "an"
	}
	return "a"
}

// listed is the refusal of a file that lists more of something than
// MaxListed.
func listed(what, part string) error {
	return overLimit("its part %s lists more than %d %s", part, MaxListed, what)
}

// tolerable reports an error from a part the text can do without that
// says only that the part is damaged. A limit, a stop or a failure to
// fetch is never tolerable: each says the same about the parts to come.
func tolerable(err error) bool {
	var oe *Error
	return errors.As(err, &oe) && !oe.Limit
}

// aux is the error for a part the text depends on but is not: reaching a
// limit there refuses the file rather than cutting its text short,
// because the text that would follow cannot be read without it.
func aux(part string, err error) error {
	if errors.Is(err, errFull) || errors.Is(err, errReadCap) || errors.Is(err, errTokenCap) {
		return overLimit("reading its part %s needs more than this server reads", part)
	}
	return err
}
