package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/v2/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v2/internal/service"
)

func TestTheHandWrittenPDFPointsAtItsOwnObjects(t *testing.T) {
	// A reader finds every object through the cross-reference table, so
	// an offset one byte out is a PDF Google may refuse to import. Each
	// entry has to land on its object's header.
	pdf := handWrittenPDF("DRIVER RECEIPT", "TOTAL (42)")
	text := string(pdf)
	at := strings.LastIndex(text, "startxref\n")
	xref, err := strconv.Atoi(strings.Fields(text[at+len("startxref\n"):])[0])
	if err != nil || !strings.HasPrefix(text[xref:], "xref\n0 6\n") {
		t.Fatalf("startxref = %d (%v), which is not the table", xref, err)
	}
	entries := strings.Split(text[xref:], "\n")[3:8]
	for i, e := range entries {
		if len(e)+1 != 20 {
			t.Errorf("entry %d is %d bytes with its line end, want 20: %q", i+1, len(e)+1, e)
		}
		off, err := strconv.Atoi(e[:10])
		if err != nil || !strings.HasPrefix(text[off:], strconv.Itoa(i+1)+" 0 obj\n") {
			t.Errorf("entry %d points at %d, which is not object %d", i+1, off, i+1)
		}
	}
	if !bytes.Contains(pdf, []byte(`(TOTAL \(42\)) Tj`)) {
		t.Error("the parentheses in a line are not escaped")
	}
}

func TestTheKeptCopysIDIsReadFromWhatExtractTextSays(t *testing.T) {
	// The pattern is matched against the service's own words, so a change
	// to them fails here rather than leaving a kept copy behind in a live
	// account's My Drive.
	fake := drivetest.New()
	t.Cleanup(fake.Close)
	fake.AddFile("id-receipt-fixture", "receipt.pdf", "application/pdf", fake.RootID)
	fake.SetContent("id-receipt-fixture", "TOTAL 42")
	svc := service.New(drivetest.Client(t, fake), service.Options{Now: func() time.Time {
		return time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	}})
	out, err := svc.ExtractText(t.Context(), service.ExtractTextInput{File: "id-receipt-fixture", KeepCopy: true})
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	m := keptCopyID.FindStringSubmatch(out)
	if len(m) != 2 || fake.Files[m[1]] == nil || fake.Files[m[1]].Name != "Copy of receipt.pdf" {
		t.Errorf("read %v out of:\n%s", m, out)
	}
}
