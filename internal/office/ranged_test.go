package office_test

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v2/internal/office"
	"github.com/mmedum/google-drive-mcp/v2/internal/office/officetest"
)

// remote stands for a file reachable only by byte ranges, and counts
// what was asked of it.
type remote struct {
	data     []byte
	requests int
	asked    int64
	fail     error
	short    bool
}

func (r *remote) fetch(off, n int64) ([]byte, error) {
	r.requests++
	r.asked += n
	if r.fail != nil {
		return nil, r.fail
	}
	if r.short {
		n--
	}
	return r.data[off : off+n], nil
}

// noise is bytes that do not compress, standing for an image.
func noise(n int) string {
	rng := rand.New(rand.NewPCG(1, 2))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.UintN(256))
	}
	return string(b)
}

func TestRangeReaderFetchesOnlyThePartsItReads(t *testing.T) {
	// Four megabytes of image before the text, as a deck or a document
	// with pictures has. None of it is text, so none of it is fetched.
	data := officetest.Zip(
		officetest.Entry{Name: "word/media/image1.png", Data: noise(4 << 20), Store: true},
		officetest.Entry{Name: "word/document.xml", Data: `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
			`<w:body><w:p><w:r><w:t>after the picture</w:t></w:r></w:p></w:body></w:document>`},
	)
	r := &remote{data: data}
	ra := office.NewRangeReader(int64(len(data)), r.fetch, 0)
	res, err := office.Extract(ra, int64(len(data)), office.Docx, office.Options{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Text != "after the picture\n" {
		t.Errorf("text = %q", res.Text)
	}
	if r.asked > 256<<10 {
		t.Errorf("fetched %d bytes of a %d-byte file whose text is a few hundred bytes", r.asked, len(data))
	}
	if ra.Fetched() != r.asked || ra.Requests() != r.requests {
		t.Errorf("the reader reports %d bytes in %d requests; the file saw %d in %d",
			ra.Fetched(), ra.Requests(), r.asked, r.requests)
	}
}

func TestRangeReaderFetchesFurtherAheadAsAReadCarriesOn(t *testing.T) {
	// A four-megabyte part read from start to end is 64 blocks. Fetched
	// a block at a time that is 64 requests; growing the run each time
	// it is a handful.
	var body strings.Builder
	for body.Len() < 4<<20 {
		body.WriteString(officetest.WordParagraph("", "a line of the long document"))
	}
	data := officetest.Zip(officetest.Entry{Name: "word/document.xml", Store: true,
		Data: `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
			body.String() + `</w:body></w:document>`})
	r := &remote{data: data}
	ra := office.NewRangeReader(int64(len(data)), r.fetch, 0)
	if _, err := office.Extract(ra, int64(len(data)), office.Docx, office.Options{}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if r.requests > 12 {
		t.Errorf("reading a 4 MB part took %d requests, want at most 12", r.requests)
	}
}

func TestATooLongDirectoryIsRefusedBeforeItIsRead(t *testing.T) {
	// archive/zip reads the whole table of contents into memory before
	// anything else. A file that lists too many entries is refused from
	// the record at its end, before a byte of that table is fetched.
	many := make([]officetest.Entry, office.MaxEntries+1)
	for i := range many {
		many[i] = officetest.Entry{Name: fmt.Sprintf("entry-with-a-longish-name-%05d.xml", i)}
	}
	data := officetest.Zip(many...)
	r := &remote{data: data}
	ra := office.NewRangeReader(int64(len(data)), r.fetch, 0)
	_, err := office.Extract(ra, int64(len(data)), office.Odt, office.Options{})
	var oe *office.Error
	if !errors.As(err, &oe) || !oe.Limit {
		t.Fatalf("err = %v, want a limit", err)
	}
	if r.asked > 128<<10 {
		t.Errorf("fetched %d bytes of a %d-byte file before refusing it", r.asked, len(data))
	}
}

func TestRangeReaderStopsAtItsBudget(t *testing.T) {
	data := officetest.Docx(officetest.WordParagraph("", "text"))
	r := &remote{data: data}
	ra := office.NewRangeReader(int64(len(data)), r.fetch, 100)
	_, err := office.Extract(ra, int64(len(data)), office.Docx, office.Options{})
	if !errors.Is(err, office.ErrFetchLimit) {
		t.Fatalf("err = %v, want ErrFetchLimit", err)
	}
	if r.requests != 0 {
		t.Errorf("%d requests went out past the budget", r.requests)
	}
}

func TestRangeReaderKeepsTheFirstFailure(t *testing.T) {
	data := officetest.Docx(officetest.WordParagraph("", "text"))
	cases := map[string]*remote{
		"the fetch fails":            {data: data, fail: errors.New("connection reset")},
		"the fetch comes back short": {data: data, short: true},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			ra := office.NewRangeReader(int64(len(data)), r.fetch, 0)
			if _, err := office.Extract(ra, int64(len(data)), office.Docx, office.Options{}); err == nil {
				t.Fatal("Extract succeeded over a failing fetch")
			}
			if ra.Err() == nil {
				t.Error("Err() is nil after a failed fetch")
			}
			// A failure is not fetched again.
			before := r.requests
			_, _ = ra.ReadAt(make([]byte, 10), 0)
			if r.requests != before {
				t.Errorf("a read after the failure fetched again (%d requests, was %d)", r.requests, before)
			}
		})
	}
}

func TestRangeReaderReadsAcrossBlocksAndStopsAtTheEnd(t *testing.T) {
	data := []byte(noise(200 << 10))
	r := &remote{data: data}
	ra := office.NewRangeReader(int64(len(data)), r.fetch, 0)
	got := make([]byte, 100<<10)
	n, err := ra.ReadAt(got, 50<<10)
	if err != nil || n != len(got) || !bytes.Equal(got, data[50<<10:150<<10]) {
		t.Fatalf("ReadAt across blocks: n=%d err=%v, bytes match %v", n, err, bytes.Equal(got[:n], data[50<<10:50<<10+n]))
	}
	tail := make([]byte, 10)
	n, err = ra.ReadAt(tail, int64(len(data))-4)
	if n != 4 || err == nil {
		t.Errorf("a read past the end: n=%d err=%v, want 4 and EOF", n, err)
	}
	if _, err := ra.ReadAt(tail, -1); err == nil {
		t.Error("a negative offset was read")
	}
}
