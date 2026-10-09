// Package office reads the text out of Word, Excel and PowerPoint files
// and their OpenDocument counterparts, with nothing but the standard
// library: archive/zip for the container and encoding/xml for the parts
// inside it.
//
// It reads through an io.ReaderAt, so a caller whose file sits elsewhere
// — in Drive, behind byte ranges — fetches only the zip's directory and
// the parts that carry text, never the images beside them. Everything it
// reads is bounded: the text it returns, the entries it accepts, how far
// one part may expand, how much XML it parses and how deeply that XML
// nests. A file past a limit is refused, or its text is cut short and
// the result says so. Nothing a file contains can make it hold more than
// a few tens of megabytes.
//
// It imports nothing from this repository, so it can be fuzzed and
// tested on its own.
package office

import (
	"archive/zip"
	"bytes"
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

// Extract reads the text of one file of the given kind.
func Extract(r io.ReaderAt, size int64, kind Kind, o Options) (*Result, error) {
	o = o.withDefaults()
	if kind == None {
		return nil, unreadable("it is not a format this reader knows")
	}
	if err := checkDirectory(r, size); err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, notAZip(r, err)
	}
	if len(zr.File) > MaxEntries {
		return nil, overLimit("it holds %d entries, and this server reads files of at most %d", len(zr.File), MaxEntries)
	}
	p := &pkg{o: o, files: make(map[string]*zip.File, len(zr.File))}
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

// checkDirectory reads the zip's end record and refuses a directory too
// large to hold, before archive/zip reads all of it into memory. The
// record sits in the last 64 KiB of the file, which is the first thing
// archive/zip reads anyway, so this costs no extra fetch.
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
	records := uint64(binary.LittleEndian.Uint16(rec[10:]))
	dirSize := uint64(binary.LittleEndian.Uint32(rec[12:]))
	dirOffset := uint64(binary.LittleEndian.Uint32(rec[16:]))
	if records == 0xffff || dirSize == 0xffffffff || dirOffset == 0xffffffff {
		// A zip64 archive keeps the real numbers in a second record,
		// which a locator just before this one points at.
		var err error
		if records, dirSize, err = zip64Directory(r, size, size-tailLen+int64(at)); err != nil {
			return err
		}
	}
	if records > MaxEntries {
		return overLimit("it lists %d entries, and this server reads files of at most %d", records, MaxEntries)
	}
	if dirSize > MaxDirectory {
		return overLimit("its table of contents is %d bytes, and this server reads at most %d", dirSize, MaxDirectory)
	}
	return nil
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

// zip64Directory reads the entry count and directory size out of a
// zip64 end record.
func zip64Directory(r io.ReaderAt, size, endAt int64) (records, dirSize uint64, err error) {
	const locatorLen, recordLen = 20, 56
	if endAt < locatorLen {
		return 0, 0, unreadable("its zip64 directory is missing")
	}
	loc := make([]byte, locatorLen)
	if _, err := r.ReadAt(loc, endAt-locatorLen); err != nil && !errors.Is(err, io.EOF) {
		return 0, 0, err
	}
	if !bytes.Equal(loc[:4], []byte("PK\x06\x07")) {
		return 0, 0, unreadable("its zip64 directory is missing")
	}
	at := binary.LittleEndian.Uint64(loc[8:])
	if at > uint64(size-recordLen) {
		return 0, 0, unreadable("its zip64 directory points outside the file")
	}
	rec := make([]byte, recordLen)
	if _, err := r.ReadAt(rec, int64(at)); err != nil && !errors.Is(err, io.EOF) {
		return 0, 0, err
	}
	if !bytes.Equal(rec[:4], []byte("PK\x06\x06")) {
		return 0, 0, unreadable("its zip64 directory is damaged")
	}
	return binary.LittleEndian.Uint64(rec[32:]), binary.LittleEndian.Uint64(rec[40:]), nil
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
	o     Options
	files map[string]*zip.File
	// read is the XML parsed so far, across every part.
	read int64
}

// errReadCap stops a parse once MaxRead is spent.
var errReadCap = errors.New("the XML read limit was reached")

// has reports whether the package holds a part.
func (p *pkg) has(name string) bool { return p.files[name] != nil }

// open opens one part for reading, refusing one that would expand past
// the ratio Apache POI calls a zip bomb.
func (p *pkg) open(name string) (io.ReadCloser, error) {
	f := p.files[name]
	if f == nil {
		return nil, unreadable("it has no %s", name)
	}
	// archive/zip stops a part at its declared uncompressed size and
	// reads no more than its declared compressed size, so the ratio of
	// the two is the ratio a reader can actually meet.
	if f.UncompressedSize64 > ratioGrace && f.UncompressedSize64 > MaxRatio*max(f.CompressedSize64, 1) {
		return nil, overLimit("its part %s expands from %d bytes to %d, more than %d times over, which is how "+
			"a file built to exhaust a reader looks", name, f.CompressedSize64, f.UncompressedSize64, MaxRatio)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, unreadable("its part %s cannot be opened: %v", name, err)
	}
	return &counted{rc: rc, p: p}, nil
}

// counted charges what a part yields against the package's read limit.
type counted struct {
	rc io.ReadCloser
	p  *pkg
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
	return errors.Is(err, errFull) || errors.Is(err, errReadCap) || errors.Is(err, ErrFetchLimit)
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
	if errors.Is(err, errFull) || errors.Is(err, errReadCap) {
		return overLimit("reading its part %s needs more than this server reads", part)
	}
	return err
}
