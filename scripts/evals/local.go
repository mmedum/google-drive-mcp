package main

import (
	"os"
	"path/filepath"
)

// The two tasks that move bytes need the local directory the server is
// confined to, which is the harness's own temporary directory: the
// transfer tools read from and write to it and nowhere else.

func writeLocal(s *taskState, name, content string) error {
	return os.WriteFile(filepath.Join(s.dir, name), []byte(content), 0o600)
}

func localExists(s *taskState, name string) bool {
	_, err := os.Stat(filepath.Join(s.dir, name))
	return err == nil
}
