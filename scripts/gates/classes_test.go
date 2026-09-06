package main

import (
	"strings"
	"testing"
)

// TestTheDeclaredValuesAreReadNotDerived is the shape the gate now
// depends on: internal/gapi/errors.go says what each constant holds, and
// the gate reads it rather than guessing from the name. Two values here
// cannot be derived by any convention — ClassAmbiguousIO holds
// "ambiguous_outcome" — which is the reason the first draft needed
// hardcoded exceptions and this one does not.
func TestTheDeclaredValuesAreReadNotDerived(t *testing.T) {
	t.Chdir("../..")
	declared, err := declaredClasses()
	if err != nil {
		t.Fatalf("declaredClasses: %v", err)
	}
	for constant, want := range map[string]string{
		"ClassNotFound":    "not_found",
		"ClassAmbiguousIO": "ambiguous_outcome",
		"ClassAmbiguous":   "ambiguous",
	} {
		if got := declared[constant]; got != want {
			t.Errorf("%s = %q, want %q", constant, got, want)
		}
	}
	if _, ok := declared["Classes"]; ok {
		t.Error("Classes is the list, not a member of it")
	}
}

func TestTheClassGateReadsThisRepository(t *testing.T) {
	// Run from the repository root, as make check runs it.
	t.Chdir("../..")
	var out strings.Builder
	if err := classes(&out, nil); err != nil {
		t.Fatalf("classes: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "class check ok") {
		t.Errorf("output = %q", out.String())
	}
}

// TestTheClassGateRefusesToPassWithNothingToRead is the floor. A gate
// run where there is no internal/ is the same shape as one run after the
// walk quietly stops working, and both must fail rather than report a
// clean tree.
func TestTheClassGateRefusesToPassWithNothingToRead(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := classes(&strings.Builder{}, nil); err == nil {
		t.Error("the gate passed with nothing to read")
	}
}

func TestEveryDeclaredClassIsEmittedAndTheOtherWayRound(t *testing.T) {
	t.Chdir("../..")
	declared, err := declaredClasses()
	if err != nil {
		t.Fatalf("declaredClasses: %v", err)
	}
	if len(declared) < 10 {
		t.Fatalf("only %d classes were read out of internal/gapi/errors.go", len(declared))
	}
	emitted, files, err := emittedClasses(declared)
	if err != nil {
		t.Fatalf("emittedClasses: %v", err)
	}
	if files < 10 {
		t.Fatalf("only %d files were read under internal/", files)
	}
	values := map[string]bool{}
	for constant, value := range declared {
		values[value] = true
		if _, ok := emitted[value]; !ok {
			t.Errorf("the class %q (%s) is declared and nothing emits it", value, constant)
		}
	}
	// ambiguous and ambiguous_outcome are two different instructions to
	// the caller — choose between candidates, or go and find out what
	// happened — and a vocabulary that lost one of them would still pass
	// every check above.
	for _, want := range []string{"ambiguous", "ambiguous_outcome"} {
		if !values[want] {
			t.Errorf("the vocabulary no longer has %q", want)
		}
	}
}
