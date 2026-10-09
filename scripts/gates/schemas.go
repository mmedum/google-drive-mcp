package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// schemaDump is the shape --dump-schemas writes, as the gates that list
// tools and their options read it.
type schemaDump struct {
	Server string `json:"server"`
	Tools  []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		InputSchema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		} `json:"inputSchema"`
	} `json:"tools"`
}

func (d schemaDump) names() []string {
	out := make([]string, 0, len(d.Tools))
	for _, t := range d.Tools {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

// dumpSchemas runs a build of the server and decodes its tool surface.
//
// The environment is always pinned, even when there is nothing to add:
// inheriting the developer's shell means a GDRIVE_LABELS exported in a
// terminal quietly changes what the breaking-change gate baselines
// against, which is the kind of difference nobody sees until a release.
func dumpSchemas(binary string, env ...string) (schemaDump, []byte, error) {
	cmd := exec.Command(binary, "--dump-schemas")
	cmd.Env = append(scrubbedEnv(), env...)
	raw, err := cmd.Output()
	if err != nil {
		return schemaDump{}, nil, fmt.Errorf("%s --dump-schemas: %w", binary, err)
	}
	var d schemaDump
	if err := json.Unmarshal(raw, &d); err != nil {
		return schemaDump{}, nil, fmt.Errorf("decode schemas from %s: %w", binary, err)
	}
	return d, raw, nil
}

// scrubbedEnv is the parent environment with every GDRIVE_ setting
// removed, so a dump depends on what the gate asked for and nothing
// else.
func scrubbedEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GDRIVE_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// schemaBaselineFile is the tool surface of the newest release, recorded
// in that release's commit by `make schema-baseline`.
const schemaBaselineFile = "testdata/schema-baseline.json"

// schemaDiff compares the binary's tool surface with the baseline, the
// surface of the CHANGELOG's newest release; diffSurface has the rule.
//
// The baseline is a committed file, not a tag. A tag-based diff needs the
// tag in the checkout, rebuilds old source on every run, and cannot let a
// deliberate break through, because the tag always wins.
func schemaDiff(out io.Writer, args []string) error {
	binary := arg(args, 0, "./google-drive-mcp")
	// The whole registrable surface, not a default build's: --dump-schemas
	// emits tools.FullSurface, so a tool behind a feature flag is held
	// like any other.
	dump, raw, err := dumpSchemas(binary)
	if err != nil {
		return err
	}
	if len(dump.Tools) < 20 {
		return fmt.Errorf("%s published %d tools; that is not the surface", binary, len(dump.Tools))
	}
	// CI uploads this, so a reviewer can read the surface a change ships.
	if err := os.WriteFile("schemas.json", raw, 0o644); err != nil { //nolint:gosec // build output, read by anyone
		return fmt.Errorf("write schemas.json: %w", err)
	}
	changelog, baseline, err := readReleaseState()
	if err != nil {
		return err
	}
	return diffSurface(out, raw, baseline, changelog)
}

// readReleaseState reads the CHANGELOG and the baseline. A missing
// baseline is nil, not an error.
func readReleaseState() (changelog string, baseline []byte, err error) {
	raw, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return "", nil, fmt.Errorf("read CHANGELOG.md: %w", err)
	}
	baseline, err = os.ReadFile(schemaBaselineFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", nil, fmt.Errorf("read %s: %w", schemaBaselineFile, err)
	}
	return string(raw), baseline, nil
}

// diffSurface fails on a change from the baseline that breaks a caller:
// a tool or resource removed, an input or output field removed or
// retyped at any depth, or an input newly required. Anything else that
// changed is printed for a person to read.
//
// It also fails when the baseline is not the newest release's. An older
// baseline protects an older surface, so whatever shipped since could be
// dropped and nothing would say. With nothing under [Unreleased] the
// build is that release, so its surface must match the baseline exactly;
// that proves the release commit recorded the baseline rather than
// relabeling an old one.
func diffSurface(out io.Writer, current, baseline []byte, changelog string) error {
	cur, err := readSurface(current)
	if err != nil {
		return fmt.Errorf("the build's surface: %w", err)
	}
	want := newestRelease(changelog)
	if baseline == nil {
		if want != "" {
			return fmt.Errorf("CHANGELOG.md names %s as released, but %s is missing; "+
				"record it in that release's commit with `make schema-baseline VERSION=%s`",
				want, schemaBaselineFile, want)
		}
		// Before the first release there is nothing to be compatible with.
		_, _ = fmt.Fprintf(out, "no baseline yet; %d tools and resources in the current surface\n", len(cur.entries))
		return nil
	}
	base, err := readSurface(baseline)
	if err != nil {
		return fmt.Errorf("parse %s: %w", schemaBaselineFile, err)
	}

	breaking := compareSurfaces(out, base, cur)
	var problems []string
	if want != "" && base.version != want {
		problems = append(problems, fmt.Sprintf("the baseline is the %q surface, but CHANGELOG.md's newest release is %s; "+
			"record it in that release's commit with `make schema-baseline VERSION=%s`",
			base.version, want, want))
	}
	if len(breaking) > 0 {
		problems = append(problems, fmt.Sprintf("the tool surface breaks a caller of %s in %d way(s) above",
			base.version, len(breaking)))
	}
	if len(problems) == 0 && want != "" && countEntries(changelog, "## [Unreleased]") == 0 && !sameSurface(baseline, current) {
		problems = append(problems, fmt.Sprintf("nothing is under [Unreleased], so this build is %s, "+
			"and its surface differs from the baseline. In %s's release commit, run "+
			"`make schema-baseline VERSION=%s`; otherwise, say what changed under [Unreleased]", want, want, want))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// schemaBaseline records the surface of the release being cut as the
// baseline. The release commit runs it, so the baseline lands with the
// CHANGELOG heading that names it. checkBaseline says when it may.
func schemaBaseline(out io.Writer, args []string) error {
	binary := arg(args, 0, "./google-drive-mcp")
	_, raw, err := dumpSchemas(binary)
	if err != nil {
		return err
	}
	changelog, baseline, err := readReleaseState()
	if err != nil {
		return err
	}
	if err := checkBaseline(out, raw, baseline, changelog); err != nil {
		return err
	}
	if err := writeThrough(schemaBaselineFile, raw); err != nil {
		return fmt.Errorf("write %s: %w", schemaBaselineFile, err)
	}
	_, _ = fmt.Fprintf(out, "%s is now the %s surface\n", schemaBaselineFile, newestRelease(changelog))
	return nil
}

// checkBaseline refuses to record a build that is not stamped with the
// release being cut. It compares the build with the current baseline
// first, and refuses a change that breaks a caller unless the release is
// a new major version, which is what a break has to ship as. Overwriting
// first would leave the diff comparing the release with itself.
func checkBaseline(out io.Writer, current, baseline []byte, changelog string) error {
	cur, err := readSurface(current)
	if err != nil {
		return fmt.Errorf("the build's surface: %w", err)
	}
	want := newestRelease(changelog)
	if want == "" {
		return errors.New("CHANGELOG.md names no release yet, so there is no surface to record")
	}
	if cur.version != want {
		return fmt.Errorf("the build is stamped %q, but the release being cut is %s; run `make schema-baseline VERSION=%s`",
			cur.version, want, want)
	}
	if baseline == nil {
		return nil
	}
	base, err := readSurface(baseline)
	if err != nil {
		return fmt.Errorf("parse %s: %w", schemaBaselineFile, err)
	}
	if breaking := compareSurfaces(out, base, cur); len(breaking) > 0 && !newMajor(base.version, want) {
		msg := fmt.Sprintf("%s breaks a caller of %s in %d way(s) above, and is not a new major version; %s is unchanged",
			want, base.version, len(breaking), schemaBaselineFile)
		if base.version == want {
			msg += fmt.Sprintf(". It already holds %s from an earlier run; restore the last release's "+
				"baseline from its tag, then run this again", want)
		}
		return errors.New(msg)
	}
	return nil
}

// newestRelease is the release the baseline must hold: the CHANGELOG's
// first version heading, or "" before the first release.
//
// Between releases that heading is the last tag. In a release commit it
// is the release being cut, and the baseline is recorded in that same
// commit. Recording it after the tag instead would fail every branch
// from the moment the tag is pushed until a second change lands.
func newestRelease(changelog string) string {
	if m := versionHeading.FindStringSubmatch(changelog); m != nil {
		return "v" + m[1]
	}
	return ""
}

// newMajor reports whether release to is a later major version than
// release from. An unreadable version is not.
func newMajor(from, to string) bool {
	major := func(v string) int {
		head, _, _ := strings.Cut(strings.TrimPrefix(v, "v"), ".")
		n, err := strconv.Atoi(head)
		if err != nil {
			return -1
		}
		return n
	}
	f, t := major(from), major(to)
	return f >= 0 && t > f
}

// surface is one schema dump, read for the diff.
type surface struct {
	version string
	// entries is every tool and resource, keyed by tool name or by
	// "resource " and the URI template, with what a person should read
	// when it changes.
	entries map[string]string
	fields  map[string]toolFields
}

func readSurface(data []byte) (surface, error) {
	var dump struct {
		Version string `json:"version"`
		Tools   []struct {
			Name         string          `json:"name"`
			Description  string          `json:"description"`
			InputSchema  json.RawMessage `json:"inputSchema"`
			OutputSchema json.RawMessage `json:"outputSchema"`
			Annotations  json.RawMessage `json:"annotations"`
		} `json:"tools"`
		ResourceTemplates []struct {
			Name        string `json:"name"`
			Title       string `json:"title"`
			URITemplate string `json:"uriTemplate"`
			MIMEType    string `json:"mimeType"`
			Description string `json:"description"`
		} `json:"resourceTemplates"`
	}
	if err := json.Unmarshal(data, &dump); err != nil {
		return surface{}, err
	}
	s := surface{version: dump.Version, entries: map[string]string{}, fields: map[string]toolFields{}}
	for _, t := range dump.Tools {
		s.entries[t.Name] = strings.Join([]string{t.Description, canonical(t.InputSchema), canonical(t.OutputSchema), canonical(t.Annotations)}, "\x00")
		f := toolFields{inputs: map[string]string{}, outputs: map[string]string{}, required: map[string]bool{}}
		if err := walkSchema(t.InputSchema, f.inputs, f.required); err != nil {
			return surface{}, fmt.Errorf("%s input: %w", t.Name, err)
		}
		if err := walkSchema(t.OutputSchema, f.outputs, map[string]bool{}); err != nil {
			return surface{}, fmt.Errorf("%s output: %w", t.Name, err)
		}
		s.fields[t.Name] = f
	}
	// A resource whose URI changed breaks a client exactly as a renamed
	// tool does.
	for _, r := range dump.ResourceTemplates {
		s.entries[resourcePrefix+r.URITemplate] = strings.Join([]string{r.Name, r.Title, r.MIMEType, r.Description}, "\x00")
	}
	return s, nil
}

// canonical is a JSON value with its keys sorted and no spacing, so two
// spellings of one schema compare equal.
func canonical(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(out)
}

// resourcePrefix keeps a resource's key apart from a tool's.
const resourcePrefix = "resource "

// compareSurfaces prints what changed from base to cur and returns what
// breaks a caller.
func compareSurfaces(out io.Writer, base, cur surface) []string {
	var removed, changed, added []string
	for name, prev := range base.entries {
		now, still := cur.entries[name]
		switch {
		case !still:
			removed = append(removed, name)
		case prev != now:
			changed = append(changed, name)
		}
	}
	for name := range cur.entries {
		if _, had := base.entries[name]; !had {
			added = append(added, name)
		}
	}
	sort.Strings(removed)
	sort.Strings(changed)
	sort.Strings(added)

	_, _ = fmt.Fprintf(out, "against %s (%s): %d added, %d changed, %d removed\n",
		schemaBaselineFile, base.version, len(added), len(changed), len(removed))
	for _, n := range added {
		_, _ = fmt.Fprintf(out, "  + %s\n", n)
	}
	for _, n := range changed {
		_, _ = fmt.Fprintf(out, "  ~ %s (description, schema or annotations changed)\n", n)
	}
	for _, n := range removed {
		_, _ = fmt.Fprintf(out, "  - %s  BREAKING\n", n)
	}
	fields := brokenFields(base.fields, cur.fields)
	for _, b := range fields {
		_, _ = fmt.Fprintf(out, "  ! %s  BREAKING\n", b)
	}
	return append(removed, fields...)
}

// sameSurface reports whether two dumps publish the same tools and
// resources, field for field. The version and SDK stamps are not part of
// it, and neither is key order.
func sameSurface(a, b []byte) bool {
	type published struct {
		Tools             any `json:"tools"`
		ResourceTemplates any `json:"resourceTemplates"`
	}
	var x, y published
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// toolFields is what a caller relies on in one tool, at any depth: the
// fields it may send and their types, the ones it must send, and the
// ones it reads back and their types.
//
// A path names a field the way a caller reaches it: `exposure.before`,
// `files[].id` for a field of each element of a list, and
// `restrictions{}` for the values of a map.
type toolFields struct {
	inputs, outputs map[string]string
	required        map[string]bool
}

// schemaNode is the part of a JSON Schema the diff walks.
type schemaNode struct {
	Type                 json.RawMessage        `json:"type"`
	Properties           map[string]*schemaNode `json:"properties"`
	Items                *schemaNode            `json:"items"`
	AdditionalProperties json.RawMessage        `json:"additionalProperties"`
	Required             []string               `json:"required"`
	// The walk cannot see through these, so a schema that uses one is
	// refused rather than compared as if it had no fields.
	Ref   string            `json:"$ref"`
	AnyOf []json.RawMessage `json:"anyOf"`
	OneOf []json.RawMessage `json:"oneOf"`
	AllOf []json.RawMessage `json:"allOf"`
}

// walkSchema records the type of every field in a raw schema, and which
// are required. A tool with no output schema has no output fields.
func walkSchema(raw json.RawMessage, types map[string]string, required map[string]bool) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var n schemaNode
	if err := json.Unmarshal(raw, &n); err != nil {
		return err
	}
	return n.walk("", types, required)
}

func (n *schemaNode) walk(prefix string, types map[string]string, required map[string]bool) error {
	if n.Ref != "" || n.AnyOf != nil || n.OneOf != nil || n.AllOf != nil {
		where := prefix
		if where == "" {
			where = "the top level"
		}
		return fmt.Errorf("%s uses $ref, anyOf, oneOf or allOf, which this diff cannot compare", where)
	}
	for _, r := range n.Required {
		required[fieldPath(prefix, r)] = true
	}
	for name, child := range n.Properties {
		path := fieldPath(prefix, name)
		types[path] = child.typeName()
		if err := child.walk(path, types, required); err != nil {
			return err
		}
	}
	if n.Items != nil {
		types[prefix+"[]"] = n.Items.typeName()
		if err := n.Items.walk(prefix+"[]", types, required); err != nil {
			return err
		}
	}
	// additionalProperties is false on a closed object, and a schema on
	// a map, where it types every value.
	if bytes.HasPrefix(bytes.TrimSpace(n.AdditionalProperties), []byte("{")) {
		var values schemaNode
		if err := json.Unmarshal(n.AdditionalProperties, &values); err != nil {
			return err
		}
		types[prefix+"{}"] = values.typeName()
		if err := values.walk(prefix+"{}", types, required); err != nil {
			return err
		}
	}
	return nil
}

// typeName is the type as the schema spells it, so `"string"` and
// `["null","string"]` differ: a field that may now be null breaks a
// caller that read it as always there.
func (n *schemaNode) typeName() string {
	var buf bytes.Buffer
	if json.Compact(&buf, n.Type) != nil {
		return string(n.Type)
	}
	return buf.String()
}

func fieldPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// parentPath is the field a path sits in, or "" at the top.
func parentPath(path string) string {
	for _, suffix := range []string{"[]", "{}"} {
		if strings.HasSuffix(path, suffix) {
			return strings.TrimSuffix(path, suffix)
		}
	}
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[:i]
	}
	return ""
}

// brokenFields lists what a tool kept by name lost: an input or output
// field removed or retyped, or an input newly required. A field inside
// one that was removed is not listed again. A removed tool is the
// caller's to report.
func brokenFields(prev, cur map[string]toolFields) []string {
	var out []string
	for _, name := range sortedKeys(prev) {
		now, kept := cur[name]
		if !kept {
			continue
		}
		was := prev[name]
		for _, side := range []struct {
			what     string
			was, now map[string]string
		}{{"input", was.inputs, now.inputs}, {"output", was.outputs, now.outputs}} {
			for _, f := range sortedKeys(side.was) {
				t, still := side.now[f]
				parent := parentPath(f)
				_, parentKept := side.now[parent]
				switch {
				case !still && (parent == "" || parentKept):
					out = append(out, fmt.Sprintf("%s: %s field %s removed", name, side.what, f))
				case still && t != side.was[f]:
					out = append(out, fmt.Sprintf("%s: %s field %s changed type from %s to %s", name, side.what, f, side.was[f], t))
				}
			}
		}
		// A required field is new to a caller only where its parent was
		// already there: inside an object that is itself new and
		// optional, a caller who does not send the object is unaffected.
		for _, f := range sortedKeys(now.required) {
			parent := parentPath(f)
			_, parentWas := was.inputs[parent]
			if !was.required[f] && (parent == "" || parentWas) {
				out = append(out, fmt.Sprintf("%s: input field %s newly required", name, f))
			}
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// writeThrough writes path through a temporary file beside it and a
// rename, so a failed write leaves whatever was there whole.
func writeThrough(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil { //nolint:gosec // a committed file, read by everyone
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// lastTag returns the most recent tag, or "" when there is none. Having
// no tags is the normal state before the first release, so it is not an
// error and this cannot fail. The staleness gate reads it.
func lastTag() string {
	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
