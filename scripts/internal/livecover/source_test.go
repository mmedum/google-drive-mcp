package livecover

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// The driver's call shapes, all of them, in one synthetic file. A reader
// that misses one reports an option as undriven and sends somebody to
// write a reason for a gap that is not there.
const driverShapes = `package main

func read() {
	w.call(call{name: "a card is read", tool: "get_file", args: map[string]any{"file": id, "include_labels": true}})
	w.needing(id, call{name: "a file is copied", tool: "copy_file", args: map[string]any{"file": id, "to": folder}})
	w.needing(id, call{name: "an owner is refused", tool: "share_file", args: map[string]any{"file": id, "role": "owner"},
		why: "no transfer_ownership"})
	got, err := w.fetch(id) // a call returning two values, one right-hand side
}

func build(parent string) {
	args := map[string]any{"name": name}
	if parent != "" {
		args["parent"] = parent
	}
	w.createAndKeepID(call{name: "a folder is created", tool: "create_folder", args: args})
}

func scoped(c call) {
	c.tool = "empty_trash"
	c.args["drive"] = d.driveID
	d.call(c)
}

func caller() {
	d.scoped(call{name: "the trash is emptied", args: map[string]any{"dry_run": true, "confirm": true}})
}

func built(c call, id string) {
	d.call(call{name: c.name, tool: "delete_file", args: map[string]any{"file": id, "confirm": true}})
}

func buildsCaller() {
	d.built(call{name: "a file is deleted"}, id)
}

func notATool() {
	w.problem("the scratch folder could not be made", map[string]any{"unrelated": true})
}
`

func TestEveryShapeTheDriverUsesIsRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "driver.go"), []byte(driverShapes), 0o600); err != nil {
		t.Fatal(err)
	}
	known := map[string][]string{
		"get_file":      {"file", "include_labels"},
		"copy_file":     {"file", "to"},
		"share_file":    {"file", "role"},
		"create_folder": {"name", "parent"},
		"empty_trash":   {"confirm", "dry_run", "drive"},
		"delete_file":   {"confirm", "file"},
	}
	sent, err := FromSource(dir, known)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ tool, option string }{
		{"get_file", "file"}, {"get_file", "include_labels"},
		{"copy_file", "file"}, {"copy_file", "to"},
		{"share_file", "file"}, {"share_file", "role"},
		// A map built in a variable, with a key added after the fact.
		{"create_folder", "name"}, {"create_folder", "parent"},
		// The tool named by the helper rather than at the call: the
		// caller passes arguments and never writes "empty_trash".
		{"empty_trash", "drive"}, {"empty_trash", "confirm"}, {"empty_trash", "dry_run"},
		// A helper building the call for the step its caller named.
		{"delete_file", "file"}, {"delete_file", "confirm"},
	} {
		if !sent[c.tool][c.option] {
			t.Errorf("%s.%s was not read as driven", c.tool, c.option)
		}
	}
	// A string literal that is not a tool name must not turn its
	// neighboring map into coverage of something.
	if len(sent) != len(known) {
		t.Errorf("read %d tools, want %d: %v", len(sent), len(known), Sorted(sent))
	}
}

// TestAnIsAnIdentifierNotAnInterface. The alias is resolved by the type
// checker and never by the parser, so a reader looking for
// *ast.InterfaceType sees no argument maps at all — which is not a
// failure, it is a measurement of two options out of a hundred and
// eighty-eight, reported as though somebody had checked.
func TestAnIsAnIdentifierNotAnInterface(t *testing.T) {
	dir := t.TempDir()
	src := "package main\n\nfunc f() {\n\tw.call(call{tool: \"get_file\", args: map[string]any{\"file\": id}})\n" +
		"\tw.call(call{tool: \"trash_file\", args: map[string]interface{}{\"file\": id}})\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "driver.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	sent, err := FromSource(dir, map[string][]string{"get_file": {"file"}, "trash_file": {"file"}})
	if err != nil {
		t.Fatal(err)
	}
	if !sent["get_file"]["file"] {
		t.Error("map[string]any was not read as an argument map")
	}
	if !sent["trash_file"]["file"] {
		t.Error("map[string]interface{} was not read as an argument map")
	}
}

// TestAnEmptyDirectoryIsAnError, because a reader that finds no source
// reports a driver sending nothing, and a gate believing it would demand
// a reason for every option there is.
func TestAnEmptyDirectoryIsAnError(t *testing.T) {
	if _, err := FromSource(t.TempDir(), map[string][]string{"get_file": {"file"}}); err == nil {
		t.Error("a directory with no Go source was read as a driver that sends nothing")
	}
}

// TestOnlyTheArgumentMapBelongsToTheNamedTool. The helper that names a
// tool for its caller reads the arguments out of the `call` it was
// given; every other map in that function belongs to something else.
//
// Recording them all is silently wrong for a helper that builds a
// second map, and wrong in the worst direction: coverage goes UP, and
// the rows excusing those options get deleted as driven.
func TestOnlyTheArgumentMapBelongsToTheNamedTool(t *testing.T) {
	dir := t.TempDir()
	src := `package main

func scoped(c call) {
	c.tool = "empty_trash"
	c.args["drive"] = d.driveID
	unrelated := map[string]any{"not_an_option": true}
	d.note(unrelated)
	d.call(c)
}

func caller() {
	d.scoped(call{args: map[string]any{"confirm": true}})
}
`
	if err := os.WriteFile(filepath.Join(dir, "driver.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	sent, err := FromSource(dir, map[string][]string{"empty_trash": {"confirm", "drive"}})
	if err != nil {
		t.Fatal(err)
	}
	if !sent["empty_trash"]["drive"] || !sent["empty_trash"]["confirm"] {
		t.Errorf("the arguments of the named tool were not read: %v", sent)
	}
	if sent["empty_trash"]["not_an_option"] {
		t.Error("a map the function built for something else was recorded as this tool's options")
	}
}

// TestAStepKnowsTheFlagsItWaitsFor holds the shapes a step can wait for
// a flag in: inside the flag's if, after an early return when it is
// missing, in a function only called under it, and a key added to a map
// under it. A run reads these to say which flag it lacked for an option
// it did not send, rather than a list kept by hand that named the wrong
// flags for half of them.
func TestAStepKnowsTheFlagsItWaitsFor(t *testing.T) {
	dir := t.TempDir()
	src := `package main

func main() {
	labels := flag.Bool("labels", false, "")
	share := flag.String("share", "", "")
	destructive := flag.Bool("destructive", false, "")
	parent := flag.String("parent", "", "")
	run(options{labels: *labels, share: *share, destructive: *destructive, parent: *parent})
}

func run(o options) {
	w.call(call{tool: "get_file", args: map[string]any{"file": id}})
	if o.labels && m.text != "" {
		w.call(call{tool: "get_file", args: map[string]any{"include_labels": true}})
	}
	args := map[string]any{"name": "a folder"}
	if o.parent != "" {
		args["parent"] = o.parent
	}
	w.createAndKeepID(call{name: "a folder is created", tool: "create_folder", args: args})
	if o.destructive {
		destroy()
	}
	w.sharing()
}

func destroy() {
	w.call(call{tool: "empty_trash", args: map[string]any{"drive": id}})
	d.lock()
}

func (d *destroyRun) lock() {
	d.call(call{tool: "manage_approval", args: map[string]any{"lock_file": true}})
}

func (w *writeRun) sharing() {
	if w.share == "" || m.text == "" {
		return
	}
	w.call(call{tool: "share_file", args: map[string]any{"principal": w.share}})
}
`
	if err := os.WriteFile(filepath.Join(dir, "driver.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	steps, err := ReadSteps(dir, map[string][]string{
		"get_file": {"file", "include_labels"}, "create_folder": {"name", "parent"},
		"empty_trash": {"drive"}, "share_file": {"principal"}, "manage_approval": {"lock_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		tool, option string
		want         []Gate
	}{
		{"get_file", "file", []Gate{{}}},
		{"get_file", "include_labels", []Gate{{"labels"}}},
		{"create_folder", "name", []Gate{{}}},
		{"create_folder", "parent", []Gate{{"parent"}}},
		{"empty_trash", "drive", []Gate{{"destructive"}}},
		// Called by a function only called under the flag.
		{"manage_approval", "lock_file", []Gate{{"destructive"}}},
		{"share_file", "principal", []Gate{{"share"}}},
	} {
		if got := steps[c.tool][c.option]; !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s.%s waits for %v, want %v", c.tool, c.option, got, c.want)
		}
	}
}

// stepsIn parses src as one driver file and walks its step literals.
func stepsIn(t *testing.T, src string) []Step {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "driver.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return StepLiterals([]*ast.File{f})
}

// A step's name is a string literal, or the name a helper's own `call`
// parameter carries; any other name is reported rather than skipped.
func TestAStepIsNamedByALiteralOrByItsCaller(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, literal, wantName, wantProblem string }{
		{"a string literal", `call{name: "a card is read"}`, "a card is read", ""},
		{"the helper's own call parameter", `call{name: c.name, tool: "get_file"}`, "", ""},
		{"a variable", `call{name: label}`, "", notLiteral},
		{"two strings joined", `call{name: "a card " + "is read"}`, "", notLiteral},
		{"another value's name", `call{name: r.name}`, "", notLiteral},
		{"no name", `call{tool: "get_file"}`, "", "a step has no name"},
		{"an empty name", `call{name: ""}`, "", "a step's name is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := "package main\n\nfunc helper(c call, r resourceRead, label string) {\n\tw.call(" + tc.literal + ")\n}\n"
			steps := stepsIn(t, src)
			if len(steps) != 1 {
				t.Fatalf("read %d steps, want 1", len(steps))
			}
			if steps[0].Name != tc.wantName || steps[0].Problem != tc.wantProblem {
				t.Errorf("name %q, problem %q; want %q, %q", steps[0].Name, steps[0].Problem, tc.wantName, tc.wantProblem)
			}
		})
	}
}

// Both step types are read in one walk, a table's entries with their
// type left out among them, and a literal of any other type is not.
func TestStepLiteralsReadsBothTypesAndTheirTables(t *testing.T) {
	t.Parallel()
	steps := stepsIn(t, `package main

func run() {
	for _, c := range []call{{name: "a"}, call{name: "b"}} {
		w.call(c)
	}
	for _, r := range []resourceRead{{name: "c"}} {
		w.read(r)
	}
	d.scoped(call{name: "d", tool: "get_file", args: map[string]any{"file": id}})
	_ = struct{ name string }{name: "not a step"}
}
`)
	var names []string
	for _, s := range steps {
		names = append(names, s.Name)
	}
	if want := []string{"a", "b", "c", "d"}; !slices.Equal(names, want) {
		t.Errorf("names %q, want %q", names, want)
	}
	last := steps[len(steps)-1]
	if last.Callee != "scoped" || last.Tool != "get_file" || last.Args == nil || last.Func == nil || last.Func.Name.Name != "run" {
		t.Errorf("step d: callee %q, tool %q, args %v, func %v", last.Callee, last.Tool, last.Args, last.Func)
	}
}
