// Package mediatype is the one place this server knows what a media type
// means: what to call it, which kind filter it answers to, what short
// name it goes by as an export format, and — for Google's own kinds —
// what a read exports it to and what a download writes by default.
//
// It exists because five tables in three packages used to answer those
// questions separately: a display name in internal/model, a query clause
// in internal/service, an export name in internal/gapi, and a read
// format and a download format in internal/service again. Adding a kind
// meant four edits in three packages, and a typo showed up as a search
// that silently matched nothing rather than as a failure.
//
// It is a leaf: it imports internal/gdrive for the media types Drive
// gives its own kinds and nothing else, so every layer can use it
// without inverting the module layout.
package mediatype

import (
	"slices"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// Entry is everything known about one media type. Every field is
// optional except the type itself: most types have a name and nothing
// else, and the ones Google owns have the most to say.
type Entry struct {
	// Mime is the media type this entry is about.
	Mime string
	// Name is what the Drive interface calls it. Empty means the name is
	// derived from the type itself, which is what happens to the long
	// tail of image, video and audio formats.
	Name string
	// Group is the kind filter this type answers to: the word a caller
	// passes as `kind` to search_files or list_folder. Empty means the
	// type is in no filter, or is in one matched by prefix (see Prefix
	// groups below).
	Group string
	// Export is the short name this type goes by as an export format —
	// "docx", "pdf", "md". Empty means Drive does not export to it, so it
	// is not a format a caller may ask for.
	Export string
	// ReadAs is the media type read_file exports this kind to. Only
	// Google's own kinds have one: everything else has bytes already.
	ReadAs string
	// DownloadAs is the short export name a download uses when the caller
	// names none. Only Google's own kinds have one.
	DownloadAs string
}

// Prefix groups are the kind filters Drive answers with a prefix match
// rather than a list of types, because the list is open-ended: nobody
// can enumerate every image format somebody might upload.
var prefixGroups = map[string]string{
	"image": "image/",
	"video": "video/",
	"audio": "audio/",
}

// entries is the registry. A type appears once, however many questions
// it answers.
var entries = []Entry{
	// Google's own kinds. Folder and shortcut are kinds a filter can
	// name but neither has content, so neither reads or downloads.
	{Mime: gdrive.MimeFolder, Name: "folder", Group: "folder"},
	{Mime: gdrive.MimeShortcut, Name: "shortcut", Group: "shortcut"},
	{Mime: gdrive.MimeDocument, Name: "Google Doc", Group: "doc",
		ReadAs: "text/markdown", DownloadAs: "docx"},
	{Mime: gdrive.MimeSheet, Name: "Google Sheet", Group: "sheet",
		ReadAs: "text/csv", DownloadAs: "xlsx"},
	{Mime: gdrive.MimeSlides, Name: "Google Slides", Group: "slides",
		ReadAs: "text/plain", DownloadAs: "pptx"},
	{Mime: gdrive.MimeForm, Name: "Google Form", Group: "form"},
	{Mime: gdrive.MimeDrawing, Name: "Google Drawing", Group: "drawing", DownloadAs: "png"},
	// Apps Script exports as its own JSON and has no filter of its own.
	{Mime: gdrive.MimeScript, Name: "Apps Script",
		ReadAs: "application/vnd.google-apps.script+json", DownloadAs: "json"},
	{Mime: gdrive.MimeSite, Name: "Google Site"},
	{Mime: gdrive.MimeMap, Name: "Google My Map"},
	{Mime: gdrive.MimeVid, Name: "Google Vid"},
	{Mime: gdrive.MimeJam, Name: "Jamboard"},

	// The formats a file arrives as, and the formats Drive exports to.
	// Many types are both, which is the reason for one table.
	{Mime: "application/pdf", Name: "PDF", Group: "pdf", Export: "pdf"},
	{Mime: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Name: "Word document", Group: "office", Export: "docx"},
	{Mime: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		Name: "Excel spreadsheet", Group: "office", Export: "xlsx"},
	{Mime: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		Name: "PowerPoint presentation", Group: "office", Export: "pptx"},
	{Mime: "application/msword", Name: "Word document (legacy)", Group: "office"},
	{Mime: "application/vnd.ms-excel", Name: "Excel spreadsheet (legacy)", Group: "office"},
	{Mime: "application/vnd.ms-powerpoint", Name: "PowerPoint presentation (legacy)", Group: "office"},
	{Mime: "application/vnd.oasis.opendocument.text", Name: "OpenDocument text", Export: "odt"},
	{Mime: "application/vnd.oasis.opendocument.spreadsheet", Name: "OpenDocument spreadsheet", Export: "ods"},
	{Mime: "application/vnd.oasis.opendocument.presentation", Name: "OpenDocument presentation", Export: "odp"},
	{Mime: "application/rtf", Name: "rich text file", Export: "rtf"},
	{Mime: "application/zip", Name: "zip archive", Export: "zip"},
	{Mime: "application/gzip", Name: "gzip archive"},
	{Mime: "application/x-tar", Name: "tar archive"},
	{Mime: "application/json", Name: "JSON file"},
	{Mime: "application/xml", Name: "XML file"},
	{Mime: "application/epub+zip", Name: "EPUB book", Export: "epub"},
	{Mime: "application/octet-stream", Name: "binary file"},
	{Mime: "text/plain", Name: "text file", Export: "txt"},
	{Mime: "text/csv", Name: "CSV file", Export: "csv"},
	{Mime: "text/tab-separated-values", Name: "TSV file", Export: "tsv"},
	{Mime: "text/markdown", Name: "Markdown file", Export: "md"},
	{Mime: "text/html", Name: "HTML file", Export: "html"},
	{Mime: "text/xml", Name: "XML file"},
	{Mime: "image/svg+xml", Name: "SVG image", Export: "svg"},
	// These two carry no name of their own: "JPEG image" and "PNG image"
	// are what the fallback already derives, and a second spelling of a
	// name is a second thing to keep in step.
	{Mime: "image/jpeg", Export: "jpg"},
	{Mime: "image/png", Export: "png"},
	// The export target of an Apps Script project, which is not a type
	// anything is stored as.
	{Mime: "application/vnd.google-apps.script+json", Export: "json"},
}

// byMime, byExport and groups are the indexes the lookups use. They are
// built once from entries, so entries stays the only thing to edit.
var (
	byMime   = map[string]Entry{}
	byExport = map[string]string{}
	groups   = map[string][]string{}
)

func init() {
	for _, e := range entries {
		byMime[e.Mime] = e
		if e.Export != "" {
			// The first entry wins, which matters for the types that share
			// a short name with nothing else. A duplicate would be a bug,
			// and TestNoTwoTypesShareAnExportName is what catches it.
			if _, taken := byExport[e.Export]; !taken {
				byExport[e.Export] = e.Mime
			}
		}
		if e.Group != "" {
			groups[e.Group] = append(groups[e.Group], e.Mime)
		}
	}
	for _, mimes := range groups {
		slices.Sort(mimes)
	}
}

// Lookup returns the entry for a media type, ignoring any parameters on
// it, and whether there was one.
func Lookup(mime string) (Entry, bool) {
	e, ok := byMime[gdrive.MimeOnly(mime)]
	return e, ok
}

// Name is what the Drive interface calls this media type. Everything not
// in the registry is described from the type itself rather than shown as
// a media type, because "MP4 video" says more to a reader than
// "video/mp4" and there is no list that could cover them all.
func Name(mime string) string {
	mime = gdrive.MimeOnly(mime)
	if mime == "" {
		return "file"
	}
	if e, ok := byMime[mime]; ok && e.Name != "" {
		return e.Name
	}
	base, sub, _ := strings.Cut(mime, "/")
	switch base {
	case "image":
		return strings.ToUpper(subtypeWord(sub)) + " image"
	case "video":
		return strings.ToUpper(subtypeWord(sub)) + " video"
	case "audio":
		return strings.ToUpper(subtypeWord(sub)) + " audio"
	case "text":
		return subtypeWord(sub) + " text file"
	}
	return mime
}

// subtypeWord strips the vendor and suffix decoration from a media
// subtype: "x-matroska" becomes "matroska", "vnd.wave" becomes "wave".
func subtypeWord(sub string) string {
	sub = strings.TrimPrefix(sub, "x-")
	sub = strings.TrimPrefix(sub, "vnd.")
	if i := strings.IndexByte(sub, '+'); i > 0 {
		sub = sub[:i]
	}
	if sub == "" {
		return "file"
	}
	return sub
}

// Groups lists every kind filter a caller may name, sorted.
func Groups() []string {
	out := make([]string, 0, len(groups)+len(prefixGroups))
	for name := range groups {
		out = append(out, name)
	}
	for name := range prefixGroups {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// GroupMimes lists the media types in a kind filter, sorted, or nil for
// a filter matched by prefix.
func GroupMimes(group string) []string {
	return slices.Clone(groups[strings.ToLower(strings.TrimSpace(group))])
}

// GroupPrefix returns the media-type prefix a kind filter matches on, or
// "" for a filter that names its types.
func GroupPrefix(group string) string {
	return prefixGroups[strings.ToLower(strings.TrimSpace(group))]
}

// IsGroup reports whether a word names a kind filter.
func IsGroup(group string) bool {
	group = strings.ToLower(strings.TrimSpace(group))
	if _, ok := prefixGroups[group]; ok {
		return true
	}
	_, ok := groups[group]
	return ok
}

// ExportName is the short name a media type goes by as an export format,
// or "" when Drive does not export to it.
func ExportName(mime string) string {
	return byMime[gdrive.MimeOnly(mime)].Export
}

// ExportMime is the media type behind a short export name, or "".
func ExportMime(name string) string {
	return byExport[strings.ToLower(strings.TrimSpace(name))]
}

// ExportNames lists every short export name, sorted, for tool
// descriptions and error messages.
func ExportNames() []string {
	out := make([]string, 0, len(byExport))
	for name := range byExport {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// ReadAs is the media type read_file exports a Google-native kind to,
// or "" for anything that has bytes of its own.
func ReadAs(mime string) string {
	return byMime[gdrive.MimeOnly(mime)].ReadAs
}

// DownloadAs is the short export name a download writes when the caller
// names none, or "" for a kind Drive does not export.
func DownloadAs(mime string) string {
	return byMime[gdrive.MimeOnly(mime)].DownloadAs
}

// Entries returns the registry, for the tests that hold the surface in
// step with it.
func Entries() []Entry { return slices.Clone(entries) }
