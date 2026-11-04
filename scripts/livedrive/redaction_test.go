package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v3/scripts/internal/redact"
	"github.com/mmedum/google-drive-mcp/v3/scripts/internal/transcript"
)

// An address reaches the transcript through the ARGUMENTS as well as
// through the results: starting an approval puts the signed-in account's
// own address into the arguments of every -write run, which nobody chose.
//
// It is asked of the transcript rather than of the redactor, because the
// transcript is what leaves the call site no other way to print.
func TestTheEchoedArgumentsAreRedacted(t *testing.T) {
	var out bytes.Buffer
	tr := transcript.NewTo(redact.NewRedactor(false), &out, &out)
	args := map[string]any{
		"file": "id-something", "action": "start",
		"reviewers": []any{"someone@example.com"},
	}
	tr.Say(banner(call{name: "an approval is started", tool: "manage_approval", args: args}))
	if strings.Contains(out.String(), "someone@example.com") {
		t.Errorf("an address survived the redactor in an argument echo: %s", out.String())
	}
}
