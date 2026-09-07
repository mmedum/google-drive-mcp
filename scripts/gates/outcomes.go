package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// outcomes holds §11's rule — never assert an outcome the response did
// not carry — to the one shape of it a machine can check.
//
// Phase 5 fixed three violations by hand and every one was found by a
// live run, because nothing here could see them. `lock_file` said "the
// file is LOCKED: nobody can change its content" directly above a card
// showing no restriction, and the next content change succeeded.
// `empty_trash` said "everything that was in it is gone for good", from
// a method the reference gives no response at all, while a file trashed
// seconds earlier survived. Each was written by somebody who knew what
// the call was supposed to do.
//
// The checkable shape is: a caller asks Drive to make something so, and
// the code writes the sentence describing the result inside the branch
// that tested the REQUEST. The response is never consulted; the argument
// is treated as its own evidence.
//
// What it cannot do is judge the WORDS. It flags a shape, and the
// honest form of that shape looks the same to a parser: "Drive was asked
// to bring the comment threads along" tests the same field and states no
// less prose than the sentence it replaced. So a branch that is right
// anyway carries a row in the record with the reason, and the gate's job
// is the one a machine can do — make somebody look at every branch of
// this shape, and refuse a new one that nobody has looked at.
//
// §17a proposed a different rule — fail if the field is read in the same
// function that builds the note — and it is wrong, which reading the
// code says and thinking about it did not. Two correct sites read the
// field exactly there: the lock sentence branches on `in.LockFile` and
// then calls lockWords(after.File), which reads the file back; the
// pinning note branches on `in.KeepPreviousRevision` and then asks Drive
// to pin, and words the outcome from what Drive answered. The function
// is the wrong unit. The BRANCH is the right one, and what makes a
// branch honest is that it consults something before it speaks.
func outcomes(out io.Writer, _ []string) error {
	fset := token.NewFileSet()
	files, err := parseService(fset)
	if err != nil {
		return err
	}
	// A scan that read too little reports a clean package for ever.
	if len(files) < 10 {
		return fmt.Errorf("only %d Go file(s) under internal/service were read; this check is not "+
			"looking at the code it is meant to", len(files))
	}
	fields := boolInputFields(files)
	if len(fields) < 20 {
		return fmt.Errorf("found %d boolean input fields in internal/service; this check is not looking "+
			"at the code it is meant to", len(fields))
	}
	exempt, err := readOutcomeExemptions()
	if err != nil {
		return err
	}

	var problems []string
	used := map[string]int{}
	for _, c := range outcomeClaims(files, fields, fset) {
		key := outcomeKey(c.file, c.field)
		if _, ok := exempt[key]; ok {
			used[key]++
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"%s:%d: this branch tests the request field %s and then states an outcome — %q — without "+
				"asking Drive anything. Read it back, or word it as what was asked for rather than what "+
				"happened, or record it in %s with the reason",
			c.file, c.line, c.field, c.quote, outcomeFile))
	}
	// A stale exemption outlives the code it excused and reads as a
	// decision somebody made about today's code.
	for _, key := range sorted(exempt) {
		switch {
		case used[key] == 0:
			problems = append(problems, fmt.Sprintf(
				"%s excuses %s and nothing there states an outcome from the request any more; delete the row",
				outcomeFile, key))
		case used[key] > 1:
			// One row, more than one branch. A key names a file and a
			// field, so a second branch testing the same boolean in
			// the same file would be excused by an argument written
			// about the first — silently, and it is the new branch
			// nobody has looked at. Refusing the ambiguity is what
			// makes somebody look; the alternative, keying on the line,
			// would make every row stale on the next edit above it.
			problems = append(problems, fmt.Sprintf(
				"%s excuses %s once and %d branches there state an outcome after testing it. One "+
					"argument cannot cover two branches: make them one, or word the second so it "+
					"does not state an outcome",
				outcomeFile, key, used[key]))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		for _, p := range problems {
			_, _ = fmt.Fprintln(out, p)
		}
		return fmt.Errorf("%d outcome(s) asserted from the request", len(problems))
	}
	_, _ = fmt.Fprintf(out, "outcome check ok (%d boolean inputs across %d files, %d branch(es) excused)\n",
		len(fields), len(files), len(exempt))
	return nil
}

// parsedFile is one file of the service package, kept with its path so a
// failure names a place rather than a syntax tree.
type parsedFile struct {
	path string
	file *ast.File
}

// parseService reads the package once. Both passes below need all of it
// — a field declared in one file is tested in another — and parsing it
// twice was how this started.
func parseService(fset *token.FileSet) ([]parsedFile, error) {
	paths, err := goFiles(filepath.Join("internal", "service"))
	if err != nil {
		return nil, err
	}
	out := make([]parsedFile, 0, len(paths))
	for _, path := range paths {
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil, fmt.Errorf("parse %s: %w", path, parseErr)
		}
		out = append(out, parsedFile{path: path, file: file})
	}
	return out, nil
}

const outcomeFile = "testdata/outcome-claims.tsv"

// claim is a branch that tests a request field and then says what
// happened.
type claim struct {
	file  string
	line  int
	field string
	quote string
}

// boolInputFields is every boolean field on a service input struct.
//
// Derived rather than listed, because a list of "fields that ask Drive
// to make something so" is a judgement that decays the day a phase adds
// one, and §17a is right that choosing the set by hand is the hard part.
// A boolean input is exactly the shape: a string carries a value, and a
// boolean asks for a state. Deriving it means a new one is covered by
// the gate on the commit that adds it, with nothing to remember.
func boolInputFields(files []parsedFile) map[string]bool {
	out := map[string]bool{}
	for _, pf := range files {
		ast.Inspect(pf.file, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok || !strings.HasSuffix(spec.Name.Name, "Input") {
				return true
			}
			st, ok := spec.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, f := range st.Fields.List {
				id, ok := f.Type.(*ast.Ident)
				if !ok || id.Name != "bool" {
					continue
				}
				for _, name := range f.Names {
					out[name.Name] = true
				}
			}
			return true
		})
	}
	return out
}

// outcomeClaims finds the branches that state an outcome from a request.
//
// It walks a function at a time so that "the request" can be resolved by
// TYPE rather than guessed: the request is the parameter whose type is
// an Input, and a selector on anything else is not one. An earlier
// version matched any selector whose field name happened to be a
// boolean input's, and needed a hardcoded exception for the service
// receiver to survive at all. It still reported the RENDERER's
// `out.DryRun` — a field of the outcome, tested to decide how to print
// it — as a request being asserted from. A rule that needs one exception
// usually needs two, and the second is the one nobody adds.
func outcomeClaims(files []parsedFile, fields map[string]bool, fset *token.FileSet) []claim {
	var found []claim
	for _, pf := range files {
		for _, decl := range pf.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			requests := inputParams(fn)
			if len(requests) == 0 {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				stmt, ok := n.(*ast.IfStmt)
				if !ok {
					return true
				}
				field := testedField(stmt.Cond, fields, requests)
				if field == "" {
					return true
				}
				if consultsDrive(stmt.Body) || refuses(stmt.Body) || saysItIsADryRun(stmt.Body) {
					return true
				}
				if quote, line := proseIn(stmt.Body, fset); quote != "" {
					found = append(found, claim{file: pf.path, line: line, field: field, quote: quote})
				}
				return true
			})
		}
	}
	return found
}

// inputParams names the parameters that carry the caller's request: the
// ones whose type is an Input, however it is spelled.
func inputParams(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	if fn.Type.Params == nil {
		return out
	}
	for _, p := range fn.Type.Params.List {
		t := p.Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		id, ok := t.(*ast.Ident)
		if !ok || !strings.HasSuffix(id.Name, "Input") {
			continue
		}
		for _, name := range p.Names {
			out[name.Name] = true
		}
	}
	return out
}

// testedField names the request field a condition tests, if any.
func testedField(cond ast.Expr, fields, requests map[string]bool) string {
	name := ""
	ast.Inspect(cond, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || name != "" {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && requests[id.Name] && fields[sel.Sel.Name] {
			name = sel.Sel.Name
			return false
		}
		return true
	})
	return name
}

// consultsDrive reports whether a branch asks Drive anything before it
// speaks. A call on the API client is the only thing that can bring a
// response back, and a branch that made one is wording its sentence from
// an answer rather than from the argument that asked the question.
func consultsDrive(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "api" {
			found = true
		}
		return !found
	})
	return found
}

// proseIn finds a sentence in a branch that REACHES THE CALLER.
//
// It selects rather than subtracts, which §17a recorded as the shape
// this gate should have had from the start. The first version collected
// every long string in the branch and then took away the ones that were
// not outcomes — a refusal, a log line — which is why it needed a list of
// function names AND a length heuristic AND a record file to be right,
// and why every one of those three had to be got right independently.
//
// The question a positive rule asks is the one the gate is actually
// about: does this literal end up in what the caller reads? In this
// package that is two shapes and only two — appended to a `notes` slice
// that ends in an outcome, or assigned into a note field — because
// `internal/render` writes every result there is. Errorf prose and slog
// prose are excluded by construction now rather than by name, and a
// sentence built in a strings.Builder for an error is not a candidate at
// all.
//
// The length heuristic is gone with them. A short note is still a note.
func proseIn(body *ast.BlockStmt, fset *token.FileSet) (string, int) {
	quote, line := "", 0
	ast.Inspect(body, func(n ast.Node) bool {
		if quote != "" {
			return false
		}
		value, pos, ok := noteText(n)
		if !ok {
			return true
		}
		quote, line = short(value), fset.Position(pos).Line
		return false
	})
	return quote, line
}

// noteText reports the words a statement puts in front of the caller.
//
// Two shapes, both of which end in the Note the renderer prints:
//
//	notes = append(notes, "…")   // a slice joined into one note
//	o.Note = "…"                 // or +=, on a field named Note
//
// A literal anywhere else in the branch — an error's words, a log line, a
// map key, a format for a debug print — is not what a caller reads, and
// the first version of this gate had to name each of those to ignore it.
func noteText(n ast.Node) (string, token.Pos, bool) {
	switch v := n.(type) {
	case *ast.AssignStmt:
		for i, lhs := range v.Lhs {
			if i >= len(v.Rhs) || !isNoteTarget(lhs) {
				continue
			}
			if s, pos, ok := literalIn(v.Rhs[i]); ok {
				return s, pos, true
			}
		}
	case *ast.KeyValueExpr:
		// outcome{Note: "…"}, which is the commonest of the three and
		// the one the first draft of this rule missed. A test caught it:
		// the dry-run branch that states an outcome without marking its
		// result went unflagged, which is the exact case the field-name
		// exclusion used to hide.
		if key, ok := v.Key.(*ast.Ident); ok && key.Name == "Note" {
			if s, pos, ok := literalIn(v.Value); ok {
				return s, pos, true
			}
		}
	case *ast.CallExpr:
		id, ok := v.Fun.(*ast.Ident)
		if !ok || id.Name != "append" || len(v.Args) < 2 {
			return "", 0, false
		}
		if !isNoteTarget(v.Args[0]) {
			return "", 0, false
		}
		for _, arg := range v.Args[1:] {
			if s, pos, ok := literalIn(arg); ok {
				return s, pos, true
			}
		}
	}
	return "", 0, false
}

// isNoteTarget reports whether an expression is where a result's words
// are collected: a `notes` slice, or a field called Note.
func isNoteTarget(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name == "note" || v.Name == "notes"
	case *ast.SelectorExpr:
		return v.Sel.Name == "Note" || v.Sel.Name == "note"
	}
	return false
}

// literalIn finds the first string literal an expression carries, so
// that a note built by concatenation is read as the sentence it is.
func literalIn(e ast.Expr) (string, token.Pos, bool) {
	var value string
	var pos token.Pos
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if found {
			return false
		}
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil || strings.TrimSpace(v) == "" {
			return true
		}
		value, pos, found = v, lit.Pos(), true
		return false
	})
	return value, pos, found
}

// isRefusalOrLog reports whether a call produces an error or a log line
// rather than a sentence a caller reads as a result.
func isRefusalOrLog(call *ast.CallExpr) bool {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name == "Errorf" || fn.Name == "New"
	case *ast.SelectorExpr:
		switch fn.Sel.Name {
		case "Errorf", "New", "Error", "Warn", "Info", "Debug", "wrap":
			return true
		}
	}
	return false
}

func short(v string) string {
	v = strings.Join(strings.Fields(v), " ")
	if len(v) > 60 {
		return v[:60] + "…"
	}
	return v
}

// readOutcomeExemptions reads the branches recorded as honest anyway.
//
// A missing file means nothing is excused, which is a legitimate state:
// this gate's record is exceptions only, and a repository with none
// should not have to keep an empty one.
func readOutcomeExemptions() (map[string]string, error) {
	if _, err := os.Stat(outcomeFile); os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	rows, problems := readTSV(outcomeFile, 2, 0)
	out := map[string]string{}
	for _, row := range rows {
		if strings.TrimSpace(row.fields[1]) == "" {
			problems = append(problems, fmt.Sprintf(
				"%s:%d: %s carries no reason, and an exemption without one is not a decision",
				outcomeFile, row.line, row.fields[0]))
			continue
		}
		out[row.fields[0]] = row.fields[1]
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return out, nil
}

// refuses reports whether a branch ENDS in an error rather than in a
// result.
//
// A refusal is not an outcome. It says what THIS SERVER did — it
// declined — which is a fact about the call and true whatever Drive
// would have answered, and the sentence in it is usually the longest
// prose in the file.
//
// The terminal statement, not any return anywhere inside. An earlier
// version inspected the whole branch and excused it on any `return nil,
// x` it found, which meant one ordinary error check silenced everything
// after it — a review probe put the phase-5 lock_file defect verbatim
// behind an `if err != nil { return nil, err }` and the gate reported
// nothing at all. A branch that propagates an error and then goes on to
// state an outcome is exactly the shape this gate is for, and it was the
// one shape it could not see.
func refuses(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) == 0 {
		return false
	}
	ret, ok := body.List[len(body.List)-1].(*ast.ReturnStmt)
	if !ok {
		return false
	}
	// `return nil, something` is this codebase's refusal, whatever builds
	// the something: Errorf, an Error literal, or a helper like
	// s.confirmed that words the confirm-flag refusals. A result return
	// is `return s.report(...), nil`, whose first value is not nil, so
	// the two are told apart by shape rather than by a list of helper
	// names that would need adding to.
	if len(ret.Results) > 1 {
		if id, ok := ret.Results[0].(*ast.Ident); ok && id.Name == "nil" {
			return true
		}
		return false
	}
	found := false
	for _, result := range ret.Results {
		ast.Inspect(result, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CallExpr:
				if isRefusalOrLog(v) {
					found = true
				}
			case *ast.CompositeLit:
				if id, ok := v.Type.(*ast.Ident); ok && id.Name == "Error" {
					found = true
				}
			}
			return !found
		})
	}
	return found
}

// saysItIsADryRun reports whether a branch marks its own result as a dry
// run, which is the thing that makes its sentence honest.
//
// A dry run makes no call by construction, so "the response did not
// carry it" is trivially true of every one of them, and what it says is
// in the conditional: "would be gone for good" is the honest form of
// exactly this sentence rather than a violation of it.
//
// The first version of this gate excluded the FIELD named DryRun
// instead, which is the same answer by a worse route. It made
// `if in.DryRun { note = "the file was deleted" }` invisible for ever,
// because a field removed from the set is removed from every branch that
// tests it. Reading the result's own DryRun marker asks the question
// that matters — does this branch tell the caller it is describing
// something that has not happened — and every dry-run branch in this
// package that carries prose sets it.
func saysItIsADryRun(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "DryRun" {
			return true
		}
		if value, ok := kv.Value.(*ast.Ident); ok && value.Name == "true" {
			found = true
		}
		return !found
	})
	return found
}

// outcomeKey names a branch in the record file: a path with forward
// slashes, whatever the platform writes, and the field it tests.
//
// One function because the key is built in two places — the gate and the
// test that holds the record to the code — and they have to agree
// character for character or the record silently excuses nothing.
//
// The path arrives from filepath, so on Windows it is
// `internal\service\drives.go` where the record file names it with
// forward slashes. Nothing matched: every excused branch was reported
// unexcused AND every row was reported stale, so the gate failed on a
// tree it passes on everywhere else. Found by CI on the third platform,
// which is the whole reason a merge waits for it.
//
// ReplaceAll rather than filepath.ToSlash, which is what the first fix
// used. ToSlash is a no-op wherever the separator is already a slash, so
// it is correct on Windows and UNTESTABLE anywhere else — the assertion
// that would prove it can only run on the platform that had the bug. A
// replacement that does the same thing everywhere can be asserted on the
// machine somebody is actually working on, which today is worth more
// than the idiom. No path this gate walks contains a backslash on a
// system where one would be legal.
func outcomeKey(file, field string) string {
	return strings.ReplaceAll(file, `\`, "/") + ":" + field
}
