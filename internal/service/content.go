package service

import (
	"context"
	"crypto/md5" //nolint:gosec // Drive publishes md5Checksum; verifying it means computing it
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/mmedum/google-drive-mcp/internal/gapi"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/mediatype"
	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/render"
)

// ReadFileInput selects the text to return.
type ReadFileInput struct {
	File string
	// Format picks between csv and tsv for a spreadsheet; other kinds
	// have one text form.
	Format string
	// Offset is where to start, as the continue_from of an earlier read.
	Offset int64
	// MaxChars bounds the window returned.
	MaxChars int
}

// ReadFile returns a file's text: a Google Doc as markdown, a Sheet as
// csv, Slides as plain text, and a text-like blob through a byte range,
// so the head of a large log costs one small request. Anything else says
// what to do instead.
func (s *Service) ReadFile(ctx context.Context, in ReadFileInput) (string, error) {
	res, plan, w, err := s.readText(ctx, in)
	if err != nil {
		return "", err
	}
	return s.renderText(ctx, res, plan, in, w), nil
}

// readText is the whole read: resolve the reference, refuse what has no
// text, decide how this kind becomes text, and fetch the window asked
// for. Both callers are the same read with a different wrapping —
// read_file lays the window out under a header, a resource hands back
// the text alone — and the head of it is shared for the same reason the
// tail is: a rule added to one of them has to reach the other.
func (s *Service) readText(ctx context.Context, in ReadFileInput,
) (*Resolved, readPlan, textWindow, error) {
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return nil, readPlan{}, textWindow{}, err
	}
	f := res.File
	if f.IsFolder() {
		return nil, readPlan{}, textWindow{}, Errorf(ClassInvalid,
			"%s is a folder. list_folder shows what is inside it, and %s is the same listing as a resource.",
			f.Name, ResourceURI(f.ID, "children"))
	}
	if in.Offset < 0 {
		return nil, readPlan{}, textWindow{}, Errorf(ClassInvalid, "offset cannot be negative")
	}
	plan, err := s.readPlan(f, in.Format)
	if err != nil {
		return nil, readPlan{}, textWindow{}, err
	}
	w, err := s.textWindow(ctx, res, plan, in, render.Budget(in.MaxChars))
	if err != nil {
		return nil, readPlan{}, textWindow{}, err
	}
	return res, plan, w, nil
}

// textWindow is one window of a file's text and what a header needs to
// describe it. It exists because a tool result and a resource want the
// same bytes and different wrappings: read_file lays the window out
// under a header saying which part of the file it is, and a resource
// hands back the text alone, because a resource IS the content and a
// client may feed it to something that parses the media type.
type textWindow struct {
	text  string
	used  int64
	total int64
	more  bool
}

// textWindow reads the window a caller asked for, from an export or from
// the file's own bytes.
func (s *Service) textWindow(ctx context.Context, res *Resolved, plan readPlan, in ReadFileInput, budget int,
) (textWindow, error) {
	f := res.File
	if plan.exportMime != "" {
		return s.exportWindow(ctx, res, plan, in, budget)
	}
	// A blob's size does not bound this call: only the window shown is
	// fetched, so the head of a 200 MB log is one small request, and
	// refusing the file for its size would have contradicted the advice
	// the refusal itself gave.
	if size, known := f.SizeBytes(); known && in.Offset >= size {
		// Drive answers a range that starts past the end with a 416,
		// which says nothing useful. The file's own size already does.
		return textWindow{total: size}, nil
	}
	content, err := s.api.Download(ctx, f.ID, gapi.DownloadOptions{
		Offset: in.Offset, Length: int64(budget) + utf8.UTFMax,
	})
	if err != nil {
		return textWindow{}, s.contentError(err, f, "reading")
	}
	defer func() { _ = content.Body.Close() }()

	window, used, more, err := readWindow(content.Body, budget)
	if err != nil {
		return textWindow{}, wrap(err, "reading "+f.Name)
	}
	return textWindow{text: window, used: used, total: totalOf(content, f), more: more}, nil
}

// readExport returns a window of a Google-native document. Drive takes
// no byte range on an export, so the whole document arrives whatever
// window was asked for; it is kept for ExportTTL, and the windows after
// the first cost nothing. The key carries the revision and the
// modification time, so a document that changed under a paging model is
// exported again rather than served from a stale copy.
func (s *Service) exportWindow(ctx context.Context, res *Resolved, plan readPlan, in ReadFileInput, budget int,
) (textWindow, error) {
	f := res.File
	key := f.ID + "\x00" + f.HeadRevisionID + "\x00" + f.ModifiedTime + "\x00" + plan.exportMime
	text, ok := s.exportedText(key)
	if !ok {
		content, err := s.api.Export(ctx, f.ID, plan.exportMime)
		if err != nil {
			return textWindow{}, s.contentError(err, f, "reading")
		}
		defer func() { _ = content.Body.Close() }()
		// The cap is Google's own: an export larger than this does not
		// arrive, so reading to the end is bounded whatever the file is.
		raw, err := io.ReadAll(io.LimitReader(content.Body, MaxExport))
		if err != nil {
			return textWindow{}, wrap(err, "reading "+f.Name)
		}
		text = string(raw)
		if plan.stripDataURIs {
			// Stripping once, before the cache, keeps every window
			// consistent and stops a data URI being cut in half by a
			// window boundary.
			text = render.StripDataURIs(text)
		}
		s.keepExport(key, text)
	}

	total := int64(len(text))
	if in.Offset >= total {
		return textWindow{total: total}, nil
	}
	// Sliced, not copied. The bytes are already a string in memory, and
	// readWindow's job is to bound an io.Reader: putting one around this
	// allocates the whole budget and memcpys into it, which on a resource
	// read is 400 KB whatever the document's size — for a 200-byte Doc
	// as much as for a long one.
	window, used := stringWindow(text[in.Offset:], budget)
	return textWindow{text: window, used: used, total: total,
		more: in.Offset+used < total}, nil
}

// stringWindow returns at most budget bytes from the front of text,
// ending on a rune boundary, and how many bytes it took. A slice of a
// string shares the backing array, so nothing is copied.
func stringWindow(text string, budget int) (string, int64) {
	if len(text) <= budget {
		return text, int64(len(text))
	}
	cut := budget
	// Back up to a boundary, so a window never ends in half a character.
	// utf8.UTFMax bounds how far that can be.
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], int64(cut)
}

// MaxExport is Google's own ceiling on files.export.
const MaxExport = 10 << 20

func (s *Service) exportedText(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.export.key != key || s.now().Sub(s.export.at) > s.opts.ExportTTL {
		return "", false
	}
	return s.export.text, true
}

func (s *Service) keepExport(key, text string) {
	s.mu.Lock()
	s.export = exported{key: key, text: text, at: s.now()}
	s.mu.Unlock()
}

// renderText lays out one window under the header that says which part
// of the file it is.
func (s *Service) renderText(ctx context.Context, res *Resolved, plan readPlan, in ReadFileInput,
	w textWindow,
) string {
	return render.FileText(s.Model(ctx, res), render.FileTextOptions{
		Now:              s.now(),
		FollowedShortcut: res.FollowedShortcut,
		Format:           plan.formatName,
		Note:             plan.note,
		Offset:           in.Offset,
		Bytes:            w.used,
		Total:            w.total,
		More:             w.more,
		Text:             w.text,
	})
}

// readPlan is how one kind of file becomes text.
type readPlan struct {
	// exportMime is the format a Google-native document is exported to;
	// empty means the file's own bytes are read.
	exportMime string
	// formatName is what the header calls the text it is showing.
	formatName string
	// note is an extra line about what was and was not returned.
	note string
	// stripDataURIs removes inlined images from a markdown export.
	stripDataURIs bool
}

// readPlan decides how to turn one file into text, and refuses the kinds
// that have no text with the two ways forward rather than an empty
// result.
func (s *Service) readPlan(f *gdrive.File, format string) (readPlan, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	// The export media type comes from internal/mediatype, so the format
	// a read takes and the format a download defaults to are two fields
	// of one entry rather than two tables that agree by hand. What stays
	// here is what the registry cannot say: which alternatives a caller
	// may ask for, and what the result has to warn about.
	switch f.MimeType {
	case gdrive.MimeDocument:
		if format != "" && format != "md" && format != "markdown" {
			return readPlan{}, Errorf(ClassInvalid, "read_file returns a Google Doc as markdown; "+
				"download_file writes it as %s.", format)
		}
		return readPlan{exportMime: mediatype.ReadAs(f.MimeType), formatName: "markdown",
			stripDataURIs: true}, nil
	case gdrive.MimeSheet:
		mime, name := mediatype.ReadAs(f.MimeType), "csv"
		switch format {
		case "", "csv":
		case "tsv":
			mime, name = mediatype.ExportMime("tsv"), "tsv"
		default:
			return readPlan{}, Errorf(ClassInvalid, "a spreadsheet reads as csv or tsv, not %q. "+
				"download_file writes it as xlsx or pdf.", format)
		}
		return readPlan{exportMime: mime, formatName: name, note: "this is the FIRST SHEET only. " +
			"Another sheet, or one range of cells, is a Sheets API read, which this server does not offer; " +
			"download_file writes the whole workbook as xlsx."}, nil
	case gdrive.MimeSlides:
		return readPlan{exportMime: mediatype.ReadAs(f.MimeType), formatName: "plain text",
			note: "the text of the slides, without their layout"}, nil
	case gdrive.MimeScript:
		return readPlan{exportMime: mediatype.ReadAs(f.MimeType), formatName: "JSON"}, nil
	case gdrive.MimeShortcut:
		return readPlan{}, Errorf(ClassInvalid, "%s is a shortcut whose target could not be read", f.Name)
	}
	if f.IsWorkspaceDoc() {
		return readPlan{}, Errorf(ClassUnsupported, "%s is %s, which has no text form. "+
			"download_file writes it to disk.", f.Name, model.KindWithArticle(f))
	}
	if !model.IsTextLike(f.MimeType) {
		return readPlan{}, Errorf(ClassUnsupported, "%s is %s, which is not text. Two ways forward: "+
			"download_file writes it to disk, or copy_file with convert_to: doc asks Google to import it "+
			"(which reads the text out of a PDF or an image) and then read_file works on the copy.",
			f.Name, model.KindWithArticle(f))
	}
	return readPlan{formatName: "text"}, nil
}

// readWindow reads at most budget bytes and returns whole characters
// only, so a window never ends in half a rune. It also reports whether
// there was more to come, which is what makes continuation honest.
func readWindow(r io.Reader, budget int) (text string, used int64, more bool, err error) {
	// One extra rune's worth so a cut in the middle of a character can be
	// detected and given back rather than shown as a replacement.
	buf := make([]byte, budget+utf8.UTFMax)
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", 0, false, err
	}
	data := buf[:n]
	if n > budget {
		more = true
		data = data[:budget]
	}
	// A window cut at a byte boundary can end in half a character. Those
	// bytes belong to the next window, not to a replacement character in
	// this one. utf8.DecodeLastRune reports a genuine U+FFFD as three
	// bytes and a broken tail as one, which is exactly the difference.
	for len(data) > 0 {
		r, size := utf8.DecodeLastRune(data)
		if r != utf8.RuneError || size != 1 {
			break
		}
		data = data[:len(data)-1]
		more = true
	}
	return string(data), int64(len(data)), more, nil
}

// totalOf is the file's whole size in the unit the window is counted in.
// A partial response names it; otherwise the metadata does, and an
// export knows only what it read.
func totalOf(c *gapi.Content, f *gdrive.File) int64 {
	if c.TotalLength >= 0 {
		return c.TotalLength
	}
	if n := sizeOf(f); n > 0 {
		return n
	}
	return -1
}

func sizeOf(f *gdrive.File) int64 {
	n, _ := f.SizeBytes()
	return n
}

// DownloadFileInput selects what to write to the local directory.
type DownloadFileInput struct {
	File string
	// Format is the export format for a Google-native document.
	Format string
	// Revision names an old version instead of the current one.
	Revision string
	// AcknowledgeAbuse downloads a file Google has flagged.
	AcknowledgeAbuse bool
}

// DownloadFile writes a file to GDRIVE_LOCAL_DIR, streaming it in chunks
// and checking Drive's own md5 where there is one.
func (s *Service) DownloadFile(ctx context.Context, in DownloadFileInput) (string, error) {
	if _, err := s.localDir(); err != nil {
		return "", err
	}
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return "", err
	}
	f := res.File
	if f.IsFolder() {
		return "", Errorf(ClassUnsupported, "%s is a folder, and Drive has no download for one. "+
			"list_folder shows what is inside it, and each file downloads on its own.", f.Name)
	}

	content, ext, note, err := s.openDownload(ctx, f, in)
	if err != nil {
		return "", err
	}
	defer func() { _ = content.Body.Close() }()

	path, err := s.localTarget(f.Name, f.ID, ext)
	if err != nil {
		return "", err
	}
	// Hashing a gigabyte is pointless when there is nothing to compare
	// the digest against, which is the case for every export and every
	// older revision.
	compare := f.MD5Checksum != "" && in.Revision == ""
	written, sum, err := writeStream(path, content.Body, compare)
	if err != nil {
		return "", err
	}

	m := s.Model(ctx, res)
	out := render.Download(m, render.DownloadOptions{
		Now:      s.now(),
		Path:     path,
		Bytes:    written,
		Format:   ext,
		Revision: in.Revision,
		Note:     note,
		Checksum: checksumVerdict(f, in.Revision, sum),
	})
	return out, nil
}

// openDownload picks the way this file's bytes are reached: an export
// for a Google-native document, an old revision's own link or bytes, and
// alt=media for everything else.
func (s *Service) openDownload(ctx context.Context, f *gdrive.File, in DownloadFileInput) (*gapi.Content, string, string, error) {
	// A kind whose bytes come only through the long-running download is
	// checked before the Workspace-document branch, because such a kind
	// IS a Workspace document and that branch would try to export it —
	// which Drive refuses. Which kinds those are is the registry's to
	// say, not this function's.
	if format := mediatype.OperationAs(f.MimeType); format != "" {
		return s.downloadOperation(ctx, f, in, format)
	}
	if f.IsWorkspaceDoc() {
		mime, ext, err := s.exportFormat(f, in.Format)
		if err != nil {
			return nil, "", "", err
		}
		if in.Revision != "" {
			c, err := s.exportRevision(ctx, f, in.Revision, mime)
			return c, ext, "an old revision, exported", err
		}
		c, err := s.api.Export(ctx, f.ID, mime)
		if err != nil {
			return nil, "", "", s.contentError(err, f, "exporting")
		}
		return c, ext, "", nil
	}
	if in.Format != "" {
		return nil, "", "", Errorf(ClassInvalid, "%s is %s, and format only applies to a Google Doc, "+
			"Sheet, Slides deck or Drawing, which Drive converts as it exports them.", f.Name, model.KindWithArticle(f))
	}
	if n := sizeOf(f); n > 0 && s.opts.MaxDownload > 0 && n > s.opts.MaxDownload {
		return nil, "", "", Errorf(ClassUnsupported, "%s is %s, and this server was started with a limit of %s "+
			"(GDRIVE_MAX_DOWNLOAD).", f.Name, model.HumanSize(n), model.HumanSize(s.opts.MaxDownload))
	}
	c, err := s.api.Download(ctx, f.ID, gapi.DownloadOptions{
		RevisionID: in.Revision, AcknowledgeAbuse: in.AcknowledgeAbuse,
	})
	if err != nil {
		return nil, "", "", s.contentError(err, f, "downloading")
	}
	return c, extensionFor(f), "", nil
}

// downloadOperation fetches a kind whose bytes come only through the
// long-running download — a Google Vid today, and whatever Google gives
// the same treatment next.
//
// The bytes are never on the file endpoint and an export is refused, so
// this is the whole of it: start the operation, poll until Drive has
// rendered the file, then fetch the address it hands back. Rendering
// takes time — the guide says a new operation is usually pending "for
// Vids files" especially — so a caller who waits too long is told the
// operation is still running rather than told it failed.
func (s *Service) downloadOperation(ctx context.Context, f *gdrive.File, in DownloadFileInput,
	format string) (*gapi.Content, string, string, error) {
	if in.Format != "" {
		return nil, "", "", Errorf(ClassInvalid,
			"%s is %s, and Drive renders one as %s and nothing else, so format does not apply",
			f.Name, model.KindWithArticle(f), strings.ToUpper(format))
	}
	op, err := s.resumeOrStartDownload(ctx, f, in.Revision)
	if err != nil {
		return nil, "", "", s.contentError(err, f, "starting the download of")
	}
	ready, err := s.api.AwaitDownload(ctx, op)
	if err != nil {
		var pending *gapi.OperationPendingError
		if errors.As(err, &pending) {
			// The name is kept, so the next call continues this render
			// rather than asking Drive to do the work again. Without that
			// the sentence below would be a promise the code does not keep.
			s.rememberDownload(f.ID, pending.Name)
			return nil, "", "", Errorf(ClassPending,
				"Drive is still rendering %s as %s. That can take a while for a video; "+
					"call download_file again in a minute or two and it will pick up this same render "+
					"rather than starting another.", f.Name, strings.ToUpper(format))
		}
		return nil, "", "", s.contentError(err, f, "downloading")
	}
	s.forgetDownload(f.ID)
	c, err := s.api.DownloadURL(ctx, ready.DownloadURI)
	if err != nil {
		return nil, "", "", s.contentError(err, f, "fetching the rendered video of")
	}
	return c, format, "rendered by Drive as " + strings.ToUpper(format) +
		", which is the only form this kind comes in", nil
}

// resumeOrStartDownload continues a render this process already asked
// for, or begins one.
//
// Resuming matters more than it looks: an operation's name comes back
// only from the call that starts it, and Google documents no way to list
// operations and find it again. A name dropped is a render dropped, and
// the caller pays for the whole thing twice.
func (s *Service) resumeOrStartDownload(ctx context.Context, f *gdrive.File, revision string) (*gdrive.Operation, error) {
	s.mu.Lock()
	name, ok := cacheGet(s.downloads, f.ID, s.now(), s.opts.DownloadOpTTL)
	s.mu.Unlock()
	if ok {
		op, err := s.api.GetOperation(ctx, name)
		if err == nil {
			return op, nil
		}
		// The operation expired or Drive forgot it. That is not a failure
		// worth reporting: starting another is exactly what the caller
		// wanted.
		s.log.DebugContext(ctx, "a kept download operation could not be read; starting another",
			"class", gapi.Class(err))
		s.forgetDownload(f.ID)
	}
	return s.api.StartDownload(ctx, f.ID, "", revision)
}

// rememberDownload keeps an unfinished render's name.
func (s *Service) rememberDownload(fileID, name string) {
	if name == "" {
		return
	}
	s.mu.Lock()
	s.downloads[fileID] = cached[string]{value: name, at: s.now()}
	s.mu.Unlock()
}

// forgetDownload drops a render that finished or expired.
func (s *Service) forgetDownload(fileID string) {
	s.mu.Lock()
	delete(s.downloads, fileID)
	s.mu.Unlock()
}

// exportRevision reaches an old version of a Google-native document.
// Its bytes are not on the file endpoint: the revision hands out export
// links, and those are what carry the old content.
func (s *Service) exportRevision(ctx context.Context, f *gdrive.File, revisionID, mime string) (*gapi.Content, error) {
	rev, err := s.api.GetRevision(ctx, f.ID, revisionID)
	if err != nil {
		return nil, wrap(err, fmt.Sprintf("reading revision %s of %s", revisionID, f.Name))
	}
	link := rev.ExportLinks[mime]
	if link == "" {
		have := make([]string, 0, len(rev.ExportLinks))
		for m := range rev.ExportLinks {
			if name := mediatype.ExportName(m); name != "" {
				have = append(have, name)
			}
		}
		return nil, Errorf(ClassUnsupported, "that revision of %s cannot be exported as %s%s",
			f.Name, mediatype.ExportName(mime), listOrNothing(have, ". It offers: ", ""))
	}
	c, err := s.api.DownloadURL(ctx, link)
	if err != nil {
		return nil, s.contentError(err, f, "downloading an old revision of")
	}
	return c, nil
}

// exportFormat maps the short format name a caller asked for onto the
// MIME type Drive exports, defaulting to the Office format for the kind
// because that is the form the file came from and goes back to.
func (s *Service) exportFormat(f *gdrive.File, name string) (mime, ext string, err error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = mediatype.DownloadAs(f.MimeType)
	}
	if name == "" {
		return "", "", Errorf(ClassUnsupported, "%s is %s, which Drive does not export. get_file lists "+
			"the formats a file offers.", f.Name, model.KindWithArticle(f))
	}
	mime = mediatype.ExportMime(name)
	if mime == "" {
		return "", "", Errorf(ClassInvalid, "%q is not an export format. Drive offers: %s",
			name, strings.Join(mediatype.ExportNames(), ", "))
	}
	if _, ok := f.ExportLinks[mime]; !ok && len(f.ExportLinks) > 0 {
		return "", "", Errorf(ClassUnsupported, "%s cannot be exported as %s. It offers: %s",
			f.Name, name, strings.Join(model.ExportFormats(f), ", "))
	}
	return mime, name, nil
}

// downloadBuffer is the block a download is copied in. io.Copy's own
// 32 KiB default costs eight times as many read, write and stall-timer
// operations on a large file, and a transfer is exactly where that adds
// up.
const downloadBuffer = 256 << 10

// writeStream copies a download to disk in chunks, computing Drive's own
// checksum as it goes when there is one to compare against. The file is
// created exclusively, so a download never lands on top of something
// already there.
func writeStream(path string, body io.Reader, checksum bool) (int64, string, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, "", Errorf(ClassInvalid, "cannot write %s: %s", path, err)
	}
	var out io.Writer = file
	sum := md5.New() //nolint:gosec // matching Drive's md5Checksum
	if checksum {
		out = io.MultiWriter(file, sum)
	}
	written, copyErr := io.CopyBuffer(out, body, make([]byte, downloadBuffer))
	closeErr := file.Close()
	if copyErr != nil {
		// A half-written file is worse than none: it looks like a
		// download that worked.
		_ = os.Remove(path)
		return 0, "", wrap(copyErr, "writing "+path)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return 0, "", Errorf(ClassInvalid, "cannot finish writing %s: %s", path, closeErr)
	}
	return written, hex.EncodeToString(sum.Sum(nil)), nil
}

// checksumVerdict says whether the bytes on disk are the bytes Drive
// holds — as facts, not as a sentence: internal/render decides how to
// put it. An export and an older revision have no checksum to compare
// against, and "verified" must never be said about one of those.
func checksumVerdict(f *gdrive.File, revision, got string) model.Checksum {
	switch {
	case f.MD5Checksum == "":
		return model.Checksum{State: model.ChecksumNotPublished}
	case revision != "":
		return model.Checksum{State: model.ChecksumNotComparable,
			Why: "the file's checksum is the current version's, not this revision's"}
	case f.MD5Checksum == got:
		return model.Checksum{State: model.ChecksumMatch, Expected: f.MD5Checksum, Actual: got}
	}
	return model.Checksum{State: model.ChecksumMismatch, Expected: f.MD5Checksum, Actual: got}
}

// extensionFor is the file extension a downloaded blob keeps: the one
// Drive recorded, then the one its name carries, then one derived from
// its type.
func extensionFor(f *gdrive.File) string {
	if f.FileExtension != "" {
		return f.FileExtension
	}
	if ext := strings.TrimPrefix(filepath.Ext(f.Name), "."); ext != "" {
		return ext
	}
	return mediatype.ExportName(f.MimeType)
}

// contentError explains the refusals a transfer meets that a metadata
// read never does.
func (s *Service) contentError(err error, f *gdrive.File, doing string) error {
	if gapi.IsAbuse(err) {
		return &Error{Class: ClassBlocked, Message: fmt.Sprintf(
			"Google has flagged %s as malware or spam and will not hand it over. "+
				"If you know what it is, call download_file again with acknowledge_abuse: true.", f.Name), Err: err}
	}
	return wrap(err, doing+" "+f.Name)
}

// listOrNothing renders a list with a prefix, or nothing when it is
// empty, so a sentence does not trail off into a colon.
func listOrNothing(items []string, prefix, suffix string) string {
	if len(items) == 0 {
		return ""
	}
	return prefix + strings.Join(items, ", ") + suffix
}
