package service

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v2/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v2/internal/gdrive"
)

// A read canceled while the parser runs, with every byte it needed
// already fetched, is reported as the cancel it was rather than as a
// file that could not be read. Through ReadFile the cancel would race
// the fetch, so the mapping is held here.
func TestACanceledOfficeReadIsNotCalledUnreadable(t *testing.T) {
	fake := drivetest.New()
	t.Cleanup(fake.Close)
	s := New(drivetest.Client(t, fake), Options{})
	err := s.officeError(context.Canceled, nil, &gdrive.File{Name: "Report.docx",
		MimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"})
	if got := err.Error(); !strings.HasPrefix(got, "[unexpected]") || strings.Contains(got, "could not be read") {
		t.Errorf("err = %q, want [unexpected] naming the cancel", got)
	}
}
