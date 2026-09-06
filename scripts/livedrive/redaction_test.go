package main

import (
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/scripts/internal/mcpstdio"
	"github.com/mmedum/google-drive-mcp/scripts/internal/redact"
)

// An address reaches the transcript through the ARGUMENTS as well as
// through the results, and until phase 4 only the results were
// redacted. Starting an approval puts the signed-in account's own
// address into the arguments of every -write run, which nobody chose.
func TestTheEchoedArgumentsAreRedacted(t *testing.T) {
	red := redact.NewRedactor(false)
	args := map[string]any{
		"file": "id-something", "action": "start",
		"reviewers": []any{"someone@example.com"},
	}
	got := red.Do(mcpstdio.Encode(args))
	if strings.Contains(got, "someone@example.com") {
		t.Errorf("an address survived the redactor in an argument echo: %s", got)
	}
}
