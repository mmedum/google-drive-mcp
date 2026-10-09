package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mmedum/google-drive-mcp/v2/internal/gapi"
	"github.com/mmedum/google-drive-mcp/v2/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v2/internal/model"
	"github.com/mmedum/google-drive-mcp/v2/internal/office"
)

// legacyOffice are the binary formats Office used before 2007, which
// this server does not read, with the Google kind each imports as.
var legacyOffice = map[string]string{
	"application/msword":            "doc",
	"application/vnd.ms-excel":      "sheet",
	"application/vnd.ms-powerpoint": "slides",
}

// officePlan is how a Word, Excel or PowerPoint file, or an OpenDocument
// one, becomes text: read out of its own bytes here, with nothing
// converted or copied in Drive.
func officePlan(f *gdrive.File, kind office.Kind, format string) (readPlan, error) {
	if kind.Spreadsheet() {
		plan := readPlan{office: kind, formatName: "csv", textMime: "text/csv",
			note: "this is the FIRST SHEET only. A number shows as stored, without its display format; a date " +
				"or a time as YYYY-MM-DD or HH:MM:SS; a formula as its last calculated value. download_file " +
				"writes the whole workbook, and copy_file with convert_to: sheet makes a Google Sheet of it."}
		switch format {
		case "", "csv":
		case "tsv":
			plan.formatName, plan.textMime = "tsv", "text/tab-separated-values"
		default:
			return readPlan{}, Errorf(ClassInvalid, "a spreadsheet reads as csv or tsv, not %q. "+
				"download_file writes it as it is.", format)
		}
		return plan, nil
	}
	if format != "" {
		return readPlan{}, Errorf(ClassInvalid, "read_file returns %s as text, and format applies to a "+
			"spreadsheet only. download_file writes it as it is.", model.KindWithArticle(f))
	}
	if kind.Presentation() {
		return readPlan{office: kind, formatName: "text", textMime: "text/plain",
			note: "the text of each slide, under a line naming the slide. Speaker notes, layouts and " +
				"images are left out."}, nil
	}
	return readPlan{office: kind, formatName: "text", textMime: "text/plain",
		note: "the text of the document in order: headings marked with #, list items with -, and table " +
			"rows with | between the cells. Formatting, images, comments, footnotes and deleted tracked " +
			"changes are left out."}, nil
}

// officeWindow reads one window of an Office file's text. The text comes
// out of the file's own bytes, fetched by byte range, so a deck of
// photographs costs its directory and its slides' XML rather than the
// photographs. The whole text is kept for ExportTTL, under the same 10
// MB cap as an export, so the windows after the first cost nothing.
func (s *Service) officeWindow(ctx context.Context, res *Resolved, plan readPlan, in ReadFileInput, budget int,
) (textWindow, error) {
	f := res.File
	size, known := f.SizeBytes()
	if !known {
		return textWindow{}, Errorf(ClassUnsupported, "Drive does not say how large %s is, and reading it "+
			"in parts needs that. download_file writes it to disk.", f.Name)
	}
	key := "office\x00" + f.ID + "\x00" + f.HeadRevisionID + "\x00" + f.ModifiedTime + "\x00" + plan.formatName
	kept, ok := s.exportedText(key)
	if !ok {
		var err error
		if kept, err = s.extractOffice(ctx, f, size, plan); err != nil {
			return textWindow{}, err
		}
		s.keepExport(key, kept)
	}
	return keptWindow(kept, in.Offset, budget), nil
}

// extractOffice reads the whole text of an Office file.
func (s *Service) extractOffice(ctx context.Context, f *gdrive.File, size int64, plan readPlan) (exported, error) {
	started := s.now()
	ra := office.NewRangeReader(size, func(off, n int64) ([]byte, error) {
		return s.fetchRange(ctx, f.ID, off, n)
	}, 0)
	delim := byte(',')
	if plan.formatName == "tsv" {
		delim = '\t'
	}
	got, err := office.Extract(ra, size, plan.office, office.Options{Delimiter: delim, MaxText: MaxExport})
	s.log.DebugContext(ctx, "office text read", "requests", ra.Requests(), "bytes", ra.Fetched(),
		"ms", s.now().Sub(started).Milliseconds(), "ok", err == nil)
	if err != nil {
		return exported{}, s.officeError(err, ra.Err(), f)
	}
	return exported{text: got.Text, note: officeNote(got, plan)}, nil
}

// fetchRange is one byte range of a file. A response that is not the
// range asked for is refused rather than read as if it were: bytes from
// the wrong place would parse as damage, or not at all.
func (s *Service) fetchRange(ctx context.Context, id string, off, n int64) ([]byte, error) {
	c, err := s.api.Download(ctx, id, gapi.DownloadOptions{Offset: off, Length: n})
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Body.Close() }()
	if !c.Partial && off > 0 {
		return nil, fmt.Errorf("%w: Drive answered a byte range with the whole file", gapi.ErrUnexpected)
	}
	buf := make([]byte, n)
	got, err := io.ReadFull(c.Body, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return buf[:got], nil
}

// officeError says why an Office file could not be read. A failure to
// fetch comes first: the parser saw it only as bytes that stopped
// arriving, and would call the file damaged.
func (s *Service) officeError(err, fetchErr error, f *gdrive.File) error {
	var oe *office.Error
	if fetchErr != nil && !errors.As(fetchErr, &oe) {
		return s.contentError(fetchErr, f, "reading")
	}
	if errors.As(err, &oe) && oe.Limit {
		return Errorf(ClassUnsupported, "%s is larger than this server reads in one go: %s. "+
			"download_file writes it to disk.", f.Name, oe.Reason)
	}
	reason := err.Error()
	if oe != nil {
		reason = oe.Reason
	}
	return Errorf(ClassUnsupported, "%s could not be read as %s: %s. download_file writes it to disk as it is.",
		f.Name, model.KindWithArticle(f), reason)
}

// officeNote is what the text itself says about the read: which sheet,
// how many slides, and whether the text stops short of the end.
func officeNote(got *office.Result, plan readPlan) string {
	var parts []string
	switch {
	case got.Sheet != "" && got.Sheets > 1:
		parts = append(parts, fmt.Sprintf("It is the sheet named %q, one of %d.", got.Sheet, got.Sheets))
	case got.Sheet != "":
		parts = append(parts, fmt.Sprintf("It is the sheet named %q.", got.Sheet))
	case plan.office.Presentation():
		parts = append(parts, fmt.Sprintf("The deck has %s.", model.Plural(got.Slides, "slide", "slides")))
	}
	if got.Truncated {
		parts = append(parts, "The text stops short of the end of the file, at the most this server reads "+
			"from one file; download_file writes all of it.")
	}
	return strings.Join(parts, " ")
}
