package gapi_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoWriteIsLabelledAsARead reads this package's own source and
// checks every request literal's kind against its method.
//
// The kind decides two things: which rate limiter the call takes, and
// how a failure is retried. Both are safe in the direction the method
// makes obvious — a GET is a read — and unsafe in the other: a call site
// that writes and says kindRead takes the read limiter, which is twice
// as fast as the write one, and inherits the read's willingness to
// repeat a request after a network failure. A write repeated after a
// network failure is a duplicate nobody asked for.
//
// §17a called this out as a comment doing a test's job: files.generateIds
// is a GET labelled kindRead with a paragraph explaining that repeating
// it is safe, and nothing stopped the next call site from writing the
// same label over a POST. A comment is a weaker guarantee than a check,
// and this is the check.
func TestNoWriteIsLabelledAsARead(t *testing.T) {
	fset := token.NewFileSet()
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list internal/gapi: %v", err)
	}
	var files []*ast.File
	for _, name := range sources {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		t.Fatal("no source files were parsed, so this check has nothing to look at")
	}

	// readMethods are the ones whose kind may be kindRead. Everything
	// else changes something on the other side.
	readMethods := map[string]bool{"http.MethodGet": true, "http.MethodHead": true}

	found := 0
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			name, ok := lit.Type.(*ast.Ident)
			if !ok || name.Name != "request" {
				return true
			}
			method, kind := "", ""
			hasURL := false
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch key.Name {
				case "method":
					method = exprText(kv.Value)
				case "kind":
					kind = exprText(kv.Value)
				case "url":
					hasURL = true
				}
			}
			// A literal with no url is not a call: RepeatableForTest
			// builds one to ask the repeatability rule a question, and it
			// never reaches the network.
			if !hasURL {
				return true
			}
			found++
			where := fset.Position(lit.Pos())
			switch {
			case method == "":
				// A literal with no method is one this check cannot judge,
				// which is itself worth knowing about.
				t.Errorf("%s: a request literal names no method, so its kind cannot be checked", where)
			case kind == "":
				t.Errorf("%s: a request literal names no kind, so it takes the read limiter by default", where)
			case kind == "kindRead" && !readMethods[method]:
				t.Errorf("%s: a %s request is labelled kindRead. It takes the read limiter and inherits a "+
					"read's willingness to repeat itself after a network failure; use kindWrite or kindSharing.",
					where, method)
			case kind != "kindRead" && readMethods[method]:
				// Not unsafe, but it means a read is queueing behind the
				// writes, and it is more likely to be a copied line than
				// a decision.
				t.Errorf("%s: a %s request is labelled %s, which puts a read on a write limiter",
					where, method, kind)
			}
			return true
		})
	}
	// Every call this client makes goes through one of these literals, so
	// a handful would mean the walk found the wrong thing.
	if found < 20 {
		t.Fatalf("found only %d request literals in internal/gapi, so this check is not looking at the "+
			"call sites it is meant to", found)
	}
	t.Logf("checked %d request literals", found)
}

// exprText renders the expressions these literals actually use: a bare
// identifier, or a selector like http.MethodGet.
func exprText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		if x, ok := v.X.(*ast.Ident); ok {
			return x.Name + "." + v.Sel.Name
		}
	}
	return ""
}
