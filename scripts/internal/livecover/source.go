// Package livecover measures how much of the tool surface a live run
// actually drives.
//
// A live run is the only check that Drive agrees with this server, and
// until now nobody knew what fraction of the surface one exercises. The
// shared standard names this gate for every repository with a live
// driver; the one place it had been measured read 14 of 28 options on
// the day it was written, and a number nobody has is not evidence of a
// good one.
//
// There are two halves here and they answer different questions.
// FromSource reads the driver's own source and says which options a step
// SENDS — that runs in CI, where there are no credentials. A Recorder
// says which options a run actually sent, which is not the same thing: a
// step can exist in a slice nobody passes to run, or behind a condition
// that was false, and the source cannot tell. A sibling repository
// demonstrated exactly that by deleting one call and watching its static
// gate still report full coverage, which is why the driver compares the
// two at the end of every run rather than trusting the first.
package livecover

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// FromSource reads a driver's source and returns, per tool, the options
// some step passes.
//
// known is the tool surface the server publishes, and it is what keeps
// this honest in the loose direction: a call whose first argument is a
// string literal is only read as a tool call when that literal is a tool
// that exists. Without it, every two-argument helper taking a name and a
// map would look like one.
//
// The shapes it reads are the ones this driver uses: a `call` literal
// carrying tool and args, a helper taking the tool name and an argument
// map, and a map built into a variable and then handed over — including
// the one place a tool name is assigned to a field rather than written
// at the call. Anything it cannot read is simply not counted, which
// makes a miss show up as an option demanding an excuse it does not
// need, rather than as coverage nobody has.
func FromSource(dir string, known map[string][]string) (map[string]map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	sent := map[string]map[string]bool{}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if parseErr != nil {
			return nil, fmt.Errorf("parse %s: %w", name, parseErr)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no Go source in %s; has the driver moved?", dir)
	}
	naming := toolNamingFuncs(files)
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			readFunction(fn, known, naming, sent)
		}
	}
	return sent, nil
}

// toolNamingFuncs finds the functions that name a tool for their caller:
// a helper taking a `call` and assigning its tool, so the caller passes
// arguments and never writes the tool's name.
//
// There is one in this driver and it is there on purpose. empty_trash
// without a drive empties the whole ACCOUNT's trash, so the tool is
// named in exactly one method and that method always scopes it — a rule
// the code enforces rather than one the next person has to remember, and
// TestEmptyTrashIsNamedInOnePlace holds it. A reader that could not see
// through that one indirection reported its confirm and dry_run as
// undriven, which would have put two lies in the decision file: those
// options are driven, twice each, on every destructive run.
func toolNamingFuncs(files []*ast.File) map[string]string {
	out := map[string]string{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for i, lhs := range as.Lhs {
					sel, ok := lhs.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "tool" || i >= len(as.Rhs) {
						continue
					}
					if lit, ok := stringLit(as.Rhs[i]); ok {
						out[fn.Name.Name] = lit
					}
				}
				return true
			})
		}
	}
	return out
}

// readFunction collects the pairs one function sends.
//
// It works a function at a time because that is the scope a map variable
// lives in: `args := map[string]any{...}` followed by `args["parent"] =
// parent` is one statement's worth of meaning split over two, and
// resolving it anywhere wider would join two functions' variables that
// happen to share a name.
func readFunction(fn *ast.FuncDecl, known map[string][]string, naming map[string]string,
	sent map[string]map[string]bool) {
	maps := mapsIn(fn)
	assigned := naming[fn.Name.Name]

	ast.Inspect(fn, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CompositeLit:
			tool, args := callLiteral(v)
			if tool != "" {
				record(sent, known, tool, keysOf(args, maps))
			}
		case *ast.CallExpr:
			// A helper that names the tool for its caller: the caller
			// passes arguments and never writes the tool's name.
			if named := namedTool(v, naming); named != "" {
				for _, arg := range v.Args {
					if lit, ok := arg.(*ast.CompositeLit); ok {
						if _, args := callLiteral(lit); args != nil {
							record(sent, known, named, keysOf(args, maps))
						}
					}
				}
			}
			// The first string-literal argument is the tool name in
			// every helper this driver has.
			tool := ""
			for _, arg := range v.Args {
				if lit, ok := stringLit(arg); ok {
					tool = lit
					break
				}
			}
			if tool == "" {
				return true
			}
			for _, arg := range v.Args {
				record(sent, known, tool, keysOf(arg, maps))
			}
		}
		return true
	})

	if assigned != "" {
		// Keys added to that call's argument map belong to the tool it was
		// given, wherever in the function they were added.
		//
		// The ARGUMENT map only. An earlier version recorded every map
		// the function built, which is right for the one function that
		// has this shape today and silently wrong the day it builds a
		// second map for something else — the keys of that one would be
		// recorded as options of this tool, coverage would go UP, and the
		// rows excusing those options would be deleted as driven. A
		// measurement that fails by growing is the worst kind.
		record(sent, known, assigned, maps[argsField])
	}
}

// argsField is the field a call carries its arguments in, and the name
// mapsIn files a `c.args["k"] = v` assignment under.
const argsField = "args"

// callLiteral reads a `call{tool: "x", args: ...}` literal.
func callLiteral(lit *ast.CompositeLit) (string, ast.Expr) {
	tool, args := "", ast.Expr(nil)
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
		case "tool":
			if s, ok := stringLit(kv.Value); ok {
				tool = s
			}
		case argsField:
			args = kv.Value
		}
	}
	return tool, args
}

// mapsIn collects every `map[string]any` a function builds, by variable
// name, including the keys assigned into it afterwards.
func mapsIn(fn *ast.FuncDecl) map[string][]string {
	out := map[string][]string{}
	ast.Inspect(fn, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range as.Lhs {
			if i >= len(as.Rhs) {
				break
			}
			// x := map[string]any{...}
			if id, ok := lhs.(*ast.Ident); ok {
				if lit, ok := as.Rhs[i].(*ast.CompositeLit); ok && isArgMap(lit) {
					out[id.Name] = append(out[id.Name], literalKeys(lit)...)
				}
				continue
			}
			// x["k"] = v, and c.args["k"] = v
			if idx, ok := lhs.(*ast.IndexExpr); ok {
				key, ok := stringLit(idx.Index)
				if !ok {
					continue
				}
				switch target := idx.X.(type) {
				case *ast.Ident:
					out[target.Name] = append(out[target.Name], key)
				case *ast.SelectorExpr:
					out[target.Sel.Name] = append(out[target.Sel.Name], key)
				}
			}
		}
		return true
	})
	return out
}

// keysOf reads the option names an argument expression carries: a map
// literal written at the call, or a variable the function built.
func keysOf(e ast.Expr, maps map[string][]string) []string {
	switch v := e.(type) {
	case *ast.CompositeLit:
		if isArgMap(v) {
			return literalKeys(v)
		}
	case *ast.Ident:
		return maps[v.Name]
	case *ast.SelectorExpr:
		return maps[v.Sel.Name]
	}
	return nil
}

// isArgMap reports whether a literal is a map[string]any, which is the
// only shape a tool's arguments take here.
func isArgMap(lit *ast.CompositeLit) bool {
	m, ok := lit.Type.(*ast.MapType)
	if !ok {
		return false
	}
	key, ok := m.Key.(*ast.Ident)
	if !ok || key.Name != "string" {
		return false
	}
	// `any` is an identifier, not an interface node: the alias is
	// resolved by the type checker and never by the parser, and a first
	// draft that looked only for *ast.InterfaceType read two of a
	// hundred and eighty-eight options and called it a measurement.
	switch value := m.Value.(type) {
	case *ast.Ident:
		return value.Name == "any"
	case *ast.InterfaceType:
		return value.Methods == nil || len(value.Methods.List) == 0
	}
	return false
}

func literalKeys(lit *ast.CompositeLit) []string {
	var out []string
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := stringLit(kv.Key); ok {
			out = append(out, key)
		}
	}
	return out
}

// record notes options against a tool, and only against a tool the
// server actually publishes.
func record(sent map[string]map[string]bool, known map[string][]string, tool string, keys []string) {
	if len(keys) == 0 {
		return
	}
	if _, ok := known[tool]; !ok {
		return
	}
	if sent[tool] == nil {
		sent[tool] = map[string]bool{}
	}
	for _, k := range keys {
		sent[tool][k] = true
	}
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	return v, err == nil
}

// Sorted is the keys of a map, in order, so that a report reads the same
// way twice.
func Sorted[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// namedTool reports the tool a call gets from the function it is calling
// rather than from an argument.
func namedTool(call *ast.CallExpr, naming map[string]string) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return naming[fn.Name]
	case *ast.SelectorExpr:
		return naming[fn.Sel.Name]
	}
	return ""
}
