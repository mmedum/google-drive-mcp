package ref_test

import (
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/ref"
)

// Every shape a reference can take, so a change to the parser is
// measured against all of them rather than against the easy one.
var benchRefs = []string{
	"1AbCdEfGhIjKlFixtureUvWxYz0123456",
	"https://docs.google.com/document/d/1AbCdEfGhIjKlFixtureUvWxYz0123456/edit#heading=h.x",
	"https://drive.google.com/file/d/1AbCdEfGhIjKlFixtureUvWxYz0123456/view?usp=sharing&resourcekey=0-abc",
	"root",
	"/Projects/2026/Budget.xlsx",
	"drive:Marketing/Campaigns/Q3 plan",
}

func BenchmarkParse(b *testing.B) {
	for b.Loop() {
		for _, in := range benchRefs {
			if _, err := ref.Parse(in); err != nil {
				b.Fatalf("%s: %v", in, err)
			}
		}
	}
}
