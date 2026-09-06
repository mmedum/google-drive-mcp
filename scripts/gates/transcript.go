package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// transcript holds the two programs that talk to a real Google account
// to one way of printing.
//
// The live driver and the eval harness both run against somebody's real
// Drive and print what comes back: file names, addresses, ids, comment
// text, the signed-in account's own name. All of it goes through a
// redactor — and until this gate, it went through by HABIT. Every line
// happened to call red.Do, which is not at all the same as every line
// having to. Phase 4 found the proof: the driver echoed each call's
// ARGUMENTS unredacted, which was arguably nobody's problem while the
// only address there was one the operator had typed, and became one the
// moment starting an approval put the signed-in account's own address
// into the arguments of every write run. It was fixed line by line, and
// the next print somebody added while debugging would have looked
// exactly like the two beside it that are safe.
//
// The rule with teeth is not "call the redactor". It is that these
// packages cannot reach a terminal at all. A terminal is os.Stdout and
// os.Stderr, and fmt.Print* is a write to os.Stdout with the name left out,
// so naming those three is naming every way out. The transcript package
// is the exemption and the only one, and what it does with what passes
// through it is checked below rather than assumed.
//
// What is deliberately NOT forbidden is fmt.Fprint* to somewhere else: a
// task builds its fixture with fmt.Fprintf into a bytes.Buffer, which
// reaches nobody. A gate that banned the function rather than the
// destination would have made that a special case, and an allowlist is
// how a gate stops being believed.
func transcript(out io.Writer, _ []string) error {
	var problems []string
	read := 0
	for _, dir := range transcriptPackages {
		files, err := goFiles(dir)
		if err != nil {
			return err
		}
		for _, path := range files {
			found, err := terminalWrites(path)
			if err != nil {
				return err
			}
			read++
			for _, w := range found {
				problems = append(problems, fmt.Sprintf("%s:%d: %s reaches the terminal directly; "+
					"print through the transcript package, which redacts", path, w.line, w.what))
			}
		}
	}
	// A scan that read too little reports a clean tree for ever, which
	// is the failure mode a gate cannot notice about itself.
	if read < 5 {
		return fmt.Errorf("only %d Go file(s) were read across %s; this check is not looking at the "+
			"code it is meant to", read, strings.Join(transcriptPackages, " and "))
	}

	redacted, err := exemptionRedacts()
	if err != nil {
		return err
	}
	if !redacted {
		problems = append(problems, transcriptPackage+" is the one package allowed to write to a "+
			"terminal, and nothing in it passes what it writes through the redactor: the exemption "+
			"is hollow and every other check here is worth nothing")
	}
	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, p)
		}
		return fmt.Errorf("%d way(s) around the redactor", len(problems))
	}
	_, _ = fmt.Fprintf(out, "transcript check ok (%d files in %s, one way out)\n",
		read, strings.Join(transcriptPackages, " and "))
	return nil
}

// transcriptPackages are the programs that drive a real account. They
// are named rather than discovered: a program that prints Drive's
// answers is a decision somebody makes, and the day a third one is
// written it belongs on this line.
var transcriptPackages = []string{
	filepath.Join("scripts", "livedrive"),
	filepath.Join("scripts", "evals"),
}

// transcriptPackage is the exemption: the one place a line may reach a
// terminal, because it is the one place that redacts first.
var transcriptPackage = filepath.Join("scripts", "internal", "transcript")

// terminalWrite is one way out of a package that should have none.
type terminalWrite struct {
	line int
	what string
}

// terminalWrites finds every expression in a file that can reach a
// terminal.
//
// Three shapes, and together they are all of them. fmt.Print, Printf and
// Println write to os.Stdout without naming it. os.Stdout and os.Stderr
// name it, however they are then used — handed to fmt.Fprintln, wrapped
// in a bufio.Writer, or written to directly. And the builtins print and
// println go to stderr, which is easy to forget is a terminal at all.
func terminalWrites(path string) ([]terminalWrite, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var found []terminalWrite
	note := func(pos token.Pos, what string) {
		found = append(found, terminalWrite{line: fset.Position(pos).Line, what: what})
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.SelectorExpr:
			pkg, ok := v.X.(*ast.Ident)
			if !ok {
				return true
			}
			name := pkg.Name + "." + v.Sel.Name
			switch {
			case pkg.Name == "os" && (v.Sel.Name == "Stdout" || v.Sel.Name == "Stderr"):
				note(v.Pos(), name)
			case pkg.Name == "fmt" && (v.Sel.Name == "Print" || v.Sel.Name == "Printf" ||
				v.Sel.Name == "Println"):
				note(v.Pos(), name)
			}
		case *ast.CallExpr:
			if id, ok := v.Fun.(*ast.Ident); ok && (id.Name == "print" || id.Name == "println") {
				note(v.Pos(), "the builtin "+id.Name)
			}
		}
		return true
	})
	return found, nil
}

// exemptionRedacts checks that the one package allowed to write to a
// terminal redacts what it writes.
//
// Without this the gate would be a rule about WHERE printing happens and
// nothing about what is printed: moving every fmt.Println into a
// passthrough helper would satisfy every other check here and leak
// exactly as much. So each write in the exempt package must have a call
// to the redactor somewhere in its arguments. That is a shallow check of
// a small file, and it is honest about being one — the behaviour is held
// by TestEveryLinePrintedIsRedacted, which asserts what comes out.
func exemptionRedacts() (bool, error) {
	files, err := goFiles(transcriptPackage)
	if err != nil {
		return false, err
	}
	fset := token.NewFileSet()
	writes, redacting := 0, 0
	for _, path := range files {
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return false, fmt.Errorf("parse %s: %w", path, parseErr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "fmt" || !strings.HasPrefix(sel.Sel.Name, "Fprint") {
				return true
			}
			writes++
			for _, arg := range call.Args {
				if callsRedactor(arg) {
					redacting++
					break
				}
			}
			return true
		})
	}
	return writes > 0 && writes == redacting, nil
}

// callsRedactor reports whether an expression passes its value through
// the redactor. The redactor's method is Do, and it is the only Do in
// this tree, which is what makes the name enough.
func callsRedactor(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Do" {
			found = true
		}
		return !found
	})
	return found
}

// goFiles lists the Go source of a package, tests excluded: a test may
// print whatever it likes, and t.Log is not a terminal a stranger reads.
func goFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no Go source in %s; has the package moved?", dir)
	}
	return out, nil
}
