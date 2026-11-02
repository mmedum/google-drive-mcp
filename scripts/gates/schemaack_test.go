package main

import (
	"fmt"
	"io"
	"os"
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

// relabeled is plain with the resource template's name changed, and
// withDraft is plain with a tool added since the release.
var (
	relabeled = strings.Replace(plain, `"name":"file text"`, `"name":"text"`, 1)
	withDraft = strings.Replace(plain, `"tools":[`, `"tools":[{"name":"draft_tool"},`, 1)
)

// refusalFix is the footer a refusal ends with, after its blank line.
func refusalFix(err error) string {
	_, fix, _ := strings.Cut(fmt.Sprint(err), "\n\n")
	return fix
}

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
		{"a description word names nothing", flipped, "Mark it\n\nSCHEMA-CHANGE: get_file: update_file is destructive", wantUnnamed},
		{"a tab after the colon starts the description too", flipped, "Mark it\n\nSCHEMA-CHANGE: get_file:\tupdate_file is destructive", wantUnnamed},
		{"a value with no description names its whole list", flipped, "Mark them\n\nSCHEMA-CHANGE: get_file, update_file", ""},
		{"a value wrapped at the margin continues it", flipped, "Mark them\n\nSCHEMA-CHANGE: get_file,\nupdate_file: destructiveHint true", ""},
		{"a Token #value footer is a footer", flipped, "Mark it\n\nSCHEMA-CHANGE: update_file: hints\n\nCloses #12", ""},
		{"a footer ends the value before it", flipped, "Mark it\n\nSCHEMA-CHANGE: get_file,\nRefs #12, update_file", wantUnnamed},
		{"three blank lines end one paragraph", flipped, "Mark it\n\nSCHEMA-CHANGE: update_file: hints\n\n\n\nCo-Authored-By: someone <x@example.com>", ""},
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

// A refusal ends with the footer that fixes it, bare so it can be copied:
// BREAKING CHANGE: for the breaks, SCHEMA-CHANGE: for the rest, an item
// the first names left out of the second. The footer it prints passes.
func TestARefusalNamesTheFooterThatFixesIt(t *testing.T) {
	built := strings.Replace(relabeled, `"properties":{"file"`, `"properties":{"target"`, 1)
	built = strings.Replace(built, `"destructiveHint":false`, `"destructiveHint":true`, 1)
	c := compareSurfaces(surfaceOf(t, plain), surfaceOf(t, built))
	fix := refusalFix(checkAck(io.Discard, c, []string{"Change them"}, "base"))
	if want := "BREAKING CHANGE: update_file: what broke\nSCHEMA-CHANGE: gdrive://{file}: what changed"; fix != want {
		t.Fatalf("the refusal's fix is %q, want %q", fix, want)
	}
	if err := checkAck(io.Discard, c, []string{"Change them", "Acknowledge them\n\n" + fix}, "base"); err != nil {
		t.Errorf("the printed fix does not pass: %v", err)
	}
}

// The fix a refusal prints puts a name the footer would split or trim in
// backticks, so the footer it prints is one that passes. Bare, the URI
// with a comma names nothing.
func TestARefusalQuotesANameTheFooterWouldSplit(t *testing.T) {
	for _, uri := range []string{"gdrive://{file}{?view,page}", "gdrive://{file}."} {
		t.Run(uri, func(t *testing.T) {
			was := strings.Replace(plain, `"gdrive://{file}"`, `"`+uri+`"`, 1)
			c := compareSurfaces(surfaceOf(t, was), surfaceOf(t, strings.Replace(was, `"name":"file text"`, `"name":"text"`, 1)))
			fix := refusalFix(checkAck(io.Discard, c, []string{"Rename it"}, "base"))
			if want := "SCHEMA-CHANGE: `" + uri + "`: what changed"; fix != want {
				t.Fatalf("the refusal's fix is %q, want %q", fix, want)
			}
			if err := checkAck(io.Discard, c, []string{"Rename it", "Name it\n\n" + fix}, "base"); err != nil {
				t.Errorf("checkAck = %v after the fix it named", err)
			}
			if bare := strings.ReplaceAll(fix, "`", ""); strings.Contains(uri, ",") && checkAck(io.Discard, c, []string{"Rename it\n\n" + bare}, "base") == nil {
				t.Errorf("%q passed, though split on its comma it names nothing", bare)
			}
		})
	}
}

// A break no footer names is asked for once, as a break: the BREAKING
// CHANGE: footer that fixes it names it for both checks.
func TestARefusalAsksForAnUnnamedBreakOnce(t *testing.T) {
	err := checkAck(io.Discard, compareSurfaces(surfaceOf(t, plain), surfaceOf(t, renamed)), nil, "base")
	if want := "the tool surface changed since base, and no BREAKING CHANGE: footer names update_file. "; err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("checkAck = %v, want it to start %q", err, want)
	}
}

// A break of the released surface needs BREAKING CHANGE:; removing a tool
// added since the release breaks no caller, so it is a plain change, and
// the report lists it as one.
func TestABreakIsABreakOfARelease(t *testing.T) {
	release := surfaceOf(t, plain)
	base := surfaceOf(t, withDraft)
	head := surfaceOf(t, renamed)
	c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking)
	if !slices.Equal(c.breaking, renameBreak) {
		t.Errorf("breaking = %v, want %v", c.breaking, renameBreak)
	}
	if want := []change{{item: "update_file", text: "update_file (inputSchema.properties)"},
		{item: "draft_tool", text: "draft_tool removed", field: "draft_tool"}}; !slices.Equal(c.changed, want) {
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
	// Nor does a released tool of the name of a prompt the pull request
	// removes: they are two items, though a footer names both alike.
	release = surfaceOf(t, `{"tools":[{"name":"get_file"},{"name":"summarize"}]}`)
	base = surfaceOf(t, `{"tools":[{"name":"get_file"}],"prompts":[{"name":"summarize"}]}`)
	head = surfaceOf(t, `{"tools":[{"name":"get_file"}],"prompts":[]}`)
	if c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking); len(c.breaking) != 0 {
		t.Errorf("breaking = %v, want the unreleased prompt's removal as a change", c.breaking)
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

// Both sides are built from commits: the merge-base and the head. The
// checkout is neither, in CI the merge commit and here main with a change
// nobody committed, so what it would serve is not what is judged.
func TestSchemaAckBuildsBothSidesFromCommits(t *testing.T) {
	r := newDumpRepo(t)
	r.commit(plain, "Base")
	r.git("switch", "--quiet", "-c", "feature")
	r.commit(flipped, "Flip update_file")
	r.git("switch", "--quiet", "main")
	r.write(renamed)

	var out strings.Builder
	err := schemaAck(&out, []string{"main", "feature"})
	if err == nil || !strings.HasSuffix(err.Error(), "\n\nSCHEMA-CHANGE: update_file: what changed") {
		t.Fatalf("schemaAck = %v, want a refusal naming the flip alone", err)
	}
	if !strings.Contains(out.String(), "update_file (annotations.destructiveHint)\n") || strings.Contains(out.String(), "BREAKING") {
		t.Errorf("the report is\n%s\nwant the flip and no break", out.String())
	}
	r.git("checkout", "--", ".")
	r.git("switch", "--quiet", "feature")
	r.git("commit", "--quiet", "--allow-empty", "-m", "Acknowledge it\n\nSCHEMA-CHANGE: update_file: destructive")
	if err := schemaAck(io.Discard, []string{"main", "HEAD"}); err != nil {
		t.Fatalf("acknowledged: %v", err)
	}
	if n := r.worktrees(); n != 1 {
		t.Errorf("%d worktrees left, want the checkout alone", n)
	}
}

// Through the whole gate: a tool no release shipped, removed, is a plain
// change, and an input the released baseline lists, renamed, is a break.
func TestSchemaAckAsksForBreakingChangeOnlyForAReleasedBreak(t *testing.T) {
	r := newDumpRepo(t)
	r.release(plain, "Release")
	r.commit(withDraft, "Add draft_tool")
	r.git("switch", "--quiet", "-c", "feature")
	r.commit(renamed, "Drop draft_tool and rename file")
	c, messages, base, err := ackInputs("main", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	fix := refusalFix(checkAck(io.Discard, c, messages, base))
	if want := "BREAKING CHANGE: update_file: what broke\nSCHEMA-CHANGE: draft_tool: what changed"; fix != want {
		t.Fatalf("the fix is %q, want %q", fix, want)
	}
	if err := checkAck(io.Discard, c, append(messages, "Acknowledge them\n\n"+fix), base); err != nil {
		t.Fatalf("acknowledged: %v", err)
	}
}

// The release is the baseline the base branch records, not the head's:
// a branch cut before the last release, renaming what that release
// already renamed, breaks no caller of it.
func TestTheReleaseIsTheBaseBranchsBaseline(t *testing.T) {
	r := newDumpRepo(t)
	r.release(plain, "Release 1")
	r.git("branch", "feature")
	r.release(renamed, "Release 2")
	r.git("switch", "--quiet", "feature")
	r.commit(renamed, "Rename file")
	if fix, want := refusalFix(schemaAck(io.Discard, []string{"main", "HEAD"})), "SCHEMA-CHANGE: update_file: what changed"; fix != want {
		t.Errorf("the fix is %q, want %q, since release 2 shipped the rename", fix, want)
	}
}

// dumpRepo is a git repository, the working directory for the test,
// whose cmd/google-drive-mcp prints a fixed dump, so the gate's worktree
// builds are exercised without building the real server. The gate's own
// git reads none of the machine's configuration either.
type dumpRepo struct{ t *testing.T }

func newDumpRepo(t *testing.T) *dumpRepo {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Chdir(t.TempDir())
	r := &dumpRepo{t}
	r.git("init", "--quiet", "-b", "main")
	write(t, "go.mod", "module example.invalid/fake\n\ngo 1.21\n")
	r.git("add", "go.mod")
	return r
}

func (r *dumpRepo) git(args ...string) {
	r.t.Helper()
	gitIn(r.t, args...)
}

// commit commits a main that prints dump.
func (r *dumpRepo) commit(dump, message string) {
	r.t.Helper()
	r.write(dump)
	r.git("add", "-A")
	r.git("commit", "--quiet", "-m", message)
}

// release commits a main that prints dump with dump as the baseline, as
// a release commit records it.
func (r *dumpRepo) release(dump, message string) {
	r.t.Helper()
	write(r.t, schemaBaselineFile, dump)
	r.commit(dump, message)
}

// write puts a main that prints dump in the working tree, uncommitted.
func (r *dumpRepo) write(dump string) {
	r.t.Helper()
	write(r.t, "cmd/google-drive-mcp/main.go",
		fmt.Sprintf("package main\n\nimport \"os\"\n\nfunc main() { os.Stdout.WriteString(%q) }\n", dump))
}

func (r *dumpRepo) worktrees() int {
	r.t.Helper()
	out, err := git("worktree", "list", "--porcelain")
	if err != nil {
		r.t.Fatal(err)
	}
	return strings.Count(out, "worktree ")
}
