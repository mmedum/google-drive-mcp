package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// This gate turns the repository's first rule into something checkable.
// Nothing deployer-specific may enter it: no account addresses, no real
// file, folder or shared-drive ids, no links carrying one. gitleaks
// covers credentials; this covers everything else, which is the part a
// live run against a real Drive can drag in — a pasted transcript, a
// fixture copied out of a listing, an id in a commit message.
//
// It is deliberately blunt. A false positive costs an entry in the
// allowlist below, with a reason; a false negative costs somebody else
// their Drive.

// documentedDomains are the domains RFC 2606 reserves for documentation,
// plus the address git commits are attributed to. Anything else that
// looks like an address belongs to a real person.
var documentedDomains = map[string]bool{
	"example.com": true, "example.org": true, "example.net": true,
	"anthropic.com": true,
}

var (
	addressPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@([A-Za-z0-9.\-]+\.[A-Za-z]{2,})`)
	// A Drive id: base64url, 19 characters or more. Requiring a capital
	// and a digit keeps prose and kebab-case identifiers out of it.
	idPattern   = regexp.MustCompile(`[A-Za-z0-9_\-]{19,}={0,2}`)
	linkPattern = regexp.MustCompile(`https://(?:drive|docs)\.google\.com/\S+`)
	hasCapital  = regexp.MustCompile(`[A-Z]`)
	hasNumber   = regexp.MustCompile(`[0-9]`)
)

// skipFiles hold long opaque content that is not ours to police.
var skipFiles = map[string]bool{"go.sum": true, "go.mod": true}

// syntheticMarker is the convention that keeps the allowlist below from
// growing with every test: an id invented for a fixture says so in its
// own text. Drive issues ids from random bytes, so one cannot contain
// this word by chance, and a rule scales where a list of exceptions does
// not — every entry added to a list is a chance to add a real one.
const syntheticMarker = "Fixture"

// allowedIDs are id-shaped strings that must look convincingly real and
// therefore cannot carry the marker. Each carries its reason: an entry
// without one is how a gate like this quietly stops working.
var allowedIDs = map[string]string{
	"1AbC23dEfGh45iJkLmN67opQrStUvWxYz": "deliberately realistic id in the mistyped-id test, which is about ids that look real",
	// These predate the marker convention and survive in this
	// repository's history, which `leaks history` walks. They were
	// invented for tests and name nothing.
	"1NoSuchFileIdAAAAAAAAAAAAAAAAAAAAAA":          "fixture id in the history, before synthetic ids declared themselves",
	"0AZzyzxSyntheticDriveIdAAA":                   "fixture shared-drive id in the history, same",
	"1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms": "the id in Google's own published API documentation",
	"1ZzzMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms": "that documentation id with its head changed, to have a second value",
	"0AKl3lQ5UUqptUk9PVA":                          "the shared-drive id in Google's own published API documentation",
	"0ABcdEFghIJklMNop":                            "synthetic shared-drive id in the ref tests",
}

// leaks scans for anything account-specific. With "history" it scans
// every blob this repository has ever held, because a leak deleted from
// the tip is still in the log and still public the moment the repository
// is.
func leaks(out io.Writer, args []string) error {
	if arg(args, 0, "") == "history" {
		return leaksInHistory(out)
	}
	files, err := trackedFiles()
	if err != nil {
		return err
	}
	var found []string
	for _, path := range files {
		if skipFiles[filepath.Base(path)] {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if isBinary(body) {
			found = append(found, path+": a compiled binary is committed here. "+
				"Nothing built belongs in the repository, and its contents cannot be reviewed")
			continue
		}
		found = append(found, scanForLeaks(path, string(body))...)
	}
	if len(found) > 0 {
		for _, f := range found {
			_, _ = fmt.Fprintln(out, f)
		}
		return fmt.Errorf("%d thing(s) here look like they came from a real Drive", len(found))
	}
	_, _ = fmt.Fprintf(out, "leak check ok (%d files)\n", len(files))
	return nil
}

func scanForLeaks(path, body string) []string {
	var found []string
	for i, line := range strings.Split(body, "\n") {
		where := fmt.Sprintf("%s:%d", path, i+1)
		for _, m := range addressPattern.FindAllStringSubmatch(line, -1) {
			if !documentedDomains[strings.ToLower(m[1])] {
				found = append(found, where+": an address on a real domain: "+abbreviate(m[0]))
			}
		}
		for _, link := range linkPattern.FindAllString(line, -1) {
			if !allowedLink(link) {
				found = append(found, where+": a Drive link carrying an id")
			}
		}
		for _, loc := range idPattern.FindAllStringIndex(line, -1) {
			id := line[loc[0]:loc[1]]
			if !suspiciousID(id) || allowedIDs[id] != "" || looksLikeInfrastructure(line) {
				continue
			}
			if isCodeIdentifier(line, loc) {
				continue
			}
			found = append(found, where+": something shaped like a Drive id: "+abbreviate(id))
		}
	}
	return found
}

// suspiciousID reports whether a token has the shape of a Drive id
// rather than of a long word.
func suspiciousID(id string) bool {
	if strings.Contains(id, syntheticMarker) {
		return false
	}
	return hasCapital.MatchString(id) && hasNumber.MatchString(id)
}

// isCodeIdentifier reports whether the match is part of a program rather
// than a value: something reached through a dot, or called. A leaked id
// arrives as a literal or in prose, never as a selector.
func isCodeIdentifier(line string, loc []int) bool {
	if loc[0] > 0 && line[loc[0]-1] == '.' {
		return true
	}
	if loc[1] < len(line) && (line[loc[1]] == '(' || line[loc[1]] == '.') {
		return true
	}
	return false
}

// allowedLink permits a link whose every id-shaped part is allowed.
func allowedLink(link string) bool {
	for _, id := range idPattern.FindAllString(link, -1) {
		if suspiciousID(id) && allowedIDs[id] == "" {
			return false
		}
	}
	return true
}

// looksLikeInfrastructure skips the long opaque strings this repository
// has for its own reasons: pinned action digests and module checksums.
func looksLikeInfrastructure(line string) bool {
	return strings.Contains(line, "uses:") ||
		strings.Contains(line, "codeql-bundle") ||
		strings.Contains(line, "h1:")
}

// abbreviate shows enough of a finding to locate it without reprinting
// it in full into a terminal, a log, or CI output.
func abbreviate(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:4] + "…" + s[len(s)-2:]
}

// leaksInHistory walks every blob in every commit. It is slower than the
// working-tree scan and is not on the make check path; it is what runs
// before the repository is made public, and after anything is removed
// from it in a hurry.
func leaksInHistory(out io.Writer) error {
	revs, err := exec.Command("git", "rev-list", "--all", "--objects").Output()
	if err != nil {
		return fmt.Errorf("list objects: %w", err)
	}
	seen := map[string]bool{}
	var found []string
	scanned := 0
	for _, line := range strings.Split(strings.TrimSpace(string(revs)), "\n") {
		hash, path, ok := strings.Cut(line, " ")
		if !ok || path == "" || !isText(path) || skipFiles[filepath.Base(path)] || seen[hash] {
			continue
		}
		seen[hash] = true
		body, err := exec.Command("git", "cat-file", "-p", hash).Output()
		if err != nil {
			continue
		}
		scanned++
		if isBinary(body) {
			found = append(found, path+"@"+hash[:8]+": a compiled binary is in the history")
			continue
		}
		found = append(found, scanForLeaks(path+"@"+hash[:8], string(body))...)
	}
	if len(found) > 0 {
		for _, f := range found {
			_, _ = fmt.Fprintln(out, f)
		}
		return fmt.Errorf("%d thing(s) in this repository's history look like they came from a real Drive", len(found))
	}
	_, _ = fmt.Fprintf(out, "history leak check ok (%d blobs)\n", scanned)
	return nil
}

func trackedFiles() ([]string, error) {
	out, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("list tracked files: %w", err)
	}
	var files []string
	for _, name := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if name != "" && isText(name) {
			files = append(files, name)
		}
	}
	return files, nil
}

// isBinary reports whether the content is not text a person wrote. A NUL
// byte in the first few kilobytes is what git itself uses to decide.
func isBinary(body []byte) bool {
	head := body
	if len(head) > 8000 {
		head = head[:8000]
	}
	for _, b := range head {
		if b == 0 {
			return true
		}
	}
	return false
}

// isText keeps the scan to files a person could paste something into.
func isText(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".pdf", ".zip", ".gz", ".ico", ".woff", ".woff2":
		return false
	}
	return true
}
