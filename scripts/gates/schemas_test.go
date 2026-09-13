package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// dumpOf builds a surface from tool names, so a case reads as the thing
// it is about rather than as a literal.
func dumpOf(tools ...string) schemaDump {
	var d schemaDump
	if err := json.Unmarshal([]byte(`{"server":"x","tools":[]}`), &d); err != nil {
		panic(err)
	}
	for _, name := range tools {
		var t struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			} `json:"inputSchema"`
		}
		t.Name = name
		t.InputSchema.Properties = map[string]json.RawMessage{"file": json.RawMessage(`{}`)}
		d.Tools = append(d.Tools, t)
	}
	return d
}

// TestARemovedToolIsBreaking is the case the gate exists for, and the
// one it could not report while the two sides were dumped with different
// environments: a flag-gated tool was never in the baseline, so its
// removal compared as nothing at all.
func TestARemovedToolIsBreaking(t *testing.T) {
	added, breaking := compareSurfaces(dumpOf("read_file", "delete_file"), dumpOf("read_file"))
	if len(added) != 0 {
		t.Errorf("nothing was added, got %v", added)
	}
	if len(breaking) != 1 || !strings.Contains(breaking[0], "tool removed: delete_file") {
		t.Errorf("a removed tool is breaking, got %v", breaking)
	}
}

// A tool present on both sides is not "added". Dumping the current build
// with the full surface and the baseline without it reported every
// flag-gated tool as new on every run, which is how a line stops being
// read.
func TestAToolOnBothSidesIsNotAdded(t *testing.T) {
	added, breaking := compareSurfaces(dumpOf("read_file", "delete_file"), dumpOf("read_file", "delete_file"))
	if len(added) != 0 || len(breaking) != 0 {
		t.Errorf("an unchanged surface is no news, got added=%v breaking=%v", added, breaking)
	}
}

func TestANewToolIsAddedAndNotBreaking(t *testing.T) {
	added, breaking := compareSurfaces(dumpOf("read_file"), dumpOf("read_file", "list_labels"))
	if len(added) != 1 || added[0] != "list_labels" {
		t.Errorf("added = %v", added)
	}
	if len(breaking) != 0 {
		t.Errorf("adding a tool is not breaking, got %v", breaking)
	}
}

// TestBothSidesAreDumpedWithTheSameEnvironment holds the rule the whole
// fix is: the surface compared has to be the same surface on both sides.
// It is checked against the source because the alternative is building
// two binaries, and a comment cannot hold a rule this easy to undo.
func TestBothSidesAreDumpedWithTheSameEnvironment(t *testing.T) {
	src, err := os.ReadFile("schemas.go")
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	for _, line := range strings.Split(string(src), "\n") {
		if strings.Contains(line, "dumpSchemas(") && !strings.Contains(line, "func dumpSchemas") {
			calls = append(calls, strings.TrimSpace(line))
		}
	}
	if len(calls) != 2 {
		t.Fatalf("expected the current and baseline dumps, found %d: %v", len(calls), calls)
	}
	for _, c := range calls {
		if !strings.Contains(c, "env...") {
			t.Errorf("a dump without the full surface: %s\n"+
				"both sides must be dumped with the same environment, or a flag-gated tool "+
				"is reported as added forever and its removal cannot be reported at all", c)
		}
	}
}
