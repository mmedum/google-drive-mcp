package main

import (
	"strings"
	"testing"
)

// TestTheSnapshotAndTheRecordAgree runs the offline half over the real
// files, which is what holds the record to the API in `make check`
// rather than only in the manual, networked `gates api-diff`.
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

// TestTheSnapshotIsMachineWritten. Its verb and path are the fields the
// record leaves to the snapshot; if they are empty here, nothing holds
// them.
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
