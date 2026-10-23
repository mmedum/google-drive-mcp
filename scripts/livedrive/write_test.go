package main

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
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
	// Both ends of the digit range.
	for _, date := range []string{"2000-01-01", "2019-09-29"} {
		if !looksLikeDate(date) {
			t.Errorf("looksLikeDate(%q) = false", date)
		}
	}
	for _, not := range []string{"rows.csv", "2026-3-4", "09:00Z", ""} {
		if looksLikeDate(not) {
			t.Errorf("looksLikeDate(%q) = true", not)
		}
	}
}

func TestHeadCountsTheLinesItLeavesOut(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"a\nb", "a\nb"},
		{"a\nb\nc", "a\nb\n… (1 more line)"},
		{"a\nb\nc\nd", "a\nb\n… (2 more lines)"},
	} {
		if got := head(tc.text, 2); got != tc.want {
			t.Errorf("head(%q, 2) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestAFlagIsGivenWhenItIsSetAwayFromItsDefault(t *testing.T) {
	fs := flag.NewFlagSet("livedrive", flag.ContinueOnError)
	fs.Bool("write", false, "")
	fs.Bool("labels", false, "")
	fs.String("share", "", "")
	fs.String("bin", "./google-drive-mcp", "")
	if err := fs.Parse([]string{"-write", "-labels=false", "-bin", "./google-drive-mcp"}); err != nil {
		t.Fatal(err)
	}
	if got := givenFlags(fs); len(got) != 1 || !got["write"] {
		t.Errorf("given = %v, want only write", got)
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

func TestTheReviewerAddressLeavesTheAngleBracket(t *testing.T) {
	// get_account prints "Name <address>". A character class that ran
	// from + to … took in the <, and Drive refused the reviewer.
	out := "Jane Doe <jane.doe@example.com>\nstorage: 1 GiB of 15 GiB used\n"
	if got := addressInAccount.FindString(out); got != "jane.doe@example.com" {
		t.Errorf("address = %q, want jane.doe@example.com", got)
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

// The query probe's status comes from the answer when Google's front end
// refused, and from the debug log otherwise, in either log format.
func TestTheProbeReadsTheStatusFromTheAnswerOrTheLog(t *testing.T) {
	page := "[invalid] searching Drive failed: HTTP 400 Bad Request, answered with an error page rather than a Drive error"
	text := `time=2026-03-06T12:00:00Z level=DEBUG msg="drive api error" method=GET path=/drive/v3/files attempt=1 status=414 class=invalid`
	json := `{"level":"DEBUG","msg":"drive api error","method":"GET","attempt":1,"status":413,"class":"invalid"}`
	for _, tc := range []struct {
		answer string
		logs   []string
		want   string
	}{
		{page, []string{text}, "400"},
		{"[invalid] Invalid Value", []string{text}, "414"},
		{"[invalid] Invalid Value", []string{json}, "413"},
		{"[invalid] Invalid Value", []string{`level=INFO msg="tool call" status=200`}, "not in the answer, and not logged: run with GDRIVE_LOG_LEVEL=debug"},
	} {
		if got := statusOf(tc.answer, tc.logs); got != tc.want {
			t.Errorf("statusOf(%q, %q) = %q, want %q", tc.answer, tc.logs, got, tc.want)
		}
	}
}

// Drive drops a file's own link grant when the file moves into a folder
// open to anyone with the link and back out (§18). After that, a move of
// several, a copy into the folder and removing the link each need the
// file shared by link, and have it only because the driver shares it
// again first. This plays Drive's part: share_by_link gives the grant and
// a move back to the scratch folder takes it.
func TestTheFileSharedByLinkHasItsGrantAtEachStepThatNeedsIt(t *testing.T) {
	f := newFakeDrive()
	w, _ := testRun(t, f, options{destructive: true})
	w.scratchID = w.createAndKeepID("create_folder", map[string]any{"name": scratchPrefix + " 2026-03-06 12:00:00"})
	w.moveExposure()

	var file, open string
	for _, c := range f.calls {
		if c.tool != "share_by_link" {
			continue
		}
		switch c.text("file_name") {
		case "moves into the open folder.txt":
			file = c.text("file")
		case "Open by link":
			open = c.text("file")
		}
	}
	if file == "" || open == "" {
		t.Fatalf("the file (%q) or the folder (%q) was never shared by link", file, open)
	}
	names := func(c sentCall) bool {
		files, _ := c.args["files"].([]any)
		return c.text("file") == file || slices.Contains(files, any(file))
	}
	linked := false
	steps := map[string]int{}
	for _, c := range f.calls {
		step := ""
		switch {
		case c.tool == "share_by_link" && names(c) && c.flag("confirm") && !c.flag("dry_run"):
			linked = true
		case c.tool == "move_file" && names(c) && c.text("to") == w.scratchID && !c.flag("dry_run"):
			linked = false
		case c.tool == "move_file" && c.args["files"] != nil && names(c) && c.text("to") == open:
			step = "the move of several"
		case c.tool == "copy_file" && names(c) && c.text("to") == open:
			step = "the copy into the open folder"
		case c.tool == "unshare_file" && names(c) && c.flag("remove_link"):
			step = "removing the link"
		}
		if step == "" {
			continue
		}
		steps[step]++
		if !linked {
			t.Errorf("%s ran while the file had no link grant: %v", step, c.args)
		}
	}
	for _, step := range []string{"the move of several", "the copy into the open folder", "removing the link"} {
		if steps[step] == 0 {
			t.Errorf("%s never ran", step)
		}
	}
}

// Only share_by_link opens a folder to anyone with the link, and it is
// registered only with -destructive. Without it the steps make no call
// and say why.
func TestWithoutDestructiveTheLinkStepsSayWhyTheyDidNotRun(t *testing.T) {
	f := newFakeDrive()
	w, out := testRun(t, f, options{})
	w.scratchID = "id-scratch-fixture"
	w.moveExposure()
	if len(f.calls) != 0 {
		t.Errorf("without -destructive the run made %d calls, the first %s %v", len(f.calls), f.calls[0].tool, f.calls[0].args)
	}
	if want := "(pass -destructive to check that a move or a copy that would reach anyone with the link is refused"; !strings.Contains(out.String(), want) {
		t.Errorf("the transcript does not say why the steps did not run:\n%s", out.String())
	}
}
