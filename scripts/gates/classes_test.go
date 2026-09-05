package main

import (
	"strings"
	"testing"
)

func TestClassValueFollowsTheNamingConvention(t *testing.T) {
	cases := map[string]string{
		"ClassNotFound":    "not_found",
		"ClassAuth":        "auth",
		"ClassRateLimited": "rate_limited",
		// The one the convention cannot derive: the constant is named
		// after the kind of failure and the value after what the caller
		// should do about it.
		"ClassAmbiguousIO": "ambiguous_outcome",
		// Not classes. "Classes" is the list, and reading it as a class
		// called "es" is what would fail this gate the day somebody
		// joined gapi.Classes() into a tool description.
		"Classes":  "",
		"Class":    "",
		"NotAName": "",
	}
	for constant, want := range cases {
		if got := classValue(constant); got != want {
			t.Errorf("classValue(%q) = %q, want %q", constant, got, want)
		}
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
	emitted, files, err := emittedClasses()
	if err != nil {
		t.Fatalf("emittedClasses: %v", err)
	}
	if files < 10 {
		t.Fatalf("only %d files were read under internal/", files)
	}
	// Both directions, said plainly, so a failure here reads as what it
	// is rather than as a count mismatch.
	for name := range declared {
		if _, ok := emitted[name]; !ok {
			t.Errorf("the class %q is declared and nothing emits it", name)
		}
	}
	for name, where := range emitted {
		if !declared[name] {
			t.Errorf("the class %q is emitted at %s and is not declared", name, where)
		}
	}
	// ambiguous and ambiguous_outcome are two different instructions to
	// the caller — choose between candidates, or go and find out what
	// happened — and a vocabulary that lost one of them would still pass
	// every check above.
	for _, want := range []string{"ambiguous", "ambiguous_outcome"} {
		if !declared[want] {
			t.Errorf("the vocabulary no longer has %q", want)
		}
	}
}
