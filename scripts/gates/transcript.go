package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

	unlisted, err := unlistedDrivers()
	if err != nil {
		return err
	}
	problems = append(problems, unlisted...)

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

// transcriptPackages are the packages that may not reach a terminal.
//
// The list is checked rather than merely kept: unlistedDrivers below
// fails when a program under scripts/ imports the redactor and is not
// here, which is what a third driver would do on its first day. "The day
// a third one is written it belongs on this line" was the original
// defence of the list, and it is the argument this same file rejects two
// paragraphs earlier: an allowlist is how a gate stops being believed.
var transcriptPackages = []string{
	filepath.Join("scripts", "livedrive"),
	filepath.Join("scripts", "evals"),
	// Not a program: the stdio session both drivers talk through. It
	// prints nothing today, and it is on the path of every line they do
	// print, so a debugging Println added there would leak past every
	// check in this file.
	filepath.Join("scripts", "internal", "mcpstdio"),
}

// transcriptPackage is the exemption: the one place a line may reach a
// terminal, because it is the one place that redacts first.
var transcriptPackage = filepath.Join("scripts", "internal", "transcript")

// terminalWrite is one way out of a package that should have none.
type terminalWrite struct {
	line int
	what string
}

// terminalWrites finds every way a file can reach a terminal.
//
// Two rules, and the second exists because the first was not enough. The
// first enumerates the call shapes: fmt.Print, Printf and Println write
// to os.Stdout without naming it; os.Stdout and os.Stderr name it,
// however they are then used — handed to fmt.Fprintln, wrapped in a
// bufio.Writer, or written to directly; and the builtins print and
// println go to stderr, which is easy to forget is a terminal at all.
//
// The second rule is an IMPORT, and it is here because a review of this
// gate found the hole by trying it: `log.Printf("owner: %s", addr)` in
// the live driver passed, unredacted, straight to stderr. log and
// log/slog are writes to a terminal with the name left out, exactly as
// fmt.Print is, and enumerating their calls would be the same list
// again — log has Print, Printf, Println, Fatal, Fatalf, Fatalln, Panic,
// Panicf, Panicln, and a Logger with all of them. A package that may not
// reach a terminal has no use for either, so the rule is that it may not
// import them. That is one fact about what a driver is allowed to
// reach, rather than a list of ways to reach it — and the list is what
// the hole got through.
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
	for _, imp := range file.Imports {
		name, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if name == "log" || name == "log/slog" {
			note(imp.Pos(), "the "+name+" package")
		}
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
			case pkg.Name == "os" && (v.Sel.Name == "Stdout" || v.Sel.Name == "Stderr"),
				pkg.Name == "fmt" && (v.Sel.Name == "Print" || v.Sel.Name == "Printf" ||
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
	// Exactly one, not merely all of them. A second write site added to
	// this package would have to be checked by hand, and the package's
	// whole claim is that there is ONE place a line reaches a terminal.
	return writes == 1 && redacting == 1, nil
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

// unlistedDrivers finds a program that prints Drive's answers and is not
// covered by this gate.
//
// A list of packages is only as good as the day it was written, and this
// one would go quiet the moment somebody wrote a third driver — the
// worst way for a check to fail, because the new program is exactly the
// one nobody has thought about yet. What every such program has in
// common is that it needs the redactor: a program that prints what a
// real account answered cannot do its job without one. So importing the
// redactor, or the transcript that wraps it, is the mark, and being
// unlisted with that mark is the failure.
func unlistedDrivers() ([]string, error) {
	var problems []string
	fset := token.NewFileSet()
	err := filepath.WalkDir("scripts", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		dir := filepath.Dir(path)
		if slices.Contains(transcriptPackages, dir) || dir == transcriptPackage {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}
		for _, imp := range file.Imports {
			name, unquoteErr := strconv.Unquote(imp.Path.Value)
			if unquoteErr != nil {
				continue
			}
			if strings.HasSuffix(name, "/internal/redact") || strings.HasSuffix(name, "/internal/transcript") {
				problems = append(problems, fmt.Sprintf(
					"%s prints what a real account answered — it imports %s — and %s is not one of the "+
						"packages this gate covers. Add it to transcriptPackages",
					dir, name, dir))
				return nil
			}
		}
		return nil
	})
	slices.Sort(problems)
	return slices.Compact(problems), err
}
