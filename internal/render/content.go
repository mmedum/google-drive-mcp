package render

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/model"
)

// FileTextOptions describe one window of a file's text.
type FileTextOptions struct {
	Now              time.Time
	FollowedShortcut string
	// Format names what the text is: markdown, csv, plain text.
	Format string
	// Note is what the reader has to know about what was returned, such
	// as a spreadsheet's other sheets.
	Note string
	// Offset is where this window starts, Bytes is how long it is, and
	// Total is the whole file where that is known (-1 when it is not).
	Offset int64
	Bytes  int64
	Total  int64
	// More says there is another window after this one.
	More bool
	Text string
}

// FileText renders a window of a file's text under a header that says
// what the file is and exactly which part of it this is. A model that
// cannot tell a whole file from its first page will summarise the first
// page and call it the file.
func FileText(f *model.File, o FileTextOptions) string {
	if f == nil {
		return "(no file)\n"
	}
	var b buf
	cardHead(&b, f, "", false, o.FollowedShortcut)
	b.field("head revision", f.HeadRevisionID)
	b.field("showing", showing(o))
	// One note, not two: a read of a Google document already says where
	// its content is edited, and the file card's own boundary note would
	// say it again in different words.
	note := o.Note
	if note == "" {
		note = f.BoundaryNote
	}
	if note != "" {
		b.line("note: " + note)
	}
	b.line("---")
	b.sb.WriteString(o.Text)
	if !strings.HasSuffix(o.Text, "\n") {
		b.sb.WriteByte('\n')
	}
	return b.String()
}

// showing describes the window in one line: the format, how much of the
// file it is, and how to ask for the rest.
func showing(o FileTextOptions) string {
	format := o.Format
	if format == "" {
		format = "text"
	}
	end := o.Offset + o.Bytes
	switch {
	case o.Bytes == 0 && o.Offset == 0:
		return format + ", and the file is empty"
	case o.Bytes == 0:
		return fmt.Sprintf("%s, nothing at offset %d: the file ends before it", format, o.Offset)
	case !o.More && o.Offset == 0:
		return fmt.Sprintf("%s, the whole file (%s)", format, model.HumanSize(o.Bytes))
	case !o.More:
		return fmt.Sprintf("%s, bytes %d to %d, to the end of the file", format, o.Offset, end-1)
	}
	of := ""
	if o.Total > 0 {
		of = fmt.Sprintf(" of %d", o.Total)
	}
	return fmt.Sprintf("%s, bytes %d to %d%s — there is more: call read_file again with offset: %d",
		format, o.Offset, end-1, of, end)
}

// dataURI matches an inlined image in a markdown export.
var dataURI = regexp.MustCompile(`!\[[^\]]*\]\(data:[^)]*\)`)

// StripDataURIs removes inlined images from a markdown export. Google
// exports a Doc's images as base64 in the text; one photograph is
// hundreds of kilobytes of nothing a model can read, and it would fill
// the whole window.
func StripDataURIs(text string) string {
	return dataURI.ReplaceAllString(text, "[image removed]")
}

// DownloadOptions describe a completed download.
type DownloadOptions struct {
	Now time.Time
	// Path is where the bytes landed.
	Path  string
	Bytes int64
	// Format is the extension written, which for a Google-native
	// document is the format it was exported to.
	Format   string
	Revision string
	Note     string
	// Checksum is the verdict on the bytes written, as facts; the
	// sentence is chosen here.
	Checksum model.Checksum
}

// Download renders what a download produced. The path is the point of
// the call, so it comes first after the file it came from.
func Download(f *model.File, o DownloadOptions) string {
	if f == nil {
		return "(no file)\n"
	}
	var b buf
	cardHead(&b, f, "saved", false, "")
	b.field("to", o.Path)
	b.field("bytes", fmt.Sprintf("%d (%s)", o.Bytes, model.HumanSize(o.Bytes)))
	b.field("format", o.Format)
	if o.Revision != "" {
		b.field("revision", o.Revision)
	}
	b.field("checksum", Checksum(o.Checksum))
	if o.Note != "" {
		b.line("note: " + o.Note)
	}
	return b.String()
}
