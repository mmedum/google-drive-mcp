package main

import (
	"strings"
	"testing"
)

// TestTheSnapshotAndTheRecordAgree runs the offline half over the real
// files. Completeness used to rest on `gates api-diff`, which is manual
// and needs the network, so CONTRIBUTING claimed this record was held to
// the API while CI held no such thing.
func TestTheSnapshotAndTheRecordAgree(t *testing.T) {
	t.Chdir("../..")
	entries, problems := readCoverage()
	if len(problems) > 0 {
		t.Fatalf("%s", strings.Join(problems, "\n"))
	}
	if got := checkAgainstSnapshot(entries); len(got) > 0 {
		t.Errorf("%s", strings.Join(got, "\n"))
	}
}

// TestTheSnapshotIsMachineWritten. Its two fields are the ones a person
// used to type into the record and nothing read; if they are empty here,
// the split has bought nothing.
func TestTheSnapshotIsMachineWritten(t *testing.T) {
	t.Chdir("../..")
	snap, err := readSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Methods) < 50 {
		t.Fatalf("the snapshot lists %d methods; that is too few to be the real one", len(snap.Methods))
	}
	if snap.Fetched == "" {
		t.Error("the snapshot carries no fetch date, so nothing says how old it is")
	}
	for name, m := range snap.Methods {
		if m.Verb == "" || m.Path == "" {
			t.Errorf("%s has verb %q and path %q; the fields the record dropped must be real here",
				name, m.Verb, m.Path)
		}
	}
}
