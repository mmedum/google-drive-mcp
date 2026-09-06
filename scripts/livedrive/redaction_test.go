package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/scripts/internal/mcpstdio"
	"github.com/mmedum/google-drive-mcp/scripts/internal/redact"
	"github.com/mmedum/google-drive-mcp/scripts/internal/transcript"
)

// An address reaches the transcript through the ARGUMENTS as well as
// through the results, and until phase 4 only the results were redacted.
// Starting an approval puts the signed-in account's own address into the
// arguments of every -write run, which nobody chose.
//
// It is asked of the transcript rather than of the redactor now. The
// difference is the whole point of that type: this used to pass because
// somebody had wrapped this particular call site, and it passes now
// because the call site cannot print any other way.
func TestTheEchoedArgumentsAreRedacted(t *testing.T) {
	var out bytes.Buffer
	tr := transcript.NewTo(redact.NewRedactor(false), &out, &out)
	args := map[string]any{
		"file": "id-something", "action": "start",
		"reviewers": []any{"someone@example.com"},
	}
	tr.Sayf("\n=== %s %s ===", "manage_approval", mcpstdio.Encode(args))
	if strings.Contains(out.String(), "someone@example.com") {
		t.Errorf("an address survived the redactor in an argument echo: %s", out.String())
	}
}
