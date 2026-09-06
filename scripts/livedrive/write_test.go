package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestFirstRevisionReadsTheIDOutOfAListing(t *testing.T) {
	// The parser is only ever run against this renderer's output, so the
	// shape it expects is worth pinning: a subject line, then rows whose
	// second field is a date, then prose.
	listing := "rows.csv — csv: 2 revisions\n" +
		"id-revision-b  2026-03-04 09:05Z (2 days ago) by Test Person (you)  1.2 KiB  current\n" +
		"id-revision-a  2026-03-04 09:00Z (2 days ago) by Test Person (you)  1.0 KiB\n" +
		"Drive discards a revision 30 days after it stops being current unless it is kept forever\n"
	got := ""
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && looksLikeDate(fields[1]) {
			got = fields[0]
			break
		}
	}
	if got != "id-revision-b" {
		t.Errorf("first revision = %q, want the newest row's id", got)
	}
	for _, not := range []string{"rows.csv", "2026-3-4", "09:00Z", ""} {
		if looksLikeDate(not) {
			t.Errorf("looksLikeDate(%q) = true", not)
		}
	}
}

func TestOwnerIsReadFromACardForComparison(t *testing.T) {
	// Spike F compares the owner before and after a transfer, because a
	// call that answers 200 and leaves the owner where it was looks
	// identical to one that worked. The value is only ever compared,
	// never printed, so it is read before redaction.
	card := "created: ownership transfer probe — text file\n" +
		"id: id-transfer-probe-fixture\n" +
		"owner: Someone Else <someone@example.com>\n" +
		"you can: edit, comment\n"
	if got := ownerFromResult(card); got != "Someone Else <someone@example.com>" {
		t.Errorf("owner = %q", got)
	}
	// A card with no owner line means the file could not be read back,
	// which spike F reports as "unknown" rather than as success.
	if got := ownerFromResult("id: id-transfer-probe-fixture\n"); got != "" {
		t.Errorf("owner = %q, want empty when there is no owner line", got)
	}
}

// TestEmptyTrashIsNamedInOnePlace holds the safety rule destroy.go
// claims for itself.
//
// empty_trash without a drive empties the signed-in account's own trash:
// real deleted work, inside the thirty-day window that is the only thing
// standing between it and gone. Every call this driver makes is scoped
// to the scratch shared drive, and the scoping lives in one method so
// that it is a property of the code rather than of whoever writes the
// next call. A second mention of the tool name is that rule quietly
// ending, which is what this fails on.
func TestEmptyTrashIsNamedInOnePlace(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the driver's directory: %v", err)
	}
	fset := token.NewFileSet()
	var found []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if v, err := strconv.Unquote(lit.Value); err == nil && v == "empty_trash" {
				found = append(found, fset.Position(lit.Pos()).String())
			}
			return true
		})
	}
	if len(found) != 1 {
		t.Errorf("the empty_trash tool is named in %d places, and the scoping to a scratch "+
			"shared drive only holds while it is named in one:\n%s",
			len(found), strings.Join(found, "\n"))
	}
}
