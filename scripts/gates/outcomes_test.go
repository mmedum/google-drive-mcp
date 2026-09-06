package main

import (
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

// TestAnExemptionThatNoLongerMatchesFails, so an excuse cannot outlive
// the code it excused and read as a decision about today's code.
func TestAnExemptionThatNoLongerMatchesFails(t *testing.T) {
	t.Chdir("../..")
	exempt, err := readOutcomeExemptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(exempt) == 0 {
		t.Skip("nothing is excused, so there is nothing to go stale")
	}
	fset := token.NewFileSet()
	files, err := parseService(fset)
	if err != nil {
		t.Fatal(err)
	}
	claims := outcomeClaims(files, boolInputFields(files), fset)
	matched := map[string]bool{}
	for _, c := range claims {
		matched[c.file+":"+c.field] = true
	}
	for key := range exempt {
		if !matched[key] {
			t.Errorf("%s is excused and nothing there states an outcome from the request", key)
		}
	}
}

// TestTheRealServiceIsChecked runs the gate over this repository.
func TestTheRealServiceIsChecked(t *testing.T) {
	t.Chdir("../..")
	var out strings.Builder
	if err := outcomes(&out, nil); err != nil {
		t.Fatalf("outcomes: %v\n%s", err, out.String())
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
