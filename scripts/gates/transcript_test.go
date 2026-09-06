package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheDriversHaveOneWayOut runs the gate over the repository itself.
func TestTheDriversHaveOneWayOut(t *testing.T) {
	t.Chdir("../..")
	var out bytes.Buffer
	if err := transcript(&out, nil); err != nil {
		t.Fatalf("a driver can reach the terminal without the redactor: %v\n%s", err, out.String())
	}
}

// TestEveryWayToATerminalIsRefused is the gate watched failing, which is
// the only thing that says what shape it has. Each of these compiles,
// reads like the lines around it, and puts somebody's file name in front
// of whoever is looking at the terminal.
func TestEveryWayToATerminalIsRefused(t *testing.T) {
	cases := []struct {
		name, line, want string
	}{
		{"the obvious one", `fmt.Println("the file is " + name)`, "fmt.Println"},
		{"the formatted one", `fmt.Printf("the file is %s\n", name)`, "fmt.Printf"},
		{"naming the stream instead", `fmt.Fprintln(os.Stdout, name)`, "os.Stdout"},
		{"the other stream", `fmt.Fprintln(os.Stderr, name)`, "os.Stderr"},
		{"not through fmt at all", `os.Stdout.WriteString(name)`, "os.Stdout"},
		{"the builtin nobody thinks of", `println(name)`, "the builtin println"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "leak.go")
			src := "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nvar _ = fmt.Sprint\nvar _ = os.Args\n\nfunc leak(name string) {\n\t" +
				c.line + "\n}\n"
			if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
				t.Fatal(err)
			}
			found, err := terminalWrites(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(found) == 0 {
				t.Fatalf("%s was not seen as a way to the terminal", c.line)
			}
			var what []string
			for _, w := range found {
				what = append(what, w.what)
			}
			if !strings.Contains(strings.Join(what, " "), c.want) {
				t.Errorf("the report says %v, and does not name %s", what, c.want)
			}
		})
	}
}

// TestPrintingSomewhereThatIsNotATerminalIsAllowed. The rule is about
// the destination, not the function: a task builds its fixture with
// fmt.Fprintf into a buffer, and forbidding the function would have made
// that an exception. An allowlist is how a gate stops being believed.
func TestPrintingSomewhereThatIsNotATerminalIsAllowed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fine.go")
	src := "package main\n\nimport (\n\t\"bytes\"\n\t\"fmt\"\n)\n\nfunc build(name string) string {\n" +
		"\tvar b bytes.Buffer\n\tfmt.Fprintf(&b, \"line %s\\n\", name)\n\treturn b.String()\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := terminalWrites(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Errorf("writing to a buffer was refused as a terminal write: %v", found)
	}
}

// TestAHollowExemptionIsRefused. Everything else here is a rule about
// WHERE printing happens; without this it would say nothing about what
// is printed, and moving every fmt.Println into a passthrough helper
// would satisfy the gate and leak exactly as much.
func TestAHollowExemptionIsRefused(t *testing.T) {
	t.Chdir("../..")
	ok, err := exemptionRedacts()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("the exempt package does not redact what it writes")
	}

	// The same question asked of a package that writes straight through.
	dir := t.TempDir()
	path := filepath.Join(dir, "passthrough.go")
	src := "package transcript\n\nimport (\n\t\"fmt\"\n\t\"io\"\n)\n\n" +
		"func write(w io.Writer, text string) {\n\t_, _ = fmt.Fprintln(w, text)\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := terminalWrites(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("a passthrough helper is not itself a terminal write: %v", found)
	}
}
