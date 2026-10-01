package main

import (
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheGateCatchesTheMistakeItWasWrittenFor. The three phase-5
// defects were all one shape: the sentence describing the result was
// written inside the branch that tested the request, and nothing was
// asked of Drive in between.
func TestTheGateCatchesTheMistakeItWasWrittenFor(t *testing.T) {
	cases := []struct {
		name, body string
		want       bool
	}{
		{
			name: "the lock_file mistake, as it was written",
			body: `	if in.LockFile {
		note += "The file is LOCKED while the approval is open: nobody can change its content."
	}`,
			want: true,
		},
		{
			name: "the same branch, wording the sentence from a read-back",
			body: `	if in.LockFile {
		note += " " + lockWords(after.File, lockedBefore, reread)
	}`,
		},
		{
			name: "the same branch, asking Drive first",
			body: `	if in.KeepPreviousRevision {
		if _, err := s.api.UpdateRevision(ctx, f.ID, before, true); err != nil {
			note += ". The content was replaced, but the previous revision could NOT be pinned."
		}
	}`,
		},
		{
			name: "a dry run, which says what WOULD happen and marks its result as one",
			body: `	if in.DryRun {
		return result(outcome{DryRun: true, Note: "everything in the trash would be gone for good."}), nil
	}`,
		},
		{
			// The hole the first version of this gate left open. It
			// excluded the FIELD named DryRun, so a branch testing it
			// could say anything at all for ever. Reading the RESULT's
			// own marker asks the question that matters: does this
			// branch tell the caller it is describing something that
			// has not happened?
			name: "a dry-run branch whose result does not say it is one",
			body: `	if in.DryRun {
		return result(outcome{Note: "the file was deleted and is gone for good."}), nil
	}`,
			want: true,
		},
		{
			// The hole a review probe found: one ordinary error check
			// used to silence everything after it, so the phase-5
			// defect verbatim plus an `if err != nil` produced no
			// claims at all.
			name: "an error propagated, and then an outcome stated anyway",
			body: `	if in.LockFile {
		if err := doThing(); err != nil {
			return nil, err
		}
		note += "The file is LOCKED while the approval is open: nobody can change its content."
	}`,
			want: true,
		},
		{
			name: "an outcome appended to a list of notes",
			body: `	if in.LockFile {
		notes = append(notes, "The file is LOCKED while the approval is open.")
	}`,
			want: true,
		},
		{
			name: "a sentence written and then refused with an error, so it never reaches the caller",
			body: `	if in.LockFile {
		note += "The file is LOCKED while the approval is open."
		return nil, Errorf(ClassInvalid, "refused")
	}`,
		},
		{
			name: "the same, from a function that returns only the error",
			body: `	if in.LockFile {
		note += "The file is LOCKED while the approval is open."
		return Errorf(ClassInvalid, "refused")
	}`,
		},
		{
			name: "a note taken from a call with two results",
			body: `	if in.LockFile {
		_, note := describe()
	}`,
		},
		{
			name: "a refusal, which says what this server did",
			body: `	if !in.Confirm {
		return nil, s.confirmed(false, "empty_trash", "destroy everything in the trash, with no way back")
	}`,
		},
		{
			name: "an ambiguity refusal, whose words are built up before the error",
			body: `	if in.RemoveLink {
		var b strings.Builder
		b.WriteString("more than one link-style grant is on this file; name one with permission_id:")
		return nil, &Error{Class: ClassAmbiguous, Message: b.String()}
	}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "package service\n\ntype ThingInput struct {\n\tLockFile bool\n\tDryRun bool\n" +
				"\tConfirm bool\n\tRemoveLink bool\n\tKeepPreviousRevision bool\n}\n\n" +
				"type outcome struct {\n\tDryRun bool\n\tNote string\n}\n\n" +
				"func (s *Service) Do(in ThingInput) {\n" + c.body + "\n}\n"
			if got := len(claimsIn(t, src)) > 0; got != c.want {
				t.Errorf("flagged = %v, want %v", got, c.want)
			}
		})
	}
}

// TestEveryBooleanInputIsInScope, DryRun included. The first version of
// this gate dropped that one field from the set, which excused every
// branch testing it whatever it said. A dry run is excused by its own
// result saying it is a dry run, which is a fact about the branch rather
// than about the field's name.
func TestEveryBooleanInputIsInScope(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	pkg := filepath.Join("internal", "service")
	if err := os.MkdirAll(pkg, 0o700); err != nil {
		t.Fatal(err)
	}
	src := "package service\n\ntype ThingInput struct {\n\tDryRun bool\n\tNotify bool\n}\n"
	if err := os.WriteFile(filepath.Join(pkg, "thing.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	files, err := parseService(fset)
	if err != nil {
		t.Fatal(err)
	}
	fields := boolInputFields(files)
	if !fields["DryRun"] || !fields["Notify"] {
		t.Errorf("a boolean input is missing from the field set: %v", fields)
	}
}

// outcomeTree writes a service package of exactly the gate's floors —
// ten files and twenty boolean inputs — with the given branch code in
// one of them, and the given exemption record, and runs the gate.
func outcomeTree(t *testing.T, branches, record string) (string, error) {
	t.Helper()
	t.Chdir(t.TempDir())
	pkg := filepath.Join("internal", "service")
	if err := os.MkdirAll(pkg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("testdata", 0o700); err != nil {
		t.Fatal(err)
	}
	var fields strings.Builder
	fields.WriteString("package service\n\ntype ThingInput struct {\n\tLockFile bool\n\tNotify bool\n")
	for i := range 18 {
		fmt.Fprintf(&fields, "\tOption%d bool\n", i)
	}
	fields.WriteString("}\n")
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(pkg, "input.go"), fields.String())
	write(filepath.Join(pkg, "thing.go"), "package service\n\nfunc (s *Service) Do(in ThingInput) {\n"+branches+"\n}\n")
	for i := range 8 {
		write(filepath.Join(pkg, fmt.Sprintf("other%d.go", i)), "package service\n")
	}
	write(outcomeFile, record)
	var out strings.Builder
	err := outcomes(&out, nil)
	return out.String(), err
}

// The exemption record against the gate, both ways: an excused branch
// passes, a row that excuses nothing fails, and a row cannot excuse two
// branches. The tree sits exactly on the floors, so it also holds that
// ten files and twenty inputs are enough to read.
func TestTheExemptionRecordIsHeldToTheBranches(t *testing.T) {
	const claim = "\tif in.LockFile {\n\t\tnote += \"The file is LOCKED.\"\n\t}\n"
	const row = "internal/service/thing.go:LockFile\tthe sentence is this server's own doing\n"

	out, err := outcomeTree(t, claim, row)
	if err != nil {
		t.Fatalf("an excused branch failed: %v\n%s", err, out)
	}
	if want := "outcome check ok (20 boolean inputs across 10 files, 1 branch(es) excused)\n"; out != want {
		t.Errorf("summary = %q, want %q", out, want)
	}

	out, err = outcomeTree(t, claim, row+"internal/service/thing.go:Notify\tnothing tests Notify\n")
	if err == nil || !strings.Contains(out, "thing.go:Notify and nothing there states an outcome") {
		t.Errorf("a stale row: err = %v, report:\n%s", err, out)
	}

	out, err = outcomeTree(t, claim+claim, row)
	if err == nil || !strings.Contains(out, "excuses internal/service/thing.go:LockFile once and 2 branches") {
		t.Errorf("one row for two branches: err = %v, report:\n%s", err, out)
	}

	out, err = outcomeTree(t, claim, "")
	if err == nil || !strings.Contains(out, "tests the request field LockFile") {
		t.Errorf("an unexcused branch: err = %v, report:\n%s", err, out)
	}
}

// TestTheRealServiceIsChecked runs the gate over this repository, and
// holds it to having read something: today 24 inputs across 23 files.
func TestTheRealServiceIsChecked(t *testing.T) {
	t.Chdir("../..")
	var out strings.Builder
	if err := outcomes(&out, nil); err != nil {
		t.Fatalf("outcomes: %v\n%s", err, out.String())
	}
	var inputs, files int
	if _, err := fmt.Sscanf(out.String(), "outcome check ok (%d boolean inputs across %d files", &inputs, &files); err != nil {
		t.Fatalf("cannot read the summary %q: %v", out.String(), err)
	}
	if inputs < 20 || files < 15 {
		t.Errorf("summary %q is under its floors of 20 inputs and 15 files", out.String())
	}
}

// claimsIn writes one service file and runs both passes over it, which
// is what the gate does.
func claimsIn(t *testing.T, source string) []claim {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	pkg := filepath.Join("internal", "service")
	if err := os.MkdirAll(pkg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "thing.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	files, err := parseService(fset)
	if err != nil {
		t.Fatal(err)
	}
	return outcomeClaims(files, boolInputFields(files), fset)
}

// TestTheRecordIsKeyedBySlashesOnEveryPlatform.
//
// The claim's path comes from filepath, so on Windows it arrives as
// `internal\service\drives.go` while the record file names it with
// forward slashes. Nothing matched: every excused branch was reported
// unexcused, and every row was reported stale, so the gate failed on a
// tree it passes on everywhere else.
//
// Linux and macOS cannot reproduce it — a test that only wrote a path
// and read it back would pass on both and prove nothing — so this asks
// the key function directly with the path Windows produces.
func TestTheRecordIsKeyedBySlashesOnEveryPlatform(t *testing.T) {
	const want = "internal/service/drives.go:IncludeHidden"
	for _, path := range []string{
		`internal\service\drives.go`,
		"internal/service/drives.go",
	} {
		if got := outcomeKey(path, "IncludeHidden"); got != want {
			t.Errorf("outcomeKey(%q) = %q, want %q", path, got, want)
		}
	}

	// And the committed record uses that spelling, so the two halves
	// cannot drift apart in the other direction either.
	t.Chdir("../..")
	exempt, err := readOutcomeExemptions()
	if err != nil {
		t.Fatal(err)
	}
	for key := range exempt {
		if strings.ContainsRune(key, '\\') {
			t.Errorf("%s names %q with a backslash; the record is keyed by slashes", outcomeFile, key)
		}
	}
}
