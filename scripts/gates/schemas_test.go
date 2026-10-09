package main

import (
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

// A tool kept by name breaks a caller when it loses a field it took or
// returned, at any depth, when a field or a map's values change type,
// or when an input is newly required; adding fields does not.
func TestBrokenFields(t *testing.T) {
	old, err := readSurface([]byte(`{"tools":[
		{"name":"get_file","inputSchema":{"type":"object","properties":{"file":{"type":"string"},"fields":{"type":"string"},"labels":{"type":"boolean"}},"required":["file"]},
		 "outputSchema":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},"size":{"type":"integer"},
		   "exposure":{"type":"object","properties":{"before":{"type":"string"},"after":{"type":"string"}}}}}},
		{"name":"list_folder","inputSchema":{"type":"object","properties":{"folder":{"type":"string"},
		   "restrictions":{"type":"object","additionalProperties":{"type":"boolean"}}}},
		 "outputSchema":{"type":"object","properties":{"files":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"owner":{"type":"string"}}}},
		   "dropped":{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}}}}}},
		{"name":"gone","inputSchema":{"type":"object","properties":{"x":{"type":"string"}}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cur, err := readSurface([]byte(`{"tools":[
		{"name":"get_file","inputSchema":{"type":"object","properties":{"file":{"type":"string"},"fields":{"type":"string"},"etag":{"type":"string"}},"required":["file","fields"]},
		 "outputSchema":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":["null","string"]},"etag":{"type":"string"},
		   "exposure":{"type":"object","properties":{"before":{"type":"string"}}}}}},
		{"name":"list_folder","inputSchema":{"type":"object","properties":{"folder":{"type":"string"},
		   "restrictions":{"type":"object","additionalProperties":{"type":"string"}},
		   "page":{"type":"object","properties":{"size":{"type":"integer"}},"required":["size"]}}},
		 "outputSchema":{"type":"object","properties":{"files":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"}}}}}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := brokenFields(old.fields, cur.fields)
	want := []string{
		`get_file: input field labels removed`,
		`get_file: output field exposure.after removed`,
		`get_file: output field name changed type from "string" to ["null","string"]`,
		`get_file: output field size removed`,
		`get_file: input field fields newly required`,
		`list_folder: input field restrictions{} changed type from "boolean" to "string"`,
		`list_folder: output field dropped removed`,
		`list_folder: output field files[].owner removed`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// A removed tool and a removed resource break a caller. An added one,
// and a changed description, are printed and do not. A schema spelled
// another way is not a change.
func TestARemovedToolOrResourceIsBreaking(t *testing.T) {
	base, err := readSurface([]byte(`{"tools":[{"name":"read_file","description":"a"},{"name":"delete_file"},
		{"name":"get_file","inputSchema":{"type":"object","properties":{"file":{"type":"string","description":"a <b>"}}}}],
		"resourceTemplates":[{"name":"file text","uriTemplate":"gdrive://{file}"},{"name":"card","uriTemplate":"gdrive://{file}/meta"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cur, err := readSurface([]byte(`{"tools":[{"name":"read_file","description":"b"},{"name":"list_labels"},
		{"name":"get_file","inputSchema":{"properties":{"file":{"description":"a \u003cb\u003e","type":"string"}},"type":"object"}}],
		"resourceTemplates":[{"name":"file text","uriTemplate":"gdrive://{file}"},{"name":"children","uriTemplate":"gdrive://{file}/children"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	got := compareSurfaces(&out, base, cur)
	want := []string{"delete_file", "resource gdrive://{file}/meta"}
	if !slices.Equal(got, want) {
		t.Errorf("breaking = %q, want %q", got, want)
	}
	for _, line := range []string{"  + list_labels\n", "  + resource gdrive://{file}/children\n", "  ~ read_file "} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("the report does not say %q:\n%s", line, out.String())
		}
	}
	if strings.Contains(out.String(), "get_file") {
		t.Errorf("a schema spelled another way is reported as changed:\n%s", out.String())
	}
}

// A schema the walk cannot see into is refused, not read as having no
// fields, which would let every field inside it go unnoticed.
func TestASchemaTheDiffCannotReadIsRefused(t *testing.T) {
	_, err := readSurface([]byte(`{"tools":[{"name":"get_file","outputSchema":{"type":"object","properties":{"card":{"$ref":"#/$defs/card"}}}}]}`))
	if err == nil || !strings.Contains(err.Error(), "get_file output: card uses $ref") {
		t.Errorf("err = %v, want the $ref named", err)
	}
}

// The dumps below differ only where a case says so. surfaceV1 has two
// tools; the version stamp is the release it was recorded for.
const (
	surfaceV1 = `{"version":"v2.0.1","sdk":"v1.8.0","tools":[
		{"name":"get_file","description":"d","inputSchema":{"type":"object","properties":{"file":{"type":"string"}}}},
		{"name":"trash_file","description":"d","inputSchema":{"type":"object","properties":{"file":{"type":"string"}}}}]}`
	// The same surface, another stamp, other key order.
	surfaceV1Restamped = `{"tools":[
		{"inputSchema":{"properties":{"file":{"type":"string"}},"type":"object"},"description":"d","name":"get_file"},
		{"description":"d","name":"trash_file","inputSchema":{"type":"object","properties":{"file":{"type":"string"}}}}],
		"sdk":"v1.9.0","version":"v2.0.2-0.20261009000000-abcdefabcdef"}`
	surfaceAdded = `{"version":"dev","tools":[
		{"name":"get_file","description":"d","inputSchema":{"type":"object","properties":{"file":{"type":"string"},"fields":{"type":"string"}}}},
		{"name":"trash_file","description":"d","inputSchema":{"type":"object","properties":{"file":{"type":"string"}}}}]}`
	surfaceRemoved = `{"version":"dev","tools":[
		{"name":"get_file","description":"d","inputSchema":{"type":"object","properties":{}}},
		{"name":"trash_file","description":"d","inputSchema":{"type":"object","properties":{"file":{"type":"string"}}}}]}`

	released   = "# Changelog\n\n## [Unreleased]\n\n## [2.0.1] - 2026-10-01\n\n- x\n"
	unreleased = "# Changelog\n\n## [Unreleased]\n\n### Added\n\n- y\n\n## [2.0.1] - 2026-10-01\n\n- x\n"
	cutting    = "# Changelog\n\n## [Unreleased]\n\n## [2.1.0] - 2026-10-10\n\n- y\n\n## [2.0.1] - 2026-10-01\n\n- x\n"
	cuttingV3  = "# Changelog\n\n## [3.0.0] - 2026-10-10\n\n- y\n\n## [2.0.1] - 2026-10-01\n\n- x\n"
	firstDraft = "# Changelog\n\n## [Unreleased]\n\n- first\n"
)

// Every way the diff fails, and the states it passes.
func TestDiffSurface(t *testing.T) {
	for _, c := range []struct {
		name                string
		current, baseline   string
		changelog, wantFail string
	}{
		{"an unreleased addition", surfaceAdded, surfaceV1, unreleased, ""},
		{"the release itself, restamped and reordered", surfaceV1Restamped, surfaceV1, released, ""},
		{"before the first release, with no baseline", surfaceAdded, "", firstDraft, ""},
		{"a removed field", surfaceRemoved, surfaceV1, unreleased, "breaks a caller of v2.0.1 in 1 way(s)"},
		{"no baseline once a release exists", surfaceV1, "", released, "names v2.0.1 as released, but testdata/schema-baseline.json is missing"},
		{"a baseline older than the newest release", surfaceAdded, surfaceV1, cutting, `the baseline is the "v2.0.1" surface, but CHANGELOG.md's newest release is v2.1.0`},
		{"a change with nothing unreleased", surfaceAdded, surfaceV1, released, "nothing is under [Unreleased], so this build is v2.0.1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var baseline []byte
			if c.baseline != "" {
				baseline = []byte(c.baseline)
			}
			err := diffSurface(io.Discard, []byte(c.current), baseline, c.changelog)
			switch {
			case c.wantFail == "" && err != nil:
				t.Errorf("failed: %v", err)
			case c.wantFail != "" && (err == nil || !strings.Contains(err.Error(), c.wantFail)):
				t.Errorf("err = %v, want it to say %q", err, c.wantFail)
			}
		})
	}
}

// A baseline is recorded only from a build stamped with the release
// being cut, and a break only as a new major version.
func TestCheckBaseline(t *testing.T) {
	stamp := func(dump, version string) []byte {
		return []byte(strings.Replace(dump, `"version":"dev"`, `"version":"`+version+`"`, 1))
	}
	for _, c := range []struct {
		name               string
		current            []byte
		baseline           string
		changelog, wantErr string
	}{
		{"an addition in a minor release", stamp(surfaceAdded, "v2.1.0"), surfaceV1, cutting, ""},
		{"a break in a major release", stamp(surfaceRemoved, "v3.0.0"), surfaceV1, cuttingV3, ""},
		{"the first baseline", stamp(surfaceAdded, "v2.1.0"), "", cutting, ""},
		{"a build stamped with another version", []byte(surfaceAdded), surfaceV1, cutting, `the build is stamped "dev", but the release being cut is v2.1.0`},
		{"a break in a minor release", stamp(surfaceRemoved, "v2.1.0"), surfaceV1, cutting, "v2.1.0 breaks a caller of v2.0.1 in 1 way(s) above, and is not a new major version"},
		{"no release in the CHANGELOG", stamp(surfaceAdded, "v2.1.0"), "", firstDraft, "names no release yet"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var baseline []byte
			if c.baseline != "" {
				baseline = []byte(c.baseline)
			}
			err := checkBaseline(io.Discard, c.current, baseline, c.changelog)
			switch {
			case c.wantErr == "" && err != nil:
				t.Errorf("refused: %v", err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Errorf("err = %v, want it to say %q", err, c.wantErr)
			}
		})
	}
}

func TestNewMajor(t *testing.T) {
	for _, c := range []struct {
		from, to string
		want     bool
	}{
		{"v2.0.1", "v3.0.0", true},
		{"v2.0.1", "v2.1.0", false},
		{"v2.0.1", "v2.0.1", false},
		{"", "v3.0.0", false},
		{"dev", "v3.0.0", false},
	} {
		if got := newMajor(c.from, c.to); got != c.want {
			t.Errorf("newMajor(%q, %q) = %t, want %t", c.from, c.to, got, c.want)
		}
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

// fieldsOf is the fields of a dump of one tool, get_file, with the
// input and output schemas given.
func fieldsOf(t *testing.T, input, output string) map[string]toolFields {
	t.Helper()
	s, err := readSurface([]byte(`{"tools":[{"name":"get_file","inputSchema":` + input + `,"outputSchema":` + output + `}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return s.fields
}

// A type change breaks a caller in one direction only: an input that
// takes fewer types, or an output that may return more. An input that
// takes more, or an output that returns fewer, breaks nobody. No type
// and the schema `true` are any type, and the schema `false` is none.
func TestATypeChangeBreaksOneWay(t *testing.T) {
	const empty = `{"type":"object"}`
	field := func(typ string) string {
		switch typ {
		case "":
			return `{"type":"object","properties":{"x":{}}}`
		case "true", "false":
			return `{"type":"object","properties":{"x":` + typ + `}}`
		}
		return `{"type":"object","properties":{"x":{"type":` + typ + `}}}`
	}
	for _, c := range []struct {
		side, was, now, want string
	}{
		{"input", `"boolean"`, `["null","boolean"]`, ""},
		{"input", `"integer"`, `"number"`, ""},
		{"input", `"string"`, ``, ""},
		{"input", `"string"`, `true`, ""},
		{"input", `false`, `"string"`, ""},
		{"input", `"number"`, `"integer"`, `get_file: input field x changed type from "number" to "integer"`},
		{"input", `["null","boolean"]`, `"boolean"`, `get_file: input field x changed type from ["null","boolean"] to "boolean"`},
		{"input", `true`, `"string"`, `get_file: input field x changed type from any to "string"`},
		{"input", `"string"`, `false`, `get_file: input field x changed type from "string" to false`},
		{"output", `["null","string"]`, `"string"`, ""},
		{"output", `"number"`, `"integer"`, ""},
		{"output", ``, `"string"`, ""},
		{"output", `true`, `"string"`, ""},
		{"output", `"string"`, `false`, ""},
		{"output", `"string"`, `["null","string"]`, `get_file: output field x changed type from "string" to ["null","string"]`},
		{"output", `"integer"`, `"number"`, `get_file: output field x changed type from "integer" to "number"`},
		{"output", `"string"`, `true`, `get_file: output field x changed type from "string" to any`},
		{"output", `false`, `"string"`, `get_file: output field x changed type from false to "string"`},
	} {
		was, now := fieldsOf(t, field(c.was), empty), fieldsOf(t, field(c.now), empty)
		if c.side == "output" {
			was, now = fieldsOf(t, empty, field(c.was)), fieldsOf(t, empty, field(c.now))
		}
		got := strings.Join(brokenFields(was, now), "; ")
		if got != c.want {
			t.Errorf("%s %s → %s: got %q, want %q", c.side, c.was, c.now, got, c.want)
		}
	}
}

// A list or a map whose items are the schema `true` takes any value,
// and has no fields of its own to walk.
func TestABooleanItemsSchemaIsAnyValue(t *testing.T) {
	list := func(items string) string {
		return `{"type":"object","properties":{"rows":{"type":"array","items":` + items + `},` +
			`"extra":{"type":"object","additionalProperties":` + items + `}}}`
	}
	if got := brokenFields(fieldsOf(t, list(`{"type":"string"}`), `{}`), fieldsOf(t, list(`true`), `{}`)); len(got) != 0 {
		t.Errorf("an input list and map that take any value now: %q", got)
	}
	want := []string{`get_file: output field extra{} changed type from "string" to any`,
		`get_file: output field rows[] changed type from "string" to any`}
	if got := brokenFields(fieldsOf(t, `{}`, list(`{"type":"string"}`)), fieldsOf(t, `{}`, list(`true`))); !slices.Equal(got, want) {
		t.Errorf("an output list and map that may return any value: got %q, want %q", got, want)
	}
}

// An output a caller read as always there breaks it when it may be
// missing; an input that stops taking a value breaks a caller that sent
// it. An output that may carry a new value, and an input newly limited
// to a list, are named for a person to read.
func TestRequiredOutputsAndListedValues(t *testing.T) {
	was := fieldsOf(t,
		`{"type":"object","properties":{"mode":{"type":"string","enum":["a","b"]},"free":{"type":"string"}}}`,
		`{"type":"object","properties":{"id":{"type":"string"},"state":{"type":"string","enum":["x"]}},"required":["id"]}`)
	now := fieldsOf(t,
		`{"type":"object","properties":{"mode":{"type":"string","enum":["a","c"]},"free":{"type":"string","enum":["z"]}}}`,
		`{"type":"object","properties":{"id":{"type":"string"},"state":{"type":"string","enum":["x","y"]}}}`)
	want := []string{`get_file: input field mode no longer takes "b"`, `get_file: output field id no longer required`}
	if got := brokenFields(was, now); !slices.Equal(got, want) {
		t.Errorf("breaking = %q, want %q", got, want)
	}
	notes := []string{`get_file: output field state may now be "y"`, `get_file: input field free now takes only "z"`}
	if got := valueNotes(was, now); !slices.Equal(got, notes) {
		t.Errorf("notes = %q, want %q", got, notes)
	}
	// The other way round, an output newly required and an input that
	// takes any value where it took a list break nobody; mode loses c.
	if got, want := brokenFields(now, was), []string{`get_file: input field mode no longer takes "c"`}; !slices.Equal(got, want) {
		t.Errorf("the reverse = %q, want %q", got, want)
	}
}
