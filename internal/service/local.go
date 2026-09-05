package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// localDir returns the one directory this server may read from and write
// to, with the symlinks resolved. Unset means no file transfer at all,
// which is the default: a server that can write anywhere on the machine
// is a different thing from a server that can talk to Drive.
func (s *Service) localDir() (string, error) {
	if s.opts.LocalDir == "" {
		return "", Errorf(ClassUnsupported, "this server cannot read or write local files: it was started "+
			"without GDRIVE_LOCAL_DIR. read_file returns a file's text without touching the disk; "+
			"setting GDRIVE_LOCAL_DIR to a directory turns download_file and upload_file on.")
	}
	dir, err := filepath.EvalSymlinks(s.opts.LocalDir)
	if err != nil {
		return "", Errorf(ClassInvalid, "the local directory this server was given does not exist or cannot be read: %s", err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", Errorf(ClassInvalid, "the local directory this server was given is not a directory")
	}
	return dir, nil
}

// localSource resolves a path given for an upload and refuses anything
// outside the local directory. The symlinks are resolved first: a link
// inside the directory pointing at the private key next to it would
// otherwise pass a check on the name alone.
func (s *Service) localSource(path string) (string, os.FileInfo, error) {
	dir, err := s.localDir()
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(path) == "" {
		return "", nil, Errorf(ClassInvalid, "local_path is required: name a file inside %s", dir)
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(dir, candidate)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, Errorf(ClassNotFound, "no file at %s. Uploads read from %s.", path, dir)
		}
		return "", nil, Errorf(ClassInvalid, "cannot read %s: %s", path, err)
	}
	if !within(dir, resolved) {
		return "", nil, Errorf(ClassForbidden, "%s is outside %s, the one directory this server may read. "+
			"Move the file there and try again.", path, dir)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, Errorf(ClassInvalid, "cannot read %s: %s", path, err)
	}
	if info.IsDir() {
		return "", nil, Errorf(ClassInvalid, "%s is a directory. Upload one file at a time.", path)
	}
	if !info.Mode().IsRegular() {
		return "", nil, Errorf(ClassInvalid, "%s is not a regular file", path)
	}
	return resolved, info, nil
}

// openLocal is what both upload paths need: the file open, its size, and
// its name. The checks and the measurement happen once, inside
// localSource, rather than being repeated by each caller.
func (s *Service) openLocal(path string) (*os.File, os.FileInfo, error) {
	resolved, info, err := s.localSource(path)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, nil, Errorf(ClassInvalid, "cannot open %s: %s", path, err)
	}
	return file, info, nil
}

// within reports whether path is dir itself or something inside it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// localTarget picks the file a download writes to: the file's name made
// safe, the short id so two files of one name do not collide, and the
// extension for the format. It never overwrites; a name already taken
// gains a counter, because the file that is already there belongs to
// whoever put it there.
func (s *Service) localTarget(name, id, ext string) (string, error) {
	dir, err := s.localDir()
	if err != nil {
		return "", err
	}
	base := safeName(name)
	if ext != "" {
		ext = "." + strings.TrimPrefix(ext, ".")
		// A name that already ends in this extension keeps it at the end,
		// where a file manager looks for it, rather than in the middle.
		base = strings.TrimSuffix(base, ext)
	}
	base += "-" + shortID(id)
	for n := 0; n < 100; n++ {
		candidate := filepath.Join(dir, base+ext)
		if n > 0 {
			candidate = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, n+1, ext))
		}
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		}
	}
	return "", Errorf(ClassExists, "a hundred files in %s are already named like %s%s", dir, base, ext)
}

// shortID is the id fragment a downloaded file's name carries, so two
// files of one name land beside each other rather than on each other.
// Drive ids use the URL-safe base64 alphabet, which is safe in a path.
func shortID(id string) string {
	if len(id) > 6 {
		return id[:6]
	}
	return id
}

// maxSafeNameRunes bounds a generated file name. Every file system in
// use limits a component to 255 bytes; a Drive name can be longer, and a
// name that long is unreadable anyway.
const maxSafeNameRunes = 80

// safeName turns a Drive name into one component of a path: no
// separators, no control characters, no leading dot, and short enough to
// live on any file system.
func safeName(name string) string {
	var b strings.Builder
	space := false
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == 0:
			b.WriteByte('-')
		case unicode.IsControl(r):
			// Dropped: a newline in a file name is a name that lies about
			// how many lines a listing has.
		case unicode.IsSpace(r):
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		default:
			b.WriteRune(r)
		}
		space = false
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimLeft(out, ".")
	// Windows refuses a component ending in a dot or a space.
	out = strings.TrimRight(out, ". ")
	if r := []rune(out); len(r) > maxSafeNameRunes {
		out = strings.TrimRight(string(r[:maxSafeNameRunes]), ". ")
	}
	if out == "" {
		return "file"
	}
	return out
}
