// Package model turns Drive's wire form into the view this server shows:
// the kind of thing in plain words, where it sits, who can see it, what
// the signed-in person may do with it, and sizes and times as people
// read them. It computes; internal/render lays out.
package model

import (
	"slices"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/mediatype"
)

// KindName describes a media type in the words the Drive interface
// uses. The table behind it is internal/mediatype, which is also where
// the export names and the kind filters come from: one media type, one
// place that knows what it means.
func KindName(mime string) string { return mediatype.Name(mime) }

// Kind describes a file, following a shortcut into its own description
// so a listing says "shortcut to a Google Doc" rather than "shortcut".
func Kind(f *gdrive.File) string {
	if f == nil {
		return "file"
	}
	if f.IsShortcut() && f.ShortcutDetails != nil && f.ShortcutDetails.TargetMimeType != "" {
		target := KindName(f.ShortcutDetails.TargetMimeType)
		return "shortcut to " + Article(target) + " " + target
	}
	return KindName(f.MimeType)
}

// Article picks "a" or "an" for a phrase. A leading vowel takes "an";
// so does a short all-capitals initialism whose first letter is spoken
// with a leading vowel, which is why it is "an SVG image" but "a PDF".
func Article(phrase string) string {
	phrase = strings.TrimSpace(phrase)
	if phrase == "" {
		return "a"
	}
	head, _, _ := strings.Cut(phrase, " ")
	first := head[0]
	if strings.ContainsRune("AEIOUaeiou", rune(first)) {
		return "an"
	}
	if len(head) <= 4 && head == strings.ToUpper(head) && strings.ContainsRune("FHLMNRSX", rune(first)) {
		return "an"
	}
	return "a"
}

// KindWithArticle names what a file is, ready to drop into a sentence:
// "Notes is a Google Doc", not "Notes is Google Doc". Every message that
// says what something is goes through it, because the article depends on
// the kind and no caller should have to work that out.
func KindWithArticle(f *gdrive.File) string {
	kind := Kind(f)
	return Article(kind) + " " + kind
}

// BoundaryNote says, for a Google-native document, where its content is
// actually edited. This server stops at the file boundary, and a result
// that reads a Doc without saying so invites an edit that cannot happen.
func BoundaryNote(mime string) string {
	switch mime {
	case gdrive.MimeDocument:
		return "this is a Google Doc. Its text is available through Google's export; its content is edited through the Docs API, which this server does not offer."
	case gdrive.MimeSheet:
		return "this is a Google Sheet. Its cells are read and edited through the Sheets API, which this server does not offer."
	case gdrive.MimeSlides:
		return "this is a Google Slides deck. Its slides are edited through the Slides API, which this server does not offer."
	}
	return ""
}

// ReadNote is what a read of a Google-native document has to say about
// the text it just returned: which part of the file it is, and what the
// text cannot be used for. It sits beside BoundaryNote because the two
// answer the same question at different moments — one describes a file,
// the other describes a read of it — and a result that carried both
// would say the same thing twice.
func ReadNote(mime string) string {
	switch mime {
	case gdrive.MimeSheet:
		return "this is the FIRST SHEET, as csv. Another sheet, or one range of cells, is a Sheets API read, " +
			"which this server does not offer; download_file writes the whole workbook as xlsx."
	case gdrive.MimeSlides:
		return "the text of the slides, without their layout. Slides are edited through the Slides API, " +
			"which this server does not offer."
	case gdrive.MimeDocument:
		return "Google's own markdown export of the document. Its content is edited through the Docs API, " +
			"which this server does not offer; update_content cannot replace it."
	}
	return ""
}

// IsTextLike reports whether a blob's bytes are text this server will
// return inline. Google's own kinds are not text-like: they have no
// bytes until they are exported.
func IsTextLike(mime string) bool {
	mime = strings.ToLower(gdrive.MimeOnly(mime))
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	switch mime {
	case "application/json", "application/xml", "application/x-yaml", "application/yaml",
		"application/javascript", "application/x-sh", "application/toml", "application/sql",
		"application/x-ndjson", "image/svg+xml":
		return true
	}
	return strings.HasSuffix(mime, "+json") || strings.HasSuffix(mime, "+xml")
}

// ExportFormats turns the exportLinks map Drive returns into the short
// format names the tools speak, without exposing the links themselves.
// It lives here rather than in internal/gapi because a short format name
// is a word this server made up for a person to type, and that package
// speaks only Drive's own wire types.
func ExportFormats(f *gdrive.File) []string {
	if f == nil || len(f.ExportLinks) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for mime := range f.ExportLinks {
		if name := mediatype.ExportName(mime); name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}
