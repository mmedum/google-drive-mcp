package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// The record files. Three gates keep one — every API method and its
// verdict, every tool option the live driver does not send, every branch
// that states an outcome after testing a request field — and each had
// its own reader until the three had drifted.
//
// The drift was not cosmetic. One trimmed a trailing TAB before counting
// columns, so an empty final field failed as "wrong number of columns"
// rather than as the missing reason it was; one checked for a key listed
// twice and one did not, so a second row for the same key silently won.
// Both are decided here now, once.

// tsvRow is one record line: its fields, and the line it came from, so a
// failure can name a place a person can open.
type tsvRow struct {
	fields []string
	line   int
}

// readTSV reads a tab-separated record file, skipping blank lines and
// comments. cols is how many fields a row must carry, and key is the
// index of the field that names what the row is about, which must be
// unique.
//
// Problems are returned rather than raised, because a record file's
// mistakes are worth reporting together: a person editing one has
// usually made the same mistake on several lines.
func readTSV(path string, cols, key int) ([]tsvRow, []string) {
	f, err := os.Open(path) //nolint:gosec // a path this repository owns
	if err != nil {
		return nil, []string{"cannot read " + path + ": " + err.Error()}
	}
	defer func() { _ = f.Close() }()

	var rows []tsvRow
	var problems []string
	seen := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		// Spaces and a stray carriage return go; a trailing TAB does
		// not, because it is an empty last field. Trimming it turns "no
		// reason given" into "wrong number of columns", which is a worse
		// message for the commoner mistake.
		line := strings.TrimRight(scanner.Text(), " \r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != cols {
			problems = append(problems, fmt.Sprintf("%s:%d: want %d tab-separated columns, got %d",
				path, n, cols, len(fields)))
			continue
		}
		if name := fields[key]; seen[name] {
			problems = append(problems, fmt.Sprintf("%s:%d: %s is listed twice", path, n, name))
		} else {
			seen[name] = true
		}
		rows = append(rows, tsvRow{fields: fields, line: n})
	}
	if err := scanner.Err(); err != nil {
		problems = append(problems, path+": "+err.Error())
	}
	return rows, problems
}
