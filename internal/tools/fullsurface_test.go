package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
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

func TestGatedReadsTheGatedToolsOffRegistration(t *testing.T) {
	widening, destroying := Gated()
	if want := []string{"lift_drive_restriction", "share_by_link", "share_outside_organization",
		"transfer_ownership"}; !slices.Equal(widening, want) {
		t.Errorf("widening = %v, want %v", widening, want)
	}
	if want := []string{"delete_comment", "delete_drive", "delete_file", "delete_revision",
		"empty_trash"}; !slices.Equal(destroying, want) {
		t.Errorf("destroying = %v, want %v", destroying, want)
	}
}

// A shared phrase is placed by its key, and a key that names no phrase
// stops registration rather than reach a model as "{ref}".
func TestSharedPhrasesArePutInPlace(t *testing.T) {
	type in struct {
		File string `json:"file" jsonschema:"the file: {{ref}}"`
	}
	schema, err := jsonschema.For[in](nil)
	if err != nil {
		t.Fatal(err)
	}
	fillPhrases("example_tool", schema)
	if want := "the file: an id, any Drive URL, a path from My Drive, or a shared-drive path"; schema.Properties["file"].Description != want {
		t.Errorf("description = %q, want %q", schema.Properties["file"].Description, want)
	}

	type typo struct {
		File string `json:"file" jsonschema:"the file: {{reff}}"`
	}
	bad, err := jsonschema.For[typo](nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "{{reff}} names no shared phrase") {
			t.Errorf("an unknown key: recovered %v, want a refusal naming {{reff}}", r)
		}
	}()
	fillPhrases("example_tool", bad)
}

// A phrase reaches a client filled in wherever its key stands: in a
// nested field of a list item, and in the tool's own description.
func TestAToolRegisteredThroughAddToolCarriesItsPhrases(t *testing.T) {
	type item struct {
		Target string `json:"target" jsonschema:"where: {{ref}}"`
	}
	type in struct {
		Items []item `json:"items" jsonschema:"the items"`
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	addTool(s, &mcp.Tool{Name: "example_tool", Description: "acts on one item. {{ambiguous}}."},
		func(context.Context, *mcp.CallToolRequest, in) (*mcp.CallToolResult, any, error) {
			return nil, nil, nil
		})
	raw, err := json.Marshal(listedTools(t, s))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"where: an id, any Drive URL, a path from My Drive, or a shared-drive path",
		"acts on one item. A name or path matching more than one item is refused with the candidates listed."} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("tools/list does not carry %q:\n%s", want, raw)
		}
	}
}

// No key is left unfilled anywhere on the surface a client lists, which
// holds a tool registered around addTool too.
func TestNoRegisteredToolCarriesAPhraseKey(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(s, Deps{Config: FullSurface(config.Config{})})
	listed := listedTools(t, s)
	if len(listed) < 40 {
		t.Fatalf("listed %d tools; the full surface has more, so this read nothing", len(listed))
	}
	raw, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if left := placeholder.FindString(string(raw)); left != "" {
		t.Errorf("a registered tool carries %s unfilled", left)
	}
}

// listedTools is what a client's tools/list returns from s.
func listedTools(t *testing.T, s *mcp.Server) []*mcp.Tool {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.Tools
}
