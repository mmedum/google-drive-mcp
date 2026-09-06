package main

import (
	"bytes"
	"fmt"
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

// The three checks below run the GATE rather than the helper under it.
//
// A sibling repository named the pattern that makes this worth doing: a
// partially tested mechanism reads as a tested one. Their `isBinary` had
// a unit test since phase 0 and the branch using it had none, so the
// area looked covered while what the scan actually DID with a binary was
// answerable only by reading — which is how three of us came to describe
// it wrongly. The helper was tested; the wiring was the whole claim.
//
// Each of these was watched failing by hand when it was written, in a
// shell, and that proof went away when the shell did.

// TestTheGateReportsAHollowExemption. exemptionRedacts is tested on its
// own above; this is the branch that acts on it.
func TestTheGateReportsAHollowExemption(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeDriver(t, "scripts/livedrive")
	writePackage(t, "scripts/internal/transcript",
		"package transcript\n\nimport (\n\t\"fmt\"\n\t\"io\"\n)\n\n"+
			"func write(w io.Writer, text string) {\n\t_, _ = fmt.Fprintln(w, text)\n}\n")

	defer restore(transcriptPackages, transcriptPackage)
	transcriptPackages = []string{filepath.Join("scripts", "livedrive")}
	transcriptPackage = filepath.Join("scripts", "internal", "transcript")

	var out bytes.Buffer
	if err := transcript(&out, nil); err == nil {
		t.Fatalf("a transcript package that redacts nothing was accepted:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "hollow") {
		t.Errorf("the report does not name the problem:\n%s", out.String())
	}
}

// TestTheGateReportsAnUnlistedDriver. A program that prints what a real
// account answered, and is covered by nothing, is the worst way for this
// check to go quiet: the new program is the one nobody has thought about.
func TestTheGateReportsAnUnlistedDriver(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeDriver(t, "scripts/livedrive")
	writePackage(t, "scripts/internal/transcript",
		"package transcript\n\nimport (\n\t\"fmt\"\n\t\"io\"\n)\n\n"+
			"func write(w io.Writer, text string, r redactor) {\n\t_, _ = fmt.Fprintln(w, r.Do(text))\n}\n")
	writePackage(t, "scripts/newdriver",
		"package main\n\nimport \"github.com/x/y/scripts/internal/redact\"\n\n"+
			"func main() { _ = redact.NewRedactor(false) }\n")

	defer restore(transcriptPackages, transcriptPackage)
	transcriptPackages = []string{filepath.Join("scripts", "livedrive")}
	transcriptPackage = filepath.Join("scripts", "internal", "transcript")

	var out bytes.Buffer
	if err := transcript(&out, nil); err == nil {
		t.Fatalf("a driver covered by nothing was accepted:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "newdriver") {
		t.Errorf("the report does not name the program:\n%s", out.String())
	}
}

func writePackage(t *testing.T, dir, source string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeDriver is writePackage with enough files to clear the gate's own
// floor. The floor exists because a scan that read too little reports a
// clean tree for ever — and it fires before everything else, so a
// fixture below it makes a test pass for the wrong reason, which is what
// the first draft of these two did.
func writeDriver(t *testing.T, dir string) {
	t.Helper()
	writePackage(t, dir, "package main\n\nfunc run() {}\n")
	for i := range 5 {
		name := filepath.Join(dir, fmt.Sprintf("part%d.go", i))
		body := fmt.Sprintf("package main\n\nfunc part%d() {}\n", i)
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// restore puts the package lists back, so one test's fixture is not the
// next test's subject.
func restore(packages []string, exempt string) {
	transcriptPackages = packages
	transcriptPackage = exempt
}
