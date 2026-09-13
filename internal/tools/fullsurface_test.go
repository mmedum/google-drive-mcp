package tools

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/config"
)

// registeredUnder is the set of tool names a configuration registers,
// read back through a client so it is the surface a caller would see.
func registeredUnder(t *testing.T, cfg config.Config) map[string]bool {
	t.Helper()
	ctx := context.Background()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(s, Deps{Config: cfg})

	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, tool := range res.Tools {
		out[tool.Name] = true
	}
	return out
}

// TestFullSurfaceRegistersEverything holds the claim FullSurface's
// comment makes. --dump-schemas emits that surface and the schema diff
// compares two dumps, so a tool missing from it is a tool whose removal
// the gate cannot report — which is the fault this replaced, and the
// gate's own flag list was already short by one.
func TestFullSurfaceRegistersEverything(t *testing.T) {
	full := registeredUnder(t, FullSurface(config.Config{}))
	if len(full) == 0 {
		t.Fatal("the full surface registers nothing, so this test is reading nothing")
	}
	for _, readOnly := range []bool{false, true} {
		for _, destructive := range []bool{false, true} {
			for _, labels := range []bool{false, true} {
				for _, activity := range []bool{false, true} {
					for _, sharing := range []config.Sharing{config.SharingAll, config.SharingOff} {
						cfg := config.Config{
							ReadOnly: readOnly, EnableDestructive: destructive,
							Labels: labels, Activity: activity, Sharing: sharing,
						}
						for name := range registeredUnder(t, cfg) {
							if !full[name] {
								t.Errorf("%s registers with read_only=%v destructive=%v labels=%v "+
									"activity=%v sharing=%v and is missing from the full surface, so "+
									"the schema dump would not carry it",
									name, readOnly, destructive, labels, activity, sharing)
							}
						}
					}
				}
			}
		}
	}
}
