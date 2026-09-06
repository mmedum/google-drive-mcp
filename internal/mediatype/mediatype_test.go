package mediatype_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/mediatype"
)

func TestNames(t *testing.T) {
	cases := map[string]string{
		gdrive.MimeFolder:   "folder",
		gdrive.MimeDocument: "Google Doc",
		gdrive.MimeShortcut: "shortcut",
		"application/pdf":   "PDF",
		"text/csv":          "CSV file",
		// Not in the registry: described from the type rather than shown
		// as one, because no list could cover every format.
		"image/jpeg":         "JPEG image",
		"video/x-matroska":   "MATROSKA video",
		"audio/vnd.wave":     "WAVE audio",
		"text/x-go":          "go text file",
		"application/x-nope": "application/x-nope",
		// A parameter on the type does not change what it is.
		"text/plain; charset=UTF-8": "text file",
		"":                          "file",
	}
	for mime, want := range cases {
		if got := mediatype.Name(mime); got != want {
			t.Errorf("Name(%q) = %q, want %q", mime, got, want)
		}
	}
}

// TestNoTwoTypesShareAnExportName is what makes the reverse lookup
// honest: ExportMime picks one media type per short name, and two
// entries claiming the same name would make which one it picks depend on
// the order of a slice.
func TestNoTwoTypesShareAnExportName(t *testing.T) {
	seen := map[string]string{}
	for _, e := range mediatype.Entries() {
		if e.Export == "" {
			continue
		}
		if first, taken := seen[e.Export]; taken {
			t.Errorf("the export name %q is claimed by both %s and %s", e.Export, first, e.Mime)
		}
		seen[e.Export] = e.Mime
	}
	if len(seen) == 0 {
		t.Fatal("no entry carries an export name, so this test is looking at nothing")
	}
}

func TestEveryTypeAppearsOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range mediatype.Entries() {
		if seen[e.Mime] {
			t.Errorf("%s appears twice, so which entry answers depends on the order", e.Mime)
		}
		seen[e.Mime] = true
		if e.Mime != gdrive.MimeOnly(e.Mime) {
			t.Errorf("%q carries a parameter, and lookups strip them", e.Mime)
		}
		if e.Mime != strings.ToLower(e.Mime) {
			t.Errorf("%q is not lower case, and lookups do not fold case", e.Mime)
		}
	}
}

// TestRoundTripBetweenNamesAndTypes is the property the download path
// depends on: a short name a caller types resolves to the media type the
// registry holds, and back.
func TestRoundTripBetweenNamesAndTypes(t *testing.T) {
	names := mediatype.ExportNames()
	if len(names) < 10 {
		t.Fatalf("only %d export names, which is fewer than Drive offers", len(names))
	}
	if !slices.IsSorted(names) {
		t.Error("the export names are not sorted, so a tool description built from them would churn")
	}
	for _, name := range names {
		mime := mediatype.ExportMime(name)
		if mime == "" {
			t.Errorf("the export name %q resolves to no media type", name)
			continue
		}
		if back := mediatype.ExportName(mime); back != name {
			t.Errorf("%q resolves to %q, which names itself %q", name, mime, back)
		}
	}
	// Case and space are what a person types, not what they mean.
	if mediatype.ExportMime("  PDF ") != "application/pdf" {
		t.Error("an export name is not read case- and space-insensitively")
	}
}

// TestEveryGoogleKindThatReadsAlsoDownloads holds the pairing the two
// old tables agreed on by hand: a kind with no bytes of its own needs
// both a form to read it in and a form to write it out, and one without
// the other is a kind that half works.
func TestEveryGoogleKindThatReadsAlsoDownloads(t *testing.T) {
	checked := 0
	for _, e := range mediatype.Entries() {
		if e.ReadAs == "" && e.DownloadAs == "" {
			continue
		}
		checked++
		if !gdrive.IsGoogleMime(e.Mime) {
			t.Errorf("%s has a read or download format but is not one of Google's own kinds", e.Mime)
		}
		if e.ReadAs != "" && mediatype.ExportName(e.ReadAs) == "" {
			t.Errorf("%s reads as %q, which is not an export format Drive offers", e.Mime, e.ReadAs)
		}
		if e.DownloadAs != "" && mediatype.ExportMime(e.DownloadAs) == "" {
			t.Errorf("%s downloads as %q, which is not an export format", e.Mime, e.DownloadAs)
		}
	}
	if checked == 0 {
		t.Fatal("no entry carries a read or download format, so this test is looking at nothing")
	}
	// A Drawing has no document form, so it downloads and does not read;
	// everything else with a text form has both.
	if mediatype.ReadAs(gdrive.MimeDrawing) != "" {
		t.Error("a Drawing has no text form and should not claim one")
	}
	for _, mime := range []string{gdrive.MimeDocument, gdrive.MimeSheet, gdrive.MimeSlides, gdrive.MimeScript} {
		if mediatype.ReadAs(mime) == "" || mediatype.DownloadAs(mime) == "" {
			t.Errorf("%s reads as %q and downloads as %q; it needs both",
				mime, mediatype.ReadAs(mime), mediatype.DownloadAs(mime))
		}
	}
}

func TestGroupsAreEitherAListOrAPrefix(t *testing.T) {
	all := mediatype.Groups()
	if len(all) == 0 {
		t.Fatal("no kind filters at all")
	}
	if !slices.IsSorted(all) {
		t.Error("the kind filters are not sorted")
	}
	for _, group := range all {
		mimes, prefix := mediatype.GroupMimes(group), mediatype.GroupPrefix(group)
		switch {
		case len(mimes) == 0 && prefix == "":
			t.Errorf("the kind %q matches nothing, so a search for it would find nothing and say nothing", group)
		case len(mimes) > 0 && prefix != "":
			t.Errorf("the kind %q is both a list and a prefix", group)
		}
	}
	// A word that names no kind matches nothing, which is what makes
	// service.kindClause able to refuse it rather than search for
	// everything.
	if len(mediatype.GroupMimes("not-a-kind")) != 0 || mediatype.GroupPrefix("not-a-kind") != "" {
		t.Error("a word that is not a kind matched something")
	}
	// The office filter is the one that names several types; the others
	// name one or match a prefix.
	if got := mediatype.GroupMimes("office"); len(got) != 6 {
		t.Errorf("the office kind covers %v, want the six Office formats", got)
	}
}
