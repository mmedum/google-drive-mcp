// Package livecover measures how much of the tool surface a live run
// actually drives.
//
// A live run is the only check that Drive agrees with this server, so
// what fraction of the surface one exercises is measured rather than
// assumed: a number nobody has is not evidence of a good one.
//
// There are two halves here and they answer different questions.
// FromSource reads the driver's own source and says which options a step
// SENDS — that runs in CI, where there are no credentials. A Recorder
// says which options a run actually sent, which is not the same thing: a
// step can exist in a slice nobody passes to run, or behind a condition
// that was false, and the source cannot tell. A driver with one call
// deleted from its run still reads as fully covered in its source, which
// is why the driver compares the two at the end of every run rather than
// trusting the first.
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

// DriverDir is the live driver's source, from the module root.
const DriverDir = "scripts/livedrive"

// StepTypes are the driver's step types, each with a name field: a tool
// call, and a read of a resource.
var StepTypes = []string{"call", "resourceRead"}

// A Step is one literal of a step type in the driver's source: a step,
// or the call a helper builds for the step its caller named.
type Step struct {
	Pos token.Pos
	// Name is the step's name. It is empty when the literal passes on its
	// caller's step name, and when Problem says why it has none.
	Name string
	// Problem says why the literal's name is refused: it is neither a
	// string literal nor a `call` parameter's name.
	Problem string
	// Tool is the tool a call names as a string literal, and Args the
	// value of its args field.
	Tool string
	Args ast.Expr
	// Func is the function the literal is written in, nil outside one,
	// and Callee the function it is passed to directly, by name.
	Func   *ast.FuncDecl
	Callee string
}

// StepLiterals is every step literal in files, both types in one pass:
// each literal of a step type, and each entry of a slice of one, written
// with its type left out as a table of steps is.
//
// A step's name is a string literal, so the name the transcript prints
// is the one a reader of the source finds. The one other name a literal
// may carry is p.name, where p is a `call` parameter of the function it
// is written in: a helper building the call for a step its caller named.
func StepLiterals(files []*ast.File) []Step {
	var out []Step
	for _, file := range files {
		var stack []ast.Node
		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			if lit, ok := n.(*ast.CompositeLit); ok {
				switch {
				case slices.Contains(StepTypes, identName(lit.Type)):
					out = append(out, newStep(lit, stack))
				case slices.Contains(StepTypes, sliceOf(lit.Type)):
					for _, elt := range lit.Elts {
						if entry, ok := elt.(*ast.CompositeLit); ok && entry.Type == nil {
							out = append(out, newStep(entry, append(slices.Clip(stack), lit)))
						}
					}
				}
			}
			stack = append(stack, n)
			return true
		})
	}
	return out
}

// newStep reads one step literal, given the nodes that enclose it.
func newStep(lit *ast.CompositeLit, enclosing []ast.Node) Step {
	s := Step{Pos: lit.Pos(), Args: keyed(lit, argsField)}
	s.Tool, _ = stringLit(keyed(lit, "tool"))
	if call, ok := enclosing[len(enclosing)-1].(*ast.CallExpr); ok && slices.Contains(call.Args, ast.Expr(lit)) {
		s.Callee = calleeName(call)
	}
	// The innermost function, a closure among them, says which
	// parameters may pass a name on.
	var params []string
	for i := len(enclosing) - 1; i >= 0 && s.Func == nil; i-- {
		var typ *ast.FuncType
		switch fn := enclosing[i].(type) {
		case *ast.FuncLit:
			typ = fn.Type
		case *ast.FuncDecl:
			typ, s.Func = fn.Type, fn
		}
		if typ != nil && params == nil {
			params = callParams(typ)
		}
	}
	switch name := keyed(lit, "name").(type) {
	case nil:
		s.Problem = "a step has no name"
	case *ast.BasicLit:
		var ok bool
		switch s.Name, ok = stringLit(name); {
		case !ok:
			s.Problem = notLiteral
		case s.Name == "":
			s.Problem = "a step's name is empty"
		}
	case *ast.SelectorExpr:
		if x, ok := name.X.(*ast.Ident); !ok || name.Sel.Name != "name" || !slices.Contains(params, x.Name) {
			s.Problem = notLiteral
		}
	default:
		s.Problem = notLiteral
	}
	return s
}

const notLiteral = "a step's name is neither a string literal nor the name of the step a `call` parameter carries"

// callParams is the names of a function's `call` parameters.
func callParams(typ *ast.FuncType) []string {
	names := []string{}
	for _, field := range typ.Params.List {
		if identName(field.Type) == "call" {
			for _, name := range field.Names {
				names = append(names, name.Name)
			}
		}
	}
	return names
}

// keyed is the value a keyed literal gives key, or nil.
func keyed(lit *ast.CompositeLit, key string) ast.Expr {
	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok && identName(kv.Key) == key {
			return kv.Value
		}
	}
	return nil
}

func identName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// sliceOf is the element type's name when e is a slice of a named type.
func sliceOf(e ast.Expr) string {
	if arr, ok := e.(*ast.ArrayType); ok && arr.Len == nil {
		return identName(arr.Elt)
	}
	return ""
}

// calleeName is the name of the function a call calls.
func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

// FromSource reads a driver's source and returns, per tool, the options
// some step passes.
//
// known is the tool surface the server publishes, and it is what keeps
// this honest in the loose direction: a tool name is only read as a tool
// call when it names a tool that exists.
//
// The shapes it reads are the ones this driver uses: a step literal
// carrying tool and args, a map built into a variable and then handed
// over, and a helper that assigns the tool to the `call` its caller
// passed it, which then carries the step's name and its arguments.
// Anything it cannot read is simply not counted, which makes a miss show
// up as an option demanding an excuse it does not need, rather than as
// coverage nobody has.
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
	r := &reader{known: known, flags: flags, within: callerGates(files, flags), steps: Steps{},
		funcs: map[*ast.FuncDecl]funcInfo{}}
	naming := toolNamingFuncs(files)
	for _, s := range StepLiterals(files) {
		// A helper that names the tool for its caller: the caller passes
		// the step's name and arguments and never writes the tool's.
		tool := s.Tool
		if tool == "" {
			tool = naming[s.Callee]
		}
		r.record(s.Func, tool, s.Pos, keysOf(s.Args, r.info(s.Func).built))
	}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || naming[fn.Name.Name] == "" {
				continue
			}
			// Keys added to that call's argument map belong to the tool it
			// was given, wherever in the function they were added.
			//
			// The ARGUMENT map only. Recording every map the function
			// builds is silently wrong for a helper that builds a second
			// map for something else — the keys of that one would be
			// recorded as options of this tool, coverage would go UP, and
			// the rows excusing those options would be deleted as driven.
			// A measurement that fails by growing is the worst kind.
			r.record(fn, naming[fn.Name.Name], fn.Body.Pos(), r.info(fn).built[argsField])
		}
	}
	return r.steps, nil
}

// reader holds what reading one step needs from the whole driver.
type reader struct {
	known map[string][]string
	flags map[string]bool
	// within is, per function, the flags every call of it waits for.
	within map[string]map[string]bool
	funcs  map[*ast.FuncDecl]funcInfo
	steps  Steps
}

// funcInfo is what reading a step needs from the function it is in: the
// maps it builds and the stretches of it that wait for a flag.
type funcInfo struct {
	built  map[string][]keyAt
	guards guards
}

// toolNamingFuncs finds the functions that name a tool for their caller:
// a helper taking a `call` and assigning its tool, so the caller passes
// the step's name and arguments and never writes the tool's.
//
// emptyTrash is the one that has to be. empty_trash without a drive
// empties the whole ACCOUNT's trash, so the tool is named in exactly one
// method and that method always scopes it — a rule the code enforces
// rather than one the next person has to remember, and
// TestEmptyTrashIsNamedInOnePlace holds it. A reader that cannot see
// through that indirection reports its confirm and dry_run as undriven,
// when they are driven, twice each, on every destructive run.
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

// info reads fn once. A map variable lives in its function: `args :=
// map[string]any{...}` followed by `args["parent"] = parent` is one
// statement's worth of meaning split over two, and resolving it anywhere
// wider would join two functions' variables that happen to share a name.
// A step outside any function has neither.
func (r *reader) info(fn *ast.FuncDecl) funcInfo {
	if fn == nil {
		return funcInfo{}
	}
	if i, ok := r.funcs[fn]; ok {
		return i
	}
	i := funcInfo{built: mapsIn(fn), guards: guardsIn(fn.Body, r.flags)}
	r.funcs[fn] = i
	return i
}

// record files the options keys under tool, for a step written at pos in
// fn. A tool the server does not publish is not a tool call.
func (r *reader) record(fn *ast.FuncDecl, tool string, pos token.Pos, keys []keyAt) {
	if _, ok := r.known[tool]; !ok || len(keys) == 0 {
		return
	}
	if r.steps[tool] == nil {
		r.steps[tool] = map[string][]Gate{}
	}
	g := r.info(fn).guards
	set := map[string]bool{}
	if fn != nil {
		maps.Copy(set, r.within[fn.Name.Name])
	}
	for _, k := range keys {
		// A step's gate: what every call of the function waits for, what
		// the step waits for inside it, and what the line that wrote the
		// option waits for, which is narrower when a key is added to a
		// map under a condition of its own.
		gate := maps.Clone(set)
		for _, at := range []token.Pos{pos, k.pos} {
			maps.Copy(gate, g.at(at))
		}
		sorted := Sorted(gate)
		if !slices.ContainsFunc(r.steps[tool][k.name], func(have Gate) bool { return slices.Equal(have, sorted) }) {
			r.steps[tool][k.name] = append(r.steps[tool][k.name], sorted)
		}
	}
}

// argsField is the field a call carries its arguments in, and the name
// mapsIn files a `c.args["k"] = v` assignment under.
const argsField = "args"

// keyAt is an option name and where it was written.
type keyAt struct {
	name string
	pos  token.Pos
}

// mapsIn collects every `map[string]any` a function builds, by variable
// name, including the keys assigned into it afterwards: `x["k"] = v`,
// and `c.args["k"] = v`, filed under args.
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
