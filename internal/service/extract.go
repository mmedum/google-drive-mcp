package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/v2/internal/gapi"
	"github.com/mmedum/google-drive-mcp/v2/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v2/internal/model"
	"github.com/mmedum/google-drive-mcp/v2/internal/office"
	"github.com/mmedum/google-drive-mcp/v2/internal/render"
)

// ExtractTextInput selects a PDF or an image whose text to read.
type ExtractTextInput struct {
	File string
	// OCRLanguage hints the language of the text, as an ISO 639-1 code.
	OCRLanguage string
	Offset      int64
	MaxChars    int
	// KeepCopy leaves the Google Doc the import made, rather than
	// deleting it.
	KeepCopy bool
}

// The marker on a temporary copy. Drive keeps an app property private to
// the OAuth client that wrote it, so only this server's searches see it.
const (
	ExtractMarkerKey    = "google_drive_mcp"
	extractMarkerPrefix = "extract_text_"
)

// Google's limits on reading text out of a file.
const (
	// ocrAdvised is the size the Help Center asks for: "The file should
	// be 2 MB or smaller." It is advice, so past it the read goes ahead
	// with a warning.
	ocrAdvised = 2 << 20
	// ocrLargest is the most Google converts into a Doc: "If you convert
	// a text document to Google Docs format, it can be up to 50 MB". The
	// Help Center says it of text documents; for a PDF or an image it is
	// recorded as unverified (§18).
	ocrLargest = 50 << 20
	// detachedTimeout bounds the work that runs on when the call itself
	// is canceled: waiting for Google's answer to the copy, and deleting
	// the copy. A copy left behind is the one lasting effect this tool
	// can have.
	detachedTimeout = 2 * time.Minute
)

// ocrTypes are the types Google's import guide lists as becoming a
// Google Doc with their text read: PDF, JPEG, PNG, GIF and BMP. The
// account's own import formats decide when Drive hands them out; this
// list stands in when it does not.
var ocrTypes = map[string]bool{
	"application/pdf": true, "image/jpeg": true, "image/png": true, "image/gif": true, "image/bmp": true,
}

// ExtractText reads the text of a PDF or an image with Google's OCR,
// which Drive offers only as part of converting a file into a Google
// Doc. So it copies the file as a Doc into the root of My Drive, exports
// that as text, and deletes the copy for good, unless asked to keep it.
// The whole text is kept like an export, so paging through it makes no
// second copy.
func (s *Service) ExtractText(ctx context.Context, in ExtractTextInput) (string, error) {
	if err := s.writable("extract_text"); err != nil {
		return "", err
	}
	if in.Offset < 0 {
		return "", Errorf(ClassInvalid, "offset cannot be negative")
	}
	res, err := s.Resolve(ctx, in.File, ResolveOptions{FollowShortcut: true})
	if err != nil {
		return "", err
	}
	f := res.File
	if err := s.extractable(ctx, f); err != nil {
		return "", err
	}
	lang := strings.TrimSpace(in.OCRLanguage)
	key := "extract\x00" + f.ID + "\x00" + f.HeadRevisionID + "\x00" + f.ModifiedTime + "\x00" + lang
	notes := []string{"Google's OCR read this text. Lists, tables, columns, footnotes and endnotes are " +
		"often not recognized, Google says, and the text comes without its layout."}
	// keep_copy always makes a copy, since keeping one is what it asks.
	made := ""
	kept, cached, err := s.keptText(&s.ocrText, key, in.KeepCopy, func() (exported, error) {
		text, note, err := s.ocr(ctx, f, lang, in.KeepCopy)
		made = note
		return exported{text: text}, err
	})
	if err != nil {
		return "", err
	}
	if cached {
		notes = append(notes, "This is the text read "+model.Ago(kept.at, s.now())+", kept for paging; no copy "+
			"was made this time.")
	} else {
		notes = append(notes, made)
		if in.Offset > 0 {
			notes = append(notes, "The text read for an earlier window was no longer kept, so Google's OCR "+
				"read the file again. A second reading can differ from the first, so this window may not "+
				"start exactly where the last one ended.")
		}
	}
	if n, ok := f.SizeBytes(); ok && n > ocrAdvised {
		notes = append(notes, fmt.Sprintf("The file is %s, and Google asks for 2 MB or less for this, so "+
			"some of its text may be missing.", model.HumanSize(n)))
	}
	if strings.TrimSpace(kept.text) == "" {
		notes = append(notes, "Google's OCR found no text in it.")
	}
	w := keptWindow(kept, in.Offset, render.Budget(in.MaxChars))
	again := ""
	if lang != "" {
		again = "ocr_language: " + lang
	}
	return render.FileText(s.Model(ctx, res), render.FileTextOptions{
		Now:              s.now(),
		FollowedShortcut: res.FollowedShortcut,
		Format:           "text read by Google's OCR",
		Note:             strings.Join(notes, " "),
		Offset:           in.Offset,
		Bytes:            w.used,
		Total:            w.total,
		More:             w.more,
		Text:             w.text,
		Tool:             "extract_text",
		Again:            again,
	}), nil
}

// extractable refuses, before anything is written, a file this tool
// cannot read or need not: one with text read_file returns directly,
// one Google does not import as a Doc, one larger than Google converts,
// and one this account may not copy.
func (s *Service) extractable(ctx context.Context, f *gdrive.File) error {
	mime := gdrive.MimeOnly(f.MimeType)
	switch {
	case f.IsFolder():
		return Errorf(ClassInvalid, "%s is a folder; extract_text reads one PDF or image at a time.", f.Name)
	case f.IsWorkspaceDoc(), office.KindOf(mime) != office.None, model.IsTextLike(mime):
		return Errorf(ClassInvalid, "%s is %s, whose text read_file returns directly, with no copy made. "+
			"extract_text is for a PDF or an image.", f.Name, model.KindWithArticle(f))
	case !pdfOrImage(mime):
		return Errorf(ClassUnsupported, "%s is %s, and extract_text reads a PDF or an image. download_file "+
			"writes it to disk.", f.Name, model.KindWithArticle(f))
	}
	if doc, known := s.importsAsDoc(ctx, mime); (known && !doc) || (!known && !ocrTypes[mime]) {
		return Errorf(ClassUnsupported, "Google does not import %s as a Google Doc, so it has no OCR for %s. "+
			"It reads a PDF, or a JPEG, PNG, GIF or BMP image. download_file writes it to disk.",
			model.KindWithArticle(f), f.Name)
	}
	if n, ok := f.SizeBytes(); ok && n > ocrLargest {
		return Errorf(ClassUnsupported, "%s is %s, and Google converts nothing larger than 50 MB into a "+
			"Google Doc, which is the only way it reads text out of a file. download_file writes it to disk.",
			f.Name, model.HumanSize(n))
	}
	if f.Capabilities != nil && !f.Capabilities.CanCopy {
		return Errorf(ClassForbidden, "this account cannot copy %s, and Google reads the text of a file only "+
			"while making a copy of it. Its owner may have turned copying off for viewers and commenters.", f.Name)
	}
	return nil
}

// pdfOrImage reports whether a media type is the kind of file Google's
// OCR reads: a PDF or an image.
func pdfOrImage(mime string) bool {
	return mime == "application/pdf" || strings.HasPrefix(mime, "image/")
}

// importsAsDoc reports whether the account's import formats turn this
// type into a Google Doc, and whether they could be read at all.
func (s *Service) importsAsDoc(ctx context.Context, mime string) (doc, known bool) {
	targets, _ := s.importTargets(ctx, mime)
	s.mu.Lock()
	known = s.imports != nil
	s.mu.Unlock()
	return slices.Contains(targets, gdrive.MimeDocument), known
}

// ocr makes the copy, exports its text and deletes it, and says what
// happened to the copy.
func (s *Service) ocr(ctx context.Context, f *gdrive.File, lang string, keep bool) (string, string, error) {
	marker, err := newMarker()
	if err != nil {
		return "", "", wrap(err, "marking the temporary copy")
	}
	name := "Temporary copy of " + f.Name + " (extract_text)"
	if keep {
		name = "Copy of " + f.Name
	}
	// The root of My Drive, and ignoreDefaultVisibility: a copy there
	// inherits nobody's access, and skips an organization's rule that
	// shares every new file with everyone in it. The copy carries no id
	// from files.generateIds, because Drive refuses one for a Google Doc
	// (§2), so the POST cannot be repeated safely and the client does
	// not repeat it. A copy whose answer is lost is the one way this
	// call can leave a file behind, and the marker is how to find it.
	//
	// The copy runs on when the call is canceled, under a bound of its
	// own, so that Google's answer still arrives and names the copy to
	// delete. A cancel that cut the request short would leave the copy
	// unknown and unreported.
	meta := &gdrive.FileMeta{Name: name, MimeType: gdrive.MimeDocument, Parents: []string{RootAlias},
		AppProperties: map[string]string{ExtractMarkerKey: marker}}
	copyCtx, cancel := detached(ctx)
	defer cancel()
	copied, err := s.api.CopyFile(copyCtx, f.ID, meta, gapi.WriteOptions{OCRLanguage: lang, IgnoreDefaultVisibility: true})
	if err != nil {
		if gapi.Class(err) == ClassAmbiguousIO {
			return "", "", &Error{Class: ClassAmbiguousIO, Err: err, Message: fmt.Sprintf(
				"Google did not confirm making the temporary Google Doc copy of %s, so one named %q may or "+
					"may not now be in the root of My Drive. Calling extract_text again would make another. "+
					"search_files with raw_query: %s finds it if it is there, and trash_file removes it. "+
					"Google said: %s", f.Name, name, markerQuery(marker), errText(err))}
		}
		return "", "", wrap(err, "copying "+f.Name+" as a Google Doc to read its text")
	}
	s.forget(copied, false)
	if ctx.Err() != nil {
		// Nobody is waiting for the text, so the copy is not wanted,
		// even one asked to be kept: its id would reach nobody.
		note := "The copy was deleted, so nothing was left behind."
		if err := s.deleteTemporary(ctx, f, copied); err != nil {
			note = leftover(copied, err)
		}
		return "", "", &Error{Class: ClassUnexpected, Err: ctx.Err(), Message: fmt.Sprintf(
			"the call was canceled while Google made the temporary Google Doc copy of %s. %s", f.Name, note)}
	}
	text, exportErr := s.copyText(ctx, copied)
	reading := "reading the text of " + f.Name + " from the copy Google made"
	if keep {
		if exportErr != nil {
			return "", "", withNote(wrap(exportErr, reading), "The copy is kept, as asked: "+describeCopy(copied)+".")
		}
		return text, "The Google Doc the import made is kept, as asked: " + describeCopy(copied) +
			". read_file reads it, and trash_file removes it.", nil
	}
	deleteErr := s.deleteTemporary(ctx, f, copied)
	switch {
	case exportErr != nil && deleteErr != nil:
		return "", "", withNote(wrap(exportErr, reading), leftover(copied, deleteErr))
	case exportErr != nil:
		return "", "", withNote(wrap(exportErr, reading), "The temporary copy was deleted.")
	case deleteErr != nil:
		return text, leftover(copied, deleteErr), nil
	}
	// The copy is named although it is gone: it is the one file this
	// call made in the person's Drive, and get_file on its id is how
	// anyone checks that it is gone rather than taking this word for it.
	return text, "It came from a temporary Google Doc copy in the root of My Drive, id " + copied.ID +
		", which was deleted for good.", nil
}

// copyText exports the copy as plain text, which leaves out the image
// Google embeds beside the text it read. Google caps an export at 10 MB,
// and a Doc holds far less than that.
func (s *Service) copyText(ctx context.Context, copied *gdrive.File) (string, error) {
	text, err := s.exportAll(ctx, copied.ID, "text/plain")
	// Google's plain-text export of a Doc may start with a byte-order
	// mark, which is not text.
	return strings.TrimPrefix(text, "\ufeff"), err
}

// deleteTemporary removes the copy for good. It deletes only the file
// Drive's answer to the copy named, and only when that answer is the
// Google Doc this call asked for: a delete that skips the trash has no
// way back, so it must never reach anything else. It runs on even when
// the call was canceled.
func (s *Service) deleteTemporary(ctx context.Context, source, copied *gdrive.File) error {
	if copied.ID == "" || copied.ID == source.ID || copied.MimeType != gdrive.MimeDocument {
		return fmt.Errorf("%w: Drive's answer to the copy does not describe the Google Doc it made",
			gapi.ErrUnexpected)
	}
	ctx, cancel := detached(ctx)
	defer cancel()
	if err := s.api.DeleteFile(ctx, copied.ID); err != nil {
		return err
	}
	s.forget(copied, true)
	return nil
}

// detached is a context that a cancel of the call does not end, bounded
// by detachedTimeout.
func detached(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), detachedTimeout)
}

// leftover says that a temporary copy may still be there, and how to
// remove it.
func leftover(copied *gdrive.File, err error) string {
	if gapi.Class(err) == ClassAmbiguousIO {
		return "Drive did not confirm that the temporary copy was deleted: " + describeCopy(copied) +
			". get_file on its id says whether it is still there, and trash_file removes it."
	}
	return "The temporary copy could not be deleted (" + errText(err) + "): " + describeCopy(copied) +
		". trash_file removes it."
}

// errText is Google's own words for a failure, or the failure's.
func errText(err error) string {
	if msg := gapi.Message(err); msg != "" {
		return msg
	}
	return err.Error()
}

// withNote adds a sentence to an LLM-facing error, for what a failure
// left behind.
func withNote(err error, note string) error {
	var se *Error
	if errors.As(err, &se) {
		return &Error{Class: se.Class, Message: se.Message + " " + note, Err: se.Err}
	}
	return err
}

// describeCopy names the copy with its id and link.
func describeCopy(copied *gdrive.File) string {
	out := fmt.Sprintf("%q, id %s, in the root of My Drive", copied.Name, copied.ID)
	if copied.WebViewLink != "" {
		out += ", " + copied.WebViewLink
	}
	return out
}

// newMarker is a value no other copy carries, so a search for it finds
// exactly the copy of one call.
func newMarker() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return extractMarkerPrefix + hex.EncodeToString(b), nil
}

// markerQuery is the search that finds a copy by its marker.
func markerQuery(marker string) string {
	return fmt.Sprintf("appProperties has { key='%s' and value='%s' }", ExtractMarkerKey, marker)
}
