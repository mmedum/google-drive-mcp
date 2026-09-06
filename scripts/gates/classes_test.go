package main

import (
	"os"
	"path/filepath"
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

// TestThePublishedVocabularyMatchesTheConstants. gapi.Classes() is a
// third list — what `doctor` prints and what a model is told the
// vocabulary is — and until a sibling repository reported the same class
// of hole in its own gate, nothing here read it.
//
// The duplicate case is the one worth watching. Their check used
// slices.Compact, which removes only ADJACENT equals, so a class written
// twice anywhere but beside itself passed. Both fixtures here put the
// repeat at the far end of the list.
func TestThePublishedVocabularyMatchesTheConstants(t *testing.T) {
	const good = `package gapi

const (
	ClassAuth     = "auth"
	ClassNotFound = "not_found"
	ClassServer   = "server"
)

func Classes() []string {
	return []string{ClassAuth, ClassNotFound, ClassServer}
}
`
	cases := []struct {
		name, source, want string
	}{
		{name: "the list and the constants agree", source: good},
		{
			name: "a class the list forgets",
			source: strings.Replace(good, "ClassAuth, ClassNotFound, ClassServer}",
				"ClassAuth, ClassNotFound}", 1),
			want: "ClassServer is a declared class and gapi.Classes() does not list it",
		},
		{
			name: "a class listed twice, at the other end of the list",
			source: strings.Replace(good, "ClassAuth, ClassNotFound, ClassServer}",
				"ClassAuth, ClassNotFound, ClassServer, ClassAuth}", 1),
			want: "gapi.Classes() lists ClassAuth 2 times",
		},
		{
			name: "a name in the list that is not a class",
			source: strings.Replace(good, "ClassAuth, ClassNotFound, ClassServer}",
				"ClassAuth, ClassNotFound, ClassServer, ClassGone}", 1),
			want: "ClassGone, which is not a declared class",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if err := os.MkdirAll(filepath.Join("internal", "gapi"), 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("internal", "gapi", "errors.go")
			if err := os.WriteFile(path, []byte(c.source), 0o600); err != nil {
				t.Fatal(err)
			}
			declared, err := declaredClasses()
			if err != nil {
				t.Fatal(err)
			}
			problems, err := listedClasses(declared)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(problems, "\n")
			if c.want == "" {
				if len(problems) > 0 {
					t.Fatalf("a matching pair was refused:\n%s", joined)
				}
				return
			}
			if len(problems) == 0 {
				t.Fatal("the divergence was accepted")
			}
			if !strings.Contains(joined, c.want) {
				t.Errorf("the report does not say %q:\n%s", c.want, joined)
			}
		})
	}
}
