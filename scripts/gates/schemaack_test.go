package main

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// plain is a released surface: two tools and a resource template.
const plain = `{"version":"v1.0.0","tools":[
	{"name":"update_file","annotations":{"destructiveHint":false},
	 "inputSchema":{"type":"object","properties":{"file":{"type":"string"}}}},
	{"name":"get_file","annotations":{"readOnlyHint":true},
	 "inputSchema":{"type":"object","properties":{"file":{"type":"string"}}}}],
	"resourceTemplates":[{"uriTemplate":"gdrive://{file}","name":"file text"}]}`

// flipped is plain with update_file made destructive, and renamed is
// plain with update_file's file input renamed, which breaks it.
var (
	flipped = strings.Replace(plain, `"destructiveHint":false`, `"destructiveHint":true`, 1)
	renamed = strings.Replace(plain, `"properties":{"file"`, `"properties":{"target"`, 1)
)

// renameBreak is the break renamed makes of plain.
var renameBreak = []change{{"update_file", "update_file: input field file removed", "input file"}}

// surfaceOf reads a dump the tests write out.
func surfaceOf(t *testing.T, dump string) surface {
	t.Helper()
	s, err := readSurface([]byte(dump))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// schema-ack fails a pull request unless its commits name every changed
// item in a footer, and every break of a release in a BREAKING CHANGE:
// footer.
func TestSchemaAckNeedsEveryItemNamed(t *testing.T) {
	relabeled := strings.Replace(plain, `"name":"file text"`, `"name":"text"`, 1)
	withoutTemplate := strings.Replace(plain, `{"uriTemplate":"gdrive://{file}","name":"file text"}`, ``, 1)
	reworded := strings.Replace(plain, `"version":"v1.0.0",`, `"version":"v1.0.0","instructions":"new",`, 1)
	retitled := strings.Replace(plain, `{"name":"get_file",`, `{"name":"get_file","title":"Get a file",`, 1)
	marked := strings.Replace(plain, `{"name":"get_file",`, `{"name":"get_file","_meta":{"mark":true},`, 1)
	const (
		wantUnnamed      = "no footer names update_file"
		wantUnnamedBreak = "no BREAKING CHANGE: footer names update_file"
	)
	for _, tc := range []struct {
		name, built, message, wantErr string
	}{
		{"an unchanged surface needs nothing", plain, "Tidy the README", ""},
		{"a change needs a footer", flipped, "Mark update_file destructive", wantUnnamed},
		{"a footer naming the tool acknowledges it", flipped, "Mark it\n\nSCHEMA-CHANGE: update_file: destructiveHint true", ""},
		{"a footer naming another tool does not", flipped, "Mark it\n\nSCHEMA-CHANGE: get_file: destructiveHint true", wantUnnamed},
		{"a name is a whole token", flipped, "Mark it\n\nSCHEMA-CHANGE: update_files: destructiveHint true", wantUnnamed},
		{"a continuation line can name it", flipped, "Mark them\n\nSCHEMA-CHANGE: get_file,\n  update_file: destructiveHint true", ""},
		{"an empty footer says nothing", flipped, "Mark it\n\nSCHEMA-CHANGE:", wantUnnamed},
		{"the next line is not the footer's text", flipped, "Mark it\n\nSCHEMA-CHANGE:\nCo-Authored-By: update_file", wantUnnamed},
		{"a footer must open its line", flipped, "Mark it, see SCHEMA-CHANGE: update_file", wantUnnamed},
		{"a line of prose is not a footer", flipped, "Mark it\n\nThe gate wants a\nSCHEMA-CHANGE: update_file footer, which this is not.", wantUnnamed},
		{"a subject is not a footer", flipped, "SCHEMA-CHANGE: update_file", wantUnnamed},
		{"an indented block is not a footer block", flipped, "Mark it\n\nSCHEMA-CHANGE: update_file: hints\n\n    go run ./scripts/gates schema-ack", wantUnnamed},
		{"a footer above the co-author trailer", flipped, "Mark it\n\nSCHEMA-CHANGE: update_file: hints\n\nCo-Authored-By: someone <x@example.com>", ""},
		{"a footer beside the co-author trailer", flipped, "Mark it\n\nCo-Authored-By: someone <x@example.com>\nSCHEMA-CHANGE: update_file: hints", ""},
		{"a title is a change", retitled, "Title it", "no footer names get_file"},
		{"a _meta mark is a change", marked, "Mark it", "no footer names get_file"},
		{"a resource template by its URI", relabeled, "Rename it\n\nSCHEMA-CHANGE: gdrive://{file}: renamed", ""},
		{"a resource template unnamed", relabeled, "Rename it\n\nSCHEMA-CHANGE: update_file: renamed", "no footer names gdrive://{file}"},
		{"an instructions change needs a footer", reworded, "Reword them", "no footer names instructions"},
		{"the instructions by their key", reworded, "Reword them\n\nSCHEMA-CHANGE: instructions: reworded", ""},
		{"a break named only as a change", renamed, "Rename the field\n\nSCHEMA-CHANGE: update_file: file is target", wantUnnamedBreak},
		{"BREAKING CHANGE: acknowledges a break", renamed, "Rename the field\n\nBREAKING CHANGE: update_file: file is target", ""},
		{"BREAKING-CHANGE: is its synonym", renamed, "Rename the field\n\nBREAKING-CHANGE: update_file: file is target", ""},
		{"SCHEMA-CHANGE: in any case", flipped, "Mark it\n\nSchema-Change: update_file: destructiveHint true", ""},
		{"BREAKING CHANGE: only in uppercase", renamed, "Rename the field\n\nBreaking-Change: update_file: file is target", wantUnnamedBreak},
		{"a removed template needs BREAKING CHANGE:", withoutTemplate, "Drop it\n\nSCHEMA-CHANGE: gdrive://{file}: gone",
			"no BREAKING CHANGE: footer names gdrive://{file}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := compareSurfaces(surfaceOf(t, plain), surfaceOf(t, tc.built))
			err := checkAck(io.Discard, c, []string{"Tidy the docs", tc.message}, "base")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("checkAck = %v, want it to pass", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("checkAck = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

// The fix a refusal names is the footer that fixes it: BREAKING CHANGE:
// for an unnamed break, SCHEMA-CHANGE: for the rest, each naming its
// items, and a message that ends with it passes.
func TestARefusalNamesTheFooterThatFixesIt(t *testing.T) {
	both := strings.Replace(renamed, `"name":"file text"`, `"name":"text"`, 1)
	for _, tc := range []struct{ name, built, wantFix string }{
		{"a change", flipped, "SCHEMA-CHANGE: update_file: what changed"},
		{"a break", renamed, "BREAKING CHANGE: update_file: what broke"},
		{"a break and a change", both, "BREAKING CHANGE: update_file: what broke\nSCHEMA-CHANGE: gdrive://{file}: what changed"},
		{"a break and a change to one tool", strings.Replace(renamed, `"destructiveHint":false`, `"destructiveHint":true`, 1),
			"BREAKING CHANGE: update_file: what broke"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := compareSurfaces(surfaceOf(t, plain), surfaceOf(t, tc.built))
			err := checkAck(io.Discard, c, []string{"Change it"}, "base")
			if err == nil {
				t.Fatal("checkAck passed with no footer")
			}
			_, fix, _ := strings.Cut(err.Error(), "\n\n")
			if fix != tc.wantFix {
				t.Errorf("the refusal's fix is %q, want %q", fix, tc.wantFix)
			}
			if err := checkAck(io.Discard, c, []string{"Change it", "Name it\n\n" + fix}, "base"); err != nil {
				t.Errorf("checkAck = %v after the fix it named", err)
			}
		})
	}
}

// A name in backticks is taken whole, so a URI with a comma in it can be
// named; split on its comma, it names nothing.
func TestAFooterCanNameAURIWithACommaInBackticks(t *testing.T) {
	const tmpl = "gdrive://{file}{?view,page}"
	was := strings.Replace(plain, `"gdrive://{file}"`, `"`+tmpl+`"`, 1)
	now := strings.Replace(was, `"name":"file text"`, `"name":"text"`, 1)
	c := compareSurfaces(surfaceOf(t, was), surfaceOf(t, now))
	if err := checkAck(io.Discard, c, []string{"Rename\n\nSCHEMA-CHANGE: `" + tmpl + "`: renamed"}, "base"); err != nil {
		t.Errorf("checkAck = %v, want the backticked URI to name the template", err)
	}
	if err := checkAck(io.Discard, c, []string{"Rename\n\nSCHEMA-CHANGE: " + tmpl + ": renamed"}, "base"); err == nil {
		t.Error("checkAck passed with the URI split on its comma, which names nothing")
	}
}

// The fix a refusal prints puts a name the footer would split or trim in
// backticks, so the footer it prints is one that passes.
func TestARefusalQuotesANameTheFooterWouldSplit(t *testing.T) {
	for _, uri := range []string{"gdrive://{file}{?view,page}", "gdrive://{file}."} {
		t.Run(uri, func(t *testing.T) {
			was := strings.Replace(plain, `"gdrive://{file}"`, `"`+uri+`"`, 1)
			c := compareSurfaces(surfaceOf(t, was), surfaceOf(t, strings.Replace(was, `"name":"file text"`, `"name":"text"`, 1)))
			err := checkAck(io.Discard, c, []string{"Rename it"}, "base")
			_, fix, _ := strings.Cut(fmt.Sprint(err), "\n\n")
			if want := "SCHEMA-CHANGE: `" + uri + "`: what changed"; fix != want {
				t.Fatalf("the refusal's fix is %q, want %q", fix, want)
			}
			if err := checkAck(io.Discard, c, []string{"Rename it", "Name it\n\n" + fix}, "base"); err != nil {
				t.Errorf("checkAck = %v after the fix it named", err)
			}
		})
	}
}

// A break of the released surface needs BREAKING CHANGE:; removing a tool
// added since the release breaks no caller, so it is a plain change, and
// the report lists it as one.
func TestABreakIsABreakOfARelease(t *testing.T) {
	release := surfaceOf(t, plain)
	base := surfaceOf(t, strings.Replace(plain, `"tools":[`, `"tools":[{"name":"draft_tool"},`, 1))
	head := surfaceOf(t, renamed)
	c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking)
	if !slices.Equal(c.breaking, renameBreak) {
		t.Errorf("breaking = %v, want %v", c.breaking, renameBreak)
	}
	if want := []change{{item: "update_file", text: "update_file (inputSchema.properties)"},
		{item: "draft_tool", text: "draft_tool removed"}}; !slices.Equal(c.changed, want) {
		t.Errorf("changed = %v, want %v", c.changed, want)
	}
	var report strings.Builder
	footer := "Drop and rename\n\nSCHEMA-CHANGE: draft_tool: never released\nBREAKING CHANGE: update_file: file is target"
	if err := checkAck(&report, c, []string{footer}, "base"); err != nil {
		t.Errorf("checkAck = %v, want both acknowledged", err)
	}
	want := "against the merge-base base: 0 added, 2 changed, 1 breaking\n" +
		"  ~ update_file (inputSchema.properties)\n" +
		"  ~ draft_tool removed\n" +
		"  ! update_file: input field file removed  BREAKING\n" +
		"the change since base is acknowledged\n"
	if report.String() != want {
		t.Errorf("the report is\n%s\nwant\n%s", report.String(), want)
	}
}

// A tool the base branch already broke since the release, in one field,
// does not make a pull request's break of another field, one no release
// shipped, a break of the release.
func TestABreakOfTheReleaseIsMatchedByItsField(t *testing.T) {
	withInputs := func(props string) string {
		return strings.Replace(plain, `"properties":{"file":{"type":"string"}}`, `"properties":{`+props+`}`, 1)
	}
	release := surfaceOf(t, withInputs(`"file":{"type":"string"},"note":{"type":"string"}`))
	base := surfaceOf(t, withInputs(`"file":{"type":"string"},"draft":{"type":"string"}`))
	head := surfaceOf(t, withInputs(`"file":{"type":"string"}`))
	c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking)
	if len(c.breaking) != 0 {
		t.Errorf("breaking = %v, want draft, which no release shipped, as a change", c.breaking)
	}
}

// A break of the release is matched by its field, not its text. Here the
// release takes a string, the merge-base widened that to an integer as
// well, and the head takes only an integer: the two breaks read
// differently, and both break the same field.
func TestABreakOfTheReleaseIsNotMatchedByItsText(t *testing.T) {
	typed := func(typ string) surface {
		return surfaceOf(t, strings.Replace(plain, `"properties":{"file":{"type":"string"}}`,
			`"properties":{"file":{"type":`+typ+`}}`, 1))
	}
	release, base, head := typed(`"string"`), typed(`["integer","string"]`), typed(`"integer"`)
	c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking)
	if len(c.breaking) != 1 || c.breaking[0].item != "update_file" {
		t.Errorf("breaking = %v, want update_file's narrowed input kept as a break of the release", c.breaking)
	}
}

// The release is the baseline the base branch records. Before the first
// release there is none, and nothing breaks one.
func TestReleasedBreaksReadTheBaseBranchBaseline(t *testing.T) {
	dir := gitRepo(t, "a@example.com", "Start")
	t.Chdir(dir)
	head := surfaceOf(t, renamed)

	breaks, err := releasedBreaks("HEAD", head)
	if err != nil || len(breaks) != 0 {
		t.Fatalf("releasedBreaks with no baseline = %v, %v; want none", breaks, err)
	}

	write(t, filepath.Join(dir, schemaBaselineFile), plain)
	gitIn(t, "add", schemaBaselineFile)
	gitIn(t, "commit", "--quiet", "-m", "Release")
	// The working tree's baseline is a branch's, not the release's.
	write(t, filepath.Join(dir, schemaBaselineFile), `{"tools":[]}`)
	breaks, err = releasedBreaks("HEAD", head)
	if err != nil || !slices.Equal(breaks, renameBreak) {
		t.Errorf("releasedBreaks = %v, %v; want %v", breaks, err, renameBreak)
	}
}

// A gate that could not run says so and exits 2; a refusal exits 1. The
// message is what tells them apart in CI, where `go run` exits 1 for
// both.
func TestSchemaAckTellsARefusalFromAFailure(t *testing.T) {
	refused := checkAck(io.Discard, compareSurfaces(surfaceOf(t, plain), surfaceOf(t, flipped)), nil, "base")
	if refused == nil || exitCode(refused) != 1 || strings.HasPrefix(refused.Error(), "could not run") {
		t.Errorf("a refusal (%v) exits %d, want 1 and no word of not running", refused, exitCode(refused))
	}
	t.Chdir(gitRepo(t, "a@example.com", "Start"))
	const wantErr = "could not run: git merge-base no-such-ref HEAD: "
	if err := schemaAck(io.Discard, []string{"no-such-ref", "HEAD"}); exitCode(err) != 2 || err == nil || !strings.HasPrefix(err.Error(), wantErr) {
		t.Errorf("schemaAck with no base = %v, exit %d; want exit 2 and %q", err, exitCode(err), wantErr)
	}
}
