package main

import (
	"archive/zip"
	"bytes"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/v2/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v2/internal/office/officetest"
	"github.com/mmedum/google-drive-mcp/v2/internal/service"
)

// The padding moves the second slide past the first 64 KiB block, so a
// live read fetches a range past the start, and the deck still reads
// as the same text. Its two windows, cut as the driver cuts them, join
// to that text.
func TestAPaddedDeckReadsTheSameAndItsWindowsJoin(t *testing.T) {
	plain := officetest.Pptx([]string{"Opening slide"}, []string{"Closing slide", "with two lines"})
	deck := padded(plain, "ppt/slides/slide1.xml")
	zr, err := zip.NewReader(bytes.NewReader(deck), int64(len(deck)))
	if err != nil {
		t.Fatalf("the padded deck is not a zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name != "ppt/slides/slide2.xml" {
			continue
		}
		if at, err := f.DataOffset(); err != nil || at < 64<<10 {
			t.Errorf("the second slide starts at %d (%v), inside the first 64 KiB block", at, err)
		}
	}

	fake := drivetest.New()
	t.Cleanup(fake.Close)
	const mime = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	for id, data := range map[string][]byte{"id-plain-fixture": plain, "id-padded-fixture": deck} {
		fake.AddFile(id, id+".pptx", mime, fake.RootID)
		fake.SetContent(id, string(data))
	}
	svc := service.New(drivetest.Client(t, fake), service.Options{Now: func() time.Time {
		return time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	}})
	read := func(id string, offset int64, max int) string {
		out, err := svc.ReadFile(t.Context(), service.ReadFileInput{File: id, Offset: offset, MaxChars: max})
		if err != nil {
			t.Fatalf("ReadFile %s: %v", id, err)
		}
		return textOf(out)
	}
	whole := read("id-padded-fixture", 0, 0)
	if want := read("id-plain-fixture", 0, 0); whole != want || want == "" {
		t.Fatalf("the padded deck reads %q, the plain one %q", whole, want)
	}
	if joined := read("id-padded-fixture", 0, 20)[:20] + read("id-padded-fixture", 20, 100000); joined != whole {
		t.Errorf("the windows join to %q, not %q", joined, whole)
	}
}
