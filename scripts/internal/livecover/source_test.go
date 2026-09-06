package livecover

import (
	"os"
	"path/filepath"
	"testing"
)

// The driver's call shapes, all of them, in one synthetic file. A reader
// that misses one reports an option as undriven and sends somebody to
// write a reason for a gap that is not there.
const driverShapes = `package main

func read() {
	w.call(call{tool: "get_file", args: map[string]any{"file": id, "include_labels": true}})
	w.needing("copy_file", id, map[string]any{"file": id, "to": folder})
	w.expecting("share_file", id, map[string]any{"file": id, "role": "owner"}, "no transfer_ownership")
}

func build(parent string) {
	args := map[string]any{"name": name}
	if parent != "" {
		args["parent"] = parent
	}
	w.createAndKeepID("create_folder", args)
}

func scoped(c call) {
	c.tool = "empty_trash"
	c.args["drive"] = d.driveID
	d.call(c)
}

func caller() {
	d.scoped(call{args: map[string]any{"dry_run": true, "confirm": true}})
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
	} {
		if !sent[c.tool][c.option] {
			t.Errorf("%s.%s was not read as driven", c.tool, c.option)
		}
	}
	// A string literal that is not a tool name must not turn its
	// neighbouring map into coverage of something.
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
