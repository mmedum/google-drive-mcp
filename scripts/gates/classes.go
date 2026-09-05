package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// classes checks the error vocabulary against itself.
//
// Every failure this server reports carries a class, and a model branches
// on it: "[ambiguous]" means pick between candidates, "[ambiguous_outcome]"
// means go and find out what happened, and confusing the two is how a
// duplicate write reads as a bad argument. The list of classes lives in
// gapi.Classes(), and until this gate existed nothing held the code to
// it — a class could be invented at a call site and nobody would notice,
// and a class could be listed that nothing emits.
//
// It fails three ways, and the third is the one that matters most: a
// scan that read too few files would otherwise report a clean tree for
// ever.
func classes(out io.Writer, _ []string) error {
	declared, err := declaredClasses()
	if err != nil {
		return err
	}
	emitted, files, err := emittedClasses()
	if err != nil {
		return err
	}
	if files < 10 {
		return fmt.Errorf("only %d Go files under internal/ were read; this check is not looking at the "+
			"code it is meant to", files)
	}

	var problems []string
	for _, name := range sorted(emitted) {
		if !declared[name] {
			problems = append(problems, fmt.Sprintf("the class %q is emitted at %s but is not in gapi.Classes()",
				name, emitted[name]))
		}
	}
	for _, name := range sorted(declared) {
		if _, ok := emitted[name]; !ok {
			problems = append(problems, fmt.Sprintf("gapi.Classes() lists %q and nothing emits it: either "+
				"something stopped using it or it was never real", name))
		}
	}
	if len(problems) > 0 {
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, p)
		}
		return fmt.Errorf("%d error-class problem(s)", len(problems))
	}
	_, _ = fmt.Fprintf(out, "class check ok (%d classes, %d files)\n", len(declared), files)
	return nil
}

// declaredClasses reads the Class* constants in internal/gapi/errors.go,
// which are what gapi.Classes() returns. They are read from the source
// rather than imported so that this gate keeps working when the package
// does not compile, which is exactly when a class was just renamed.
func declaredClasses() (map[string]bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("internal", "gapi", "errors.go"), nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse internal/gapi/errors.go: %w", err)
	}
	out := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if !strings.HasPrefix(name.Name, "Class") || i >= len(spec.Values) {
				continue
			}
			if value, ok := stringLiteral(spec.Values[i]); ok {
				out[value] = true
			}
		}
		return true
	})
	if len(out) == 0 {
		return nil, fmt.Errorf("found no Class constants in internal/gapi/errors.go; has the vocabulary moved?")
	}
	return out, nil
}

// emittedClasses finds every class the code actually produces.
//
// It counts a Class* constant used as a VALUE anywhere under internal/,
// which catches all three shapes this code uses: the first argument of
// Errorf, the Class field of an Error literal, and a bare `return
// ClassServer` from the function that maps Google's sentinels onto the
// vocabulary. An earlier version of this gate looked only at the first
// two and reported "server" as declared-but-never-emitted, which was
// wrong and is the reason the rule is now "used as a value" rather than
// a list of call shapes.
//
// Two things are deliberately not counted. A declaration is not a use,
// so neither the constants themselves nor internal/service's aliases for
// them register; and the body of Classes() is skipped, because it names
// every class by definition and counting it would make the
// nothing-emits-this check vacuous.
func emittedClasses() (map[string]string, int, error) {
	out := map[string]string{}
	fset := token.NewFileSet()
	read := 0
	err := filepath.WalkDir("internal", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}
		read++
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "Classes" {
				continue
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.ValueSpec:
					// A declaration is not a use.
					return false
				case *ast.Ident:
					record(out, v.Name, path)
				case *ast.SelectorExpr:
					record(out, v.Sel.Name, path)
				}
				return true
			})
		}
		return nil
	})
	return out, read, err
}

// record notes a class named by a constant, the first time it is seen,
// so a failure names a file rather than only a word.
func record(out map[string]string, constant, path string) {
	value := classValue(constant)
	if value == "" {
		return
	}
	if _, seen := out[value]; !seen {
		out[value] = path
	}
}

// classValue maps a Class* constant's name to the string it holds. The
// mapping is by convention — ClassNotFound is "not_found" — and it is
// checked against the declared list by the caller, so a convention that
// stops holding shows up as an unknown class rather than as silence.
func classValue(constant string) string {
	rest, ok := strings.CutPrefix(constant, "Class")
	if !ok || rest == "" {
		return ""
	}
	var b strings.Builder
	for i, r := range rest {
		if r >= 'A' && r <= 'Z' && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(r | 0x20)
	}
	// ClassAmbiguousIO holds "ambiguous_outcome", which the convention
	// cannot derive: the constant is named after the kind of failure and
	// the value after what the caller should do about it.
	if constant == "ClassAmbiguousIO" {
		return "ambiguous_outcome"
	}
	return b.String()
}

func stringLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	return v, err == nil
}

func sorted[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
