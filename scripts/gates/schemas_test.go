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

// TestTheDumpIsTheWholeSurface holds where the decision lives.
//
// It used to live here: the gate set an environment for the current dump
// and not for the baseline, so every flag-gated tool read as newly added
// forever and a removed one could not be reported at all. Setting the
// environment on both sides fixed the symptom and left the decision in
// the wrong place — a gate keeping its own list of gates, which was
// already short by one.
//
// It lives in the binary now: --dump-schemas emits tools.FullSurface, so
// both sides carry every tool that can register and every other reader
// of the dump gets the same answer. TestFullSurfaceRegistersEverything,
// beside Register, is what holds the surface itself.
func TestTheDumpIsTheWholeSurface(t *testing.T) {
	src, err := os.ReadFile("../../cmd/google-drive-mcp/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "tools.FullSurface(cfg)") {
		t.Error("--dump-schemas does not emit tools.FullSurface, so the schema diff compares " +
			"a partial surface and cannot report a flag-gated tool being removed")
	}

	gate, err := os.ReadFile("schemas.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(gate)
	if i := strings.Index(body, "func schemaDiff("); i >= 0 {
		if j := strings.Index(body[i:], "\n}\n"); j >= 0 {
			body = body[i : i+j]
		}
	}
	if strings.Contains(body, "fullSurfaceEnv") {
		t.Error("schemaDiff is setting an environment again; the binary decides what a dump contains, " +
			"or the gate has to keep a list of gates in step with a package it cannot see — and that " +
			"list can only hold booleans, so it cannot express Sharing at all")
	}
}
