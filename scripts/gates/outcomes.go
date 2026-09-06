package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
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
	fields, err := boolInputFields()
	if err != nil {
		return err
	}
	if len(fields) < 20 {
		return fmt.Errorf("found %d boolean input fields in internal/service; this check is not looking "+
			"at the code it is meant to", len(fields))
	}
	exempt, err := readOutcomeExemptions()
	if err != nil {
		return err
	}

	var problems []string
	claims, read, err := outcomeClaims(fields)
	if err != nil {
		return err
	}
	if read < 10 {
		return fmt.Errorf("only %d Go files under internal/service were read", read)
	}
	used := map[string]bool{}
	for _, c := range claims {
		key := fmt.Sprintf("%s:%s", c.file, c.field)
		if reason, ok := exempt[key]; ok {
			used[key] = true
			_ = reason
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
		if !used[key] {
			problems = append(problems, fmt.Sprintf(
				"%s excuses %s and nothing there states an outcome from the request any more; delete the row",
				outcomeFile, key))
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
		len(fields), read, len(exempt))
	return nil
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
func boolInputFields() (map[string]bool, error) {
	out := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(filepath.Join("internal", "service"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
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
					// DryRun is the one boolean this rule cannot be about.
					// A dry run makes no call by construction, so "the
					// response did not carry it" is trivially true of
					// every one of them — and what it says is in the
					// conditional: "would be gone for good" is the
					// honest form of exactly this sentence, not a
					// violation of it.
					if name.Name == "DryRun" {
						continue
					}
					out[name.Name] = true
				}
			}
			return true
		})
		return nil
	})
	return out, err
}

// outcomeClaims finds the branches that state an outcome from a request.
func outcomeClaims(fields map[string]bool) ([]claim, int, error) {
	var found []claim
	read := 0
	fset := token.NewFileSet()
	err := filepath.WalkDir(filepath.Join("internal", "service"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}
		read++
		ast.Inspect(file, func(n ast.Node) bool {
			stmt, ok := n.(*ast.IfStmt)
			if !ok {
				return true
			}
			field := testedField(stmt.Cond, fields)
			if field == "" {
				return true
			}
			if consultsDrive(stmt.Body) || refuses(stmt.Body) {
				return true
			}
			if quote, line := proseIn(stmt.Body, fset); quote != "" {
				found = append(found, claim{file: path, line: line, field: field, quote: quote})
			}
			return true
		})
		return nil
	})
	sort.Slice(found, func(i, j int) bool {
		if found[i].file != found[j].file {
			return found[i].file < found[j].file
		}
		return found[i].line < found[j].line
	})
	return found, read, err
}

// testedField names the request field a condition tests, if any.
func testedField(cond ast.Expr, fields map[string]bool) string {
	name := ""
	ast.Inspect(cond, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || name != "" {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && fields[sel.Sel.Name] && id.Name != "s" {
			name = sel.Sel.Name
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

// proseIn finds a sentence in a branch: a string literal with a space in
// it, long enough to be words rather than a key or a separator.
//
// A refusal is not an outcome and is skipped. Errorf says what this
// server did — it declined — which is a fact about the call and true
// whatever Drive would have said. What this gate is about is the other
// kind of sentence: the one that tells the caller what is now true of a
// file, and can be wrong.
func proseIn(body *ast.BlockStmt, fset *token.FileSet) (string, int) {
	quote, line := "", 0
	ast.Inspect(body, func(n ast.Node) bool {
		if quote != "" {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok && isRefusalOrLog(call) {
			return false
		}
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil || len(v) < 25 || !strings.Contains(strings.TrimSpace(v), " ") {
			return true
		}
		quote, line = short(v), fset.Position(lit.Pos()).Line
		return false
	})
	return quote, line
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
func readOutcomeExemptions() (map[string]string, error) {
	f, err := os.Open(outcomeFile)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	out := map[string]string{}
	scan := bufio.NewScanner(f)
	line := 0
	for scan.Scan() {
		line++
		text := strings.TrimRight(scan.Text(), " \r")
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Split(text, "\t")
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s:%d: want file:field and a reason, tab-separated", outcomeFile, line)
		}
		if strings.TrimSpace(fields[1]) == "" {
			return nil, fmt.Errorf("%s:%d: %s carries no reason, and an exemption without one is not a decision",
				outcomeFile, line, fields[0])
		}
		out[fields[0]] = fields[1]
	}
	return out, scan.Err()
}

// refuses reports whether a branch ends in an error rather than in a
// result.
//
// A refusal is not an outcome. It says what THIS SERVER did — it
// declined — which is a fact about the call and true whatever Drive
// would have answered, and the sentence in it is usually the longest
// prose in the file. isRefusalOrLog catches the ordinary shape, where
// the words are arguments to Errorf; this catches the other one, where a
// list of candidates is built up in a strings.Builder first and handed
// to an Error at the end. The ambiguity refusals are all written that
// way, because they print the candidates.
func refuses(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		// `return nil, something` is this codebase's refusal, whatever
		// builds the something: Errorf, an Error literal, or a helper like
		// s.confirmed that words the confirm-flag refusals. A result
		// return is `return s.report(...), nil`, whose first value is not
		// nil, so the two are told apart by shape rather than by a list of
		// helper names that would need adding to.
		if len(ret.Results) > 1 {
			if id, ok := ret.Results[0].(*ast.Ident); ok && id.Name == "nil" {
				found = true
				return false
			}
		}
		for _, result := range ret.Results {
			ast.Inspect(result, func(inner ast.Node) bool {
				switch v := inner.(type) {
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
		return !found
	})
	return found
}
