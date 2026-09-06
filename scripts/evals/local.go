package main

import (
	"os"
	"path/filepath"
	"strings"
)

// The two tasks that move bytes need the local directory the server is
// confined to, which is the harness's own temporary directory: the
// transfer tools read from and write to it and nowhere else.

func writeLocal(s *taskState, name, content string) error {
	return os.WriteFile(filepath.Join(s.dir, name), []byte(content), 0o600)
}

// localMatching lists what landed in the local directory whose name
// carries stem. A download does not write the name Drive holds: it
// appends a short id, so two files of one name land beside each other
// rather than on each other, and a check that looked for the exact name
// failed a download that had worked.
func localMatching(s *taskState, stem string) []string {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.Contains(e.Name(), stem) {
			out = append(out, e.Name())
		}
	}
	return out
}
