package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestTheLiveCoverRecordIsWellFormed reads the file the gate reads, so a
// row with a missing reason or an invented verdict fails here as well as
// in CI, where the failure costs a build.
func TestTheLiveCoverRecordIsWellFormed(t *testing.T) {
	t.Chdir("../..")
	recorded, err := readLiveCover()
	if err != nil {
		t.Fatalf("%s: %v", liveCoverFile, err)
	}
	if len(recorded) < 20 {
		t.Errorf("%s lists %d options; the measurement found 66", liveCoverFile, len(recorded))
	}
	for name, entry := range recorded {
		if !strings.Contains(name, ".") {
			t.Errorf("%s:%d: %q is not tool.option", liveCoverFile, entry.line, name)
		}
	}
}

// TestARecordThatIsNotADecisionIsRefused. "Not sent" without a reason is
// not a decision, and a verdict this gate does not know would lose the
// difference between a gap somebody could close this afternoon and one
// that needs a second person.
func TestARecordThatIsNotADecisionIsRefused(t *testing.T) {
	for _, c := range []struct{ name, row, want string }{
		{"no reason", "get_file.include_labels\tundriven\t", "not a decision"},
		{"an invented verdict", "get_file.include_labels\tlater\tsomebody will", "undrivable or undriven"},
		{"a row that is not three fields", "get_file.include_labels\tundriven", "three tab-separated"},
		{"the same option twice",
			"get_file.include_labels\tundriven\tone\nget_file.include_labels\tundrivable\ttwo", "listed twice"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if err := os.MkdirAll("testdata", 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(liveCoverFile, []byte("# a record\n\n"+c.row+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := readLiveCover()
			if err == nil {
				t.Fatalf("%q was accepted", c.row)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the refusal does not say %q: %v", c.want, err)
			}
		})
	}
}

// TestAnEmptyRecordIsRefused, because a file listing no decisions passes
// every check there is and says nothing.
func TestAnEmptyRecordIsRefused(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll("testdata", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(liveCoverFile, []byte("# nothing but a header\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLiveCover(); err == nil {
		t.Error("a record with no rows was accepted")
	}
}

// TestTheDriverIsMeasuredAgainstTheBinaryItDrives runs the gate itself.
// It needs the built binary, which `make check` has by the time it runs
// this and a bare `go test` may not.
func TestTheDriverIsMeasuredAgainstTheBinaryItDrives(t *testing.T) {
	t.Chdir("../..")
	if _, err := os.Stat("./google-drive-mcp"); err != nil {
		t.Skip("no built binary to publish a schema; `make build` first")
	}
	var out bytes.Buffer
	if err := liveCover(&out, []string{"./google-drive-mcp"}); err != nil {
		t.Fatalf("live cover: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "options driven") {
		t.Errorf("the gate reports no number: %s", out.String())
	}
}
