package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"slices"
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

// diffSurface fails on a change from the baseline that breaks a caller,
// at any depth: a tool or resource removed; a field removed; an input
// that takes fewer types or loses a listed value; an output that may
// return another type, or may be missing where it was required; or an
// input newly required. Anything else that changed is printed for a
// person to read, an output that may carry a value it did not list
// among it.
//
// A break the [Unreleased] part of the CHANGELOG declares is let through:
// a **Breaking:** entry there names its tool, or its resource template,
// in backticks. Only a new major version may ship one, and
// `make schema-baseline` refuses to record a break as anything else, so
// the declaration is the first half of that rule and the release is the
// second.
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
		_, _ = fmt.Fprintf(out, "no baseline yet; %d items in the current surface\n", len(cur.items))
		return nil
	}
	base, err := readSurface(baseline)
	if err != nil {
		return fmt.Errorf("parse %s: %w", schemaBaselineFile, err)
	}

	changes := compareSurfaces(base, cur)
	changes.write(out, againstBaseline(base))
	breaking := changes.breaking
	var problems []string
	if want != "" && base.version != want {
		problems = append(problems, fmt.Sprintf("the baseline is the %q surface, but CHANGELOG.md's newest release is %s; "+
			"record it in that release's commit with `make schema-baseline VERSION=%s`",
			base.version, want, want))
	}
	if undeclared := undeclaredBreaks(breaking, changelog); len(undeclared) > 0 {
		problems = append(problems, fmt.Sprintf("the tool surface breaks a caller of %s in %d way(s) above that "+
			"[Unreleased] does not declare (%s): a break names its tool, in backticks, in a **Breaking:** entry "+
			"there, and ships in a new major version", base.version, len(undeclared), strings.Join(undeclared, "; ")))
	} else if len(breaking) > 0 {
		_, _ = fmt.Fprintf(out, "%d breaking change(s), each declared under **Breaking:** in [Unreleased]; "+
			"`make schema-baseline` records them only as a new major version\n", len(breaking))
	}
	if len(problems) == 0 && want != "" && countEntries(changelog, "## [Unreleased]") == 0 && !changes.empty() {
		problems = append(problems, fmt.Sprintf("nothing is under [Unreleased], so this build is %s, "+
			"and its surface differs from the baseline. In %s's release commit, run "+
			"`make schema-baseline VERSION=%s`; otherwise, say what changed under [Unreleased]", want, want, want))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// undeclaredBreaks is each break whose item, a tool by its name or a
// resource by its URI template, no **Breaking:** line in the CHANGELOG's
// [Unreleased] section names in backticks.
func undeclaredBreaks(breaking []change, changelog string) []string {
	named := map[string]bool{}
	for line := range strings.Lines(sectionFor(changelog, "Unreleased")) {
		if !strings.Contains(line, "**Breaking:**") {
			continue
		}
		for _, span := range codeSpans(line) {
			named[span] = true
		}
	}
	var out []string
	for _, b := range breaking {
		if !named[b.item] {
			out = append(out, b.text)
		}
	}
	return out
}

// codeSpanRe is one code span: text in backticks.
var codeSpanRe = regexp.MustCompile("`([^`]+)`")

// codeSpans is the text of each code span in s.
func codeSpans(s string) []string {
	spans := codeSpanRe.FindAllStringSubmatch(s, -1)
	out := make([]string, 0, len(spans))
	for _, m := range spans {
		out = append(out, m[1])
	}
	return out
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
	changes := compareSurfaces(base, cur)
	changes.write(out, againstBaseline(base))
	if breaking := changes.breaking; len(breaking) > 0 && !newMajor(base.version, want) {
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
	fields  map[string]toolFields
	// items is every item of every list in the dump, keyed by itemKey,
	// and rest is every other key; neither holds the build stamp. A
	// change anywhere in either is a change to what a client sees: a
	// title, an annotation or a _meta mark as much as a schema.
	items map[string]map[string]any
	rest  map[string]any
}

// stampKeys are the dump's build stamp. They change with every build and
// are not part of the surface; what an SDK bump changes on the wire shows
// up inside the tools.
var stampKeys = []string{"server", "version", "sdk"}

func readSurface(data []byte) (surface, error) {
	var dump struct {
		Version string `json:"version"`
		Tools   []struct {
			Name         string          `json:"name"`
			InputSchema  json.RawMessage `json:"inputSchema"`
			OutputSchema json.RawMessage `json:"outputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(data, &dump); err != nil {
		return surface{}, err
	}
	items, rest, err := dumpItems(data)
	if err != nil {
		return surface{}, err
	}
	s := surface{version: dump.Version, fields: map[string]toolFields{}, items: items, rest: rest}
	for _, t := range dump.Tools {
		f := toolFields{inputs: newFieldSet(), outputs: newFieldSet()}
		if err := walkSchema(t.InputSchema, f.inputs); err != nil {
			return surface{}, fmt.Errorf("%s input: %w", t.Name, err)
		}
		if err := walkSchema(t.OutputSchema, f.outputs); err != nil {
			return surface{}, fmt.Errorf("%s output: %w", t.Name, err)
		}
		s.fields[t.Name] = f
	}
	return s, nil
}

// dumpItems reads every item of every list in a dump, keyed by itemKey,
// and every other key but the build stamp. A list with an item that has
// no identity is compared as one value, since a position is no identity.
func dumpItems(data []byte) (map[string]map[string]any, map[string]any, error) {
	var whole map[string]any
	if err := json.Unmarshal(data, &whole); err != nil {
		return nil, nil, err
	}
	for _, k := range stampKeys {
		delete(whole, k)
	}
	items := map[string]map[string]any{}
	rest := map[string]any{}
	for key, v := range whole {
		list, isList := v.([]any)
		keyed := map[string]map[string]any{}
		for _, item := range list {
			obj, _ := item.(map[string]any)
			id := itemKey(key, obj)
			if id == "" {
				keyed = nil
				break
			}
			if _, twice := keyed[id]; twice {
				return nil, nil, fmt.Errorf("the dump names %q twice", id)
			}
			keyed[id] = obj
		}
		if !isList || keyed == nil {
			rest[key] = v
			continue
		}
		for id, obj := range keyed {
			if _, twice := items[id]; twice {
				return nil, nil, fmt.Errorf("the dump names %q twice", id)
			}
			items[id] = obj
		}
	}
	return items, rest, nil
}

// itemKey is how an item of a dumped list is named in the diff: a tool
// by its name, a resource by the template or URI a client asks for, and
// an item of any other list by the list and its name. Empty when the
// item has none. A resource whose template changed breaks a client
// exactly as a renamed tool does.
func itemKey(list string, item map[string]any) string {
	id := ""
	for _, k := range []string{"uriTemplate", "uri", "name"} {
		if v, ok := item[k].(string); ok && v != "" {
			id = v
			break
		}
	}
	switch {
	case id == "":
		return ""
	case list == "tools":
		return id
	case list == "resourceTemplates" || list == "resources":
		return resourcePrefix + id
	}
	return list + " " + id
}

// resourcePrefix keeps a resource's key apart from a tool's.
const resourcePrefix = "resource "

// itemName is what a footer or a **Breaking:** entry names an item by,
// from its key in the surface. A tool's key is its name; every other key
// is a word for its list, a space and the name, as itemKey builds it,
// and a tool's name has no space.
func itemName(key string) string {
	if _, name, ok := strings.Cut(key, " "); ok {
		return name
	}
	return key
}

// againstBaseline is how the diff names the baseline it compares with.
func againstBaseline(base surface) string {
	return fmt.Sprintf("%s (%s)", schemaBaselineFile, base.version)
}

// A change is one difference between two dumps: the item it is to, which
// is what a footer or a **Breaking:** entry names, and how the report
// shows it. The item is a tool's name, a resource's URI template, a list
// item's name, or a top-level key such as instructions. field is, on the
// break of a tool still there, the field it breaks, such as "input file".
type change struct {
	item, text, field string
}

// surfaceChanges is what differs between two surfaces. breaking is what
// breaks a caller; changed is every other difference, a title, an
// annotation or a _meta mark included, named by the part that moved.
// notes are values a kept field lists that a person should look at.
type surfaceChanges struct {
	added, changed, breaking []change
	notes                    []string
}

func (c surfaceChanges) empty() bool {
	return len(c.added) == 0 && len(c.changed) == 0 && len(c.breaking) == 0
}

// compareSurfaces is what changed from base to cur. An item gone breaks
// a caller, and so does a tool still there that broke at any depth
// (brokenFields). A tool that broke lists every other change to it too,
// its schemas' included: the break names only the fields it broke, and
// anything else that moved there is a change a reviewer has to see.
func compareSurfaces(base, cur surface) surfaceChanges {
	var c surfaceChanges
	var removed []change
	for _, key := range sortedUnion(base.items, cur.items) {
		was, inBase := base.items[key]
		now, inCur := cur.items[key]
		name := itemName(key)
		switch {
		case !inCur:
			removed = append(removed, change{item: name, text: key + " removed"})
		case !inBase:
			c.added = append(c.added, change{item: name, text: key})
		default:
			if parts := changedParts(was, now); len(parts) > 0 {
				c.changed = append(c.changed, change{item: name, text: key + " (" + strings.Join(parts, ", ") + ")"})
			}
		}
	}
	for _, part := range changedParts(base.rest, cur.rest) {
		top, _, _ := strings.Cut(part, ".")
		c.changed = append(c.changed, change{item: top, text: part})
	}
	c.breaking = slices.Concat(removed, brokenFields(base.fields, cur.fields))
	c.notes = valueNotes(base.fields, cur.fields)
	return c
}

// write prints the changes to w, under a header naming what they are
// against.
func (c surfaceChanges) write(w io.Writer, against string) {
	_, _ = fmt.Fprintf(w, "against %s: %d added, %d changed, %d breaking\n", against, len(c.added), len(c.changed), len(c.breaking))
	for _, n := range c.added {
		_, _ = fmt.Fprintf(w, "  + %s\n", n.text)
	}
	for _, n := range c.changed {
		_, _ = fmt.Fprintf(w, "  ~ %s\n", n.text)
	}
	for _, n := range c.notes {
		_, _ = fmt.Fprintf(w, "  ~ %s\n", n)
	}
	for _, b := range c.breaking {
		_, _ = fmt.Fprintf(w, "  ! %s  BREAKING\n", b.text)
	}
}

// changedParts names what differs between two dumped objects, one level
// deep, so a flipped hint reads as annotations.destructiveHint. Values
// are compared decoded, so a schema spelled another way, its keys in
// another order, is the same schema.
func changedParts(o, n map[string]any) []string {
	var parts []string
	for _, k := range sortedUnion(o, n) {
		if reflect.DeepEqual(o[k], n[k]) {
			continue
		}
		om, oMap := o[k].(map[string]any)
		nm, nMap := n[k].(map[string]any)
		if !oMap || !nMap {
			parts = append(parts, k)
			continue
		}
		for _, sub := range sortedUnion(om, nm) {
			if !reflect.DeepEqual(om[sub], nm[sub]) {
				parts = append(parts, k+"."+sub)
			}
		}
	}
	return parts
}

// sortedUnion is every key of a and b, sorted.
func sortedUnion[V any](a, b map[string]V) []string {
	keys := slices.AppendSeq(slices.Collect(maps.Keys(a)), maps.Keys(b))
	slices.Sort(keys)
	return slices.Compact(keys)
}

// toolFields is what a caller relies on in one tool, at any depth: the
// fields it may send, and the ones it reads back.
//
// A path names a field the way a caller reaches it: `exposure.before`,
// `files[].id` for a field of each element of a list, and
// `restrictions{}` for the values of a map.
type toolFields struct {
	inputs, outputs fieldSet
}

// fieldSet is one side of a tool: every field by its path, and which of
// them are required.
type fieldSet struct {
	fields   map[string]field
	required map[string]bool
}

func newFieldSet() fieldSet {
	return fieldSet{fields: map[string]field{}, required: map[string]bool{}}
}

// field is one field's type as the schema spells it, "" for no type,
// and the values it is limited to, nil when it is not.
type field struct {
	typ  string
	enum []string
}

// schemaNode is the part of a JSON Schema the diff walks.
type schemaNode struct {
	Type                 json.RawMessage        `json:"type"`
	Enum                 []json.RawMessage      `json:"enum"`
	Properties           map[string]*schemaNode `json:"properties"`
	Items                *schemaNode            `json:"items"`
	AdditionalProperties *schemaNode            `json:"additionalProperties"`
	Required             []string               `json:"required"`
	// The walk cannot see through these, so a schema that uses one is
	// refused rather than compared as if it had no fields.
	Ref   string            `json:"$ref"`
	AnyOf []json.RawMessage `json:"anyOf"`
	OneOf []json.RawMessage `json:"oneOf"`
	AllOf []json.RawMessage `json:"allOf"`
}

// UnmarshalJSON takes a boolean schema too: `true`, which a list or a
// map of any values has for its items, is any value and has no fields to
// walk. Its type reads as the boolean, which typesCover reads as any
// type for `true` and as none for `false`.
func (n *schemaNode) UnmarshalJSON(data []byte) error {
	if b := bytes.TrimSpace(data); string(b) == "true" || string(b) == "false" {
		n.Type = json.RawMessage(b)
		return nil
	}
	type plain schemaNode
	return json.Unmarshal(data, (*plain)(n))
}

// walkSchema records every field in a raw schema, and which are
// required. A tool with no output schema has no output fields.
func walkSchema(raw json.RawMessage, into fieldSet) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var n schemaNode
	if err := json.Unmarshal(raw, &n); err != nil {
		return err
	}
	return n.walk("", into)
}

func (n *schemaNode) walk(prefix string, into fieldSet) error {
	if n.Ref != "" || n.AnyOf != nil || n.OneOf != nil || n.AllOf != nil {
		where := prefix
		if where == "" {
			where = "the top level"
		}
		return fmt.Errorf("%s uses $ref, anyOf, oneOf or allOf, which this diff cannot compare", where)
	}
	for _, r := range n.Required {
		into.required[fieldPath(prefix, r)] = true
	}
	for name, child := range n.Properties {
		path := fieldPath(prefix, name)
		into.fields[path] = child.field()
		if err := child.walk(path, into); err != nil {
			return err
		}
	}
	if n.Items != nil {
		into.fields[prefix+"[]"] = n.Items.field()
		if err := n.Items.walk(prefix+"[]", into); err != nil {
			return err
		}
	}
	// additionalProperties is false on a closed object, and on a map it
	// types every value. A closed object has no values to name.
	if v := n.AdditionalProperties; v != nil && string(v.Type) != "false" {
		into.fields[prefix+"{}"] = v.field()
		if err := v.walk(prefix+"{}", into); err != nil {
			return err
		}
	}
	return nil
}

// field is what the schema says of the field n describes.
func (n *schemaNode) field() field {
	f := field{typ: compactJSON(n.Type)}
	for _, v := range n.Enum {
		f.enum = append(f.enum, compactJSON(v))
	}
	return f
}

// compactJSON is a schema value as the schema spells it, so `"string"`
// and `["null","string"]` differ.
func compactJSON(v json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, v) != nil {
		return string(v)
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

// brokenFields lists, as changes to each tool, what a tool kept by name
// breaks: a field removed, an input that takes fewer types or values, an
// output that may return more types or may now be missing, or an input
// newly required. A field inside one that was removed is not listed
// again. A removed tool is the caller's to report.
func brokenFields(prev, cur map[string]toolFields) []change {
	var out []change
	// broke records that name's field on side broke, in the way the
	// format says, so the text and the field cannot drift apart.
	broke := func(name, side, field, format string, args ...any) {
		out = append(out, change{name, name + ": " + side + " field " + field + " " + fmt.Sprintf(format, args...), side + " " + field})
	}
	for _, name := range sortedKeys(prev) {
		now, kept := cur[name]
		if !kept {
			continue
		}
		was := prev[name]
		for _, side := range []struct {
			what     string
			input    bool
			was, now fieldSet
		}{{"input", true, was.inputs, now.inputs}, {"output", false, was.outputs, now.outputs}} {
			for _, f := range sortedKeys(side.was.fields) {
				w := side.was.fields[f]
				n, still := side.now.fields[f]
				if !still {
					if _, parentKept := side.now.fields[parentPath(f)]; parentPath(f) == "" || parentKept {
						broke(name, side.what, f, "removed")
					}
					continue
				}
				if typeBreaks(w.typ, n.typ, side.input) {
					broke(name, side.what, f, "changed type from %s to %s", typeWord(w.typ), typeWord(n.typ))
				}
				// A caller sent this value, and it is refused now.
				if side.input && n.enum != nil {
					for _, v := range w.enum {
						if !slices.Contains(n.enum, v) {
							broke(name, "input", f, "no longer takes %s", v)
						}
					}
				}
				// A caller read this field as always there.
				if !side.input && side.was.required[f] && !side.now.required[f] {
					broke(name, "output", f, "no longer required")
				}
			}
		}
		// A required field is new to a caller only where its parent was
		// already there: inside an object that is itself new and
		// optional, a caller who does not send the object is unaffected.
		for _, f := range sortedKeys(now.inputs.required) {
			parent := parentPath(f)
			_, parentWas := was.inputs.fields[parent]
			if !was.inputs.required[f] && (parent == "" || parentWas) {
				broke(name, "input", f, "newly required")
			}
		}
	}
	return out
}

// valueNotes lists what a person should look at in the values a kept
// field lists, where a caller may or may not be broken: an output that
// may carry a value it did not, which a caller may not handle, and an
// input newly limited to a list, which breaks a caller only if the
// server took other values before.
func valueNotes(prev, cur map[string]toolFields) []string {
	var out []string
	for _, name := range sortedKeys(prev) {
		now, kept := cur[name]
		if !kept {
			continue
		}
		was := prev[name]
		for _, f := range sortedKeys(was.outputs.fields) {
			w := was.outputs.fields[f]
			n, still := now.outputs.fields[f]
			switch {
			case !still || w.enum == nil:
			case n.enum == nil:
				out = append(out, fmt.Sprintf("%s: output field %s may now be any value", name, f))
			default:
				for _, v := range n.enum {
					if !slices.Contains(w.enum, v) {
						out = append(out, fmt.Sprintf("%s: output field %s may now be %s", name, f, v))
					}
				}
			}
		}
		for _, f := range sortedKeys(was.inputs.fields) {
			if n, still := now.inputs.fields[f]; still && was.inputs.fields[f].enum == nil && n.enum != nil {
				out = append(out, fmt.Sprintf("%s: input field %s now takes only %s", name, f, strings.Join(n.enum, ", ")))
			}
		}
	}
	return out
}

// typeBreaks reports whether a field's type change breaks a caller. An
// input may take more types than it did, and an output may return fewer;
// the other way round, a caller that sent or read the old type is
// broken. So `"string"` to `["null","string"]` breaks an output, where a
// caller read the field as always there, and not an input, which is what
// a bool becoming a *bool produces.
func typeBreaks(was, now string, input bool) bool {
	if was == now {
		return false
	}
	wide, narrow := now, was
	if !input {
		wide, narrow = was, now
	}
	return !typesCover(wide, narrow)
}

// typesCover reports whether every value narrow allows, wide allows too,
// as JSON Schema 2020-12 reads a type: a list allows any type in it, and
// an integer is a number. No type, or the schema `true`, is any value;
// the schema `false` lists no type, so it allows none.
func typesCover(wide, narrow string) bool {
	if anyType(wide) {
		return true
	}
	if anyType(narrow) {
		return false
	}
	allowed := map[string]bool{}
	for _, t := range typeList(wide) {
		allowed[t] = true
	}
	if allowed["number"] {
		allowed["integer"] = true
	}
	for _, t := range typeList(narrow) {
		if !allowed[t] {
			return false
		}
	}
	return true
}

// anyType reports whether a field takes any value: it has no type, or it
// is the boolean schema `true`.
func anyType(typ string) bool {
	return typ == "" || typ == "true"
}

// typeList is a schema's type as a list, whether it was written as one
// name or several. The schema `false` reads as no type.
func typeList(typ string) []string {
	var one string
	if json.Unmarshal([]byte(typ), &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal([]byte(typ), &many)
	return many
}

// typeWord is a type for a message: as the schema spells it, or any.
func typeWord(typ string) string {
	if anyType(typ) {
		return "any"
	}
	return typ
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
