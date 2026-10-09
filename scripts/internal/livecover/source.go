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
	"maps"
	"os"
	"path/filepath"
	"slices"
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
	steps, err := ReadSteps(dir, known)
	if err != nil {
		return nil, err
	}
	sent := map[string]map[string]bool{}
	for tool, options := range steps {
		sent[tool] = map[string]bool{}
		for option := range options {
			sent[tool][option] = true
		}
	}
	return sent, nil
}

// A Gate is the driver flags one step waits for, by name and in order:
// a step inside `if o.labels { ... }`, after `if !w.labels { return }`,
// or in a function every caller of which waits for -labels, waits for
// it. A step no flag gates has an empty Gate.
type Gate []string

// Steps is, per tool and option, the gate of each step that sends it.
// It is what lets a run say which flag it lacked for each option it did
// not send, read from the driver rather than from a list kept by hand.
type Steps map[string]map[string][]Gate

// ReadSteps reads a driver's source as FromSource does, and keeps the
// gate of every step. The flags are the ones the driver declares with
// the flag package; a condition names one as a field, `o.labels` or
// `w.share != ""`.
func ReadSteps(dir string, known map[string][]string) (Steps, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
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
	flags := flagNames(files)
	r := &reader{known: known, naming: toolNamingFuncs(files), flags: flags,
		within: callerGates(files, flags), steps: Steps{}}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			r.readFunction(fn)
		}
	}
	return r.steps, nil
}

// reader holds what reading one function needs from the whole driver.
type reader struct {
	known  map[string][]string
	naming map[string]string
	flags  map[string]bool
	// within is, per function, the flags every call of it waits for.
	within map[string]map[string]bool
	steps  Steps
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
func (r *reader) readFunction(fn *ast.FuncDecl) {
	built := mapsIn(fn)
	assigned := r.naming[fn.Name.Name]
	guards := guardsIn(fn.Body, r.flags)
	// at is a step's gate: what every call of the function waits for,
	// what the call waits for inside it, and what the line that wrote
	// the option waits for, which is narrower when a key is added to a
	// map under a condition of its own.
	at := func(call token.Pos, key keyAt) Gate {
		set := map[string]bool{}
		for f := range r.within[fn.Name.Name] {
			set[f] = true
		}
		for _, pos := range []token.Pos{call, key.pos} {
			for f := range guards.at(pos) {
				set[f] = true
			}
		}
		return Sorted(set)
	}
	record := func(tool string, call token.Pos, keys []keyAt) {
		if _, ok := r.known[tool]; !ok || len(keys) == 0 {
			return
		}
		if r.steps[tool] == nil {
			r.steps[tool] = map[string][]Gate{}
		}
		for _, k := range keys {
			gate := at(call, k)
			if !slices.ContainsFunc(r.steps[tool][k.name], func(g Gate) bool { return slices.Equal(g, gate) }) {
				r.steps[tool][k.name] = append(r.steps[tool][k.name], gate)
			}
		}
	}

	ast.Inspect(fn, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CompositeLit:
			tool, args := callLiteral(v)
			if tool != "" {
				record(tool, v.Pos(), keysOf(args, built))
			}
		case *ast.CallExpr:
			// A helper that names the tool for its caller: the caller
			// passes arguments and never writes the tool's name.
			if named := namedTool(v, r.naming); named != "" {
				for _, arg := range v.Args {
					if lit, ok := arg.(*ast.CompositeLit); ok {
						if _, args := callLiteral(lit); args != nil {
							record(named, v.Pos(), keysOf(args, built))
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
				record(tool, v.Pos(), keysOf(arg, built))
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
		record(assigned, fn.Body.Pos(), built[argsField])
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

// keyAt is an option name and where it was written.
type keyAt struct {
	name string
	pos  token.Pos
}

// mapsIn collects every `map[string]any` a function builds, by variable
// name, including the keys assigned into it afterwards.
func mapsIn(fn *ast.FuncDecl) map[string][]keyAt {
	out := map[string][]keyAt{}
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
					out[target.Name] = append(out[target.Name], keyAt{key, as.Pos()})
				case *ast.SelectorExpr:
					out[target.Sel.Name] = append(out[target.Sel.Name], keyAt{key, as.Pos()})
				}
			}
		}
		return true
	})
	return out
}

// keysOf reads the option names an argument expression carries: a map
// literal written at the call, or a variable the function built.
func keysOf(e ast.Expr, maps map[string][]keyAt) []keyAt {
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

func literalKeys(lit *ast.CompositeLit) []keyAt {
	var out []keyAt
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := stringLit(kv.Key); ok {
			out = append(out, keyAt{key, kv.Pos()})
		}
	}
	return out
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

// flagNames is every flag the driver declares, by the name a person
// passes: flag.Bool("labels", ...) declares -labels.
func flagNames(files []*ast.File) map[string]bool {
	out := map[string]bool{}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "flag" {
				return true
			}
			if name, ok := stringLit(call.Args[0]); ok {
				out[name] = true
			}
			return true
		})
	}
	return out
}

// guards are the stretches of one function that wait for a flag.
type guards []struct {
	from, to token.Pos
	flag     string
}

// at is the flags the code at pos waits for.
func (g guards) at(pos token.Pos) map[string]bool {
	out := map[string]bool{}
	for _, s := range g {
		if pos >= s.from && pos < s.to {
			out[s.flag] = true
		}
	}
	return out
}

// guardsIn finds the two shapes that make code wait for a flag: the body
// of `if o.labels && ... { ... }`, and the rest of a block after
// `if !w.labels || ... { return }`.
func guardsIn(body *ast.BlockStmt, flags map[string]bool) guards {
	var out guards
	add := func(from, to token.Pos, flag string) {
		out = append(out, struct {
			from, to token.Pos
			flag     string
		}{from, to, flag})
	}
	afterReturn := func(stmts []ast.Stmt, end token.Pos) {
		for _, st := range stmts {
			is, ok := st.(*ast.IfStmt)
			if !ok || is.Else != nil || len(is.Body.List) == 0 {
				continue
			}
			if _, returns := is.Body.List[len(is.Body.List)-1].(*ast.ReturnStmt); !returns {
				continue
			}
			// What follows runs only when every alternative was false.
			for _, alt := range operands(is.Cond, token.LOR) {
				if flag, given := flagTest(alt, flags); flag != "" && !given {
					add(is.End(), end, flag)
				}
			}
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.IfStmt:
			for _, part := range operands(v.Cond, token.LAND) {
				if flag, given := flagTest(part, flags); flag != "" && given {
					add(v.Body.Pos(), v.Body.End(), flag)
				}
			}
		case *ast.BlockStmt:
			afterReturn(v.List, v.End())
		case *ast.CaseClause:
			afterReturn(v.Body, v.End())
		case *ast.CommClause:
			afterReturn(v.Body, v.End())
		}
		return true
	})
	return out
}

// operands splits a chain of && or || into its parts.
func operands(e ast.Expr, op token.Token) []ast.Expr {
	e = ast.Unparen(e)
	if b, ok := e.(*ast.BinaryExpr); ok && b.Op == op {
		return append(operands(b.X, op), operands(b.Y, op)...)
	}
	return []ast.Expr{e}
}

// flagTest reads one condition as a test of a flag: `o.labels` and
// `o.share != ""` are true when it was given, `!o.labels` and
// `o.share == ""` when it was not. Anything else names no flag.
func flagTest(e ast.Expr, flags map[string]bool) (flag string, given bool) {
	named := func(e ast.Expr) string {
		if sel, ok := ast.Unparen(e).(*ast.SelectorExpr); ok && flags[sel.Sel.Name] {
			return sel.Sel.Name
		}
		return ""
	}
	e = ast.Unparen(e)
	if f := named(e); f != "" {
		return f, true
	}
	switch v := e.(type) {
	case *ast.UnaryExpr:
		if v.Op == token.NOT {
			if f := named(v.X); f != "" {
				return f, false
			}
		}
	case *ast.BinaryExpr:
		if v.Op != token.EQL && v.Op != token.NEQ {
			return "", false
		}
		f, other := named(v.X), v.Y
		if f == "" {
			f, other = named(v.Y), v.X
		}
		if lit, ok := stringLit(other); f != "" && ok && lit == "" {
			return f, v.Op == token.NEQ
		}
	}
	return "", false
}

// callerGates is, per function the driver declares, the flags every
// call of it waits for, through its callers as well: runDestructive is
// called only under `if o.destructive`, so everything it calls waits for
// -destructive too. A function nothing calls, main among them, waits for
// none. Methods are matched by name, which joins two that share one, and
// a call cycle adds nothing to what its own guards say. Both err toward
// reporting a step as one no missing flag explains, which is the loud
// direction.
func callerGates(files []*ast.File, flags map[string]bool) map[string]map[string]bool {
	type site struct {
		caller string
		local  map[string]bool
	}
	declared := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				declared[fn.Name.Name] = true
			}
		}
	}
	sites := map[string][]site{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			g := guardsIn(fn.Body, flags)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var name string
				switch f := call.Fun.(type) {
				case *ast.Ident:
					name = f.Name
				case *ast.SelectorExpr:
					name = f.Sel.Name
				}
				if declared[name] {
					sites[name] = append(sites[name], site{fn.Name.Name, g.at(call.Pos())})
				}
				return true
			})
		}
	}
	// Start from nothing and take in what every caller waits for, until
	// nothing changes. Each round can only add flags, so it ends.
	out := map[string]map[string]bool{}
	for name := range declared {
		out[name] = map[string]bool{}
	}
	for changed := true; changed; {
		changed = false
		for name, ss := range sites {
			var shared map[string]bool
			for _, s := range ss {
				here := maps.Clone(s.local)
				maps.Copy(here, out[s.caller])
				if shared == nil {
					shared = here
					continue
				}
				maps.DeleteFunc(shared, func(f string, _ bool) bool { return !here[f] })
			}
			if !maps.Equal(shared, out[name]) {
				out[name] = shared
				changed = true
			}
		}
	}
	return out
}
