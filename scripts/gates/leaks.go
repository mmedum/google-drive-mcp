package main

import (
	"fmt"
	"io"
	"net/url"
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

// isDocumented reports whether a host is one RFC 2606 reserves, or sits
// under one. The RFC reserves example.com, example.org and example.net
// and everything beneath them, so someone@corp.example.net is exactly as
// safe as someone@example.net — and nobody can register the subdomain to
// make it otherwise.
//
// It is a suffix rule rather than three more map entries because a
// fixture wanting to read as "a different organisation" will reach for a
// subdomain again, and the alternative is weakening the fixture to suit
// the gate. Which is backwards: this one bit while a test was being
// written to close a real leak.
func isDocumented(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for documented := range documentedDomains {
		if host == documented || strings.HasSuffix(host, "."+documented) {
			return true
		}
	}
	return false
}

var (
	addressPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@([A-Za-z0-9.\-]+\.[A-Za-z]{2,})`)
	// A Drive id: base64url, 19 characters or more. Requiring a capital
	// and a digit keeps prose and kebab-case identifiers out of it.
	idPattern = regexp.MustCompile(`[A-Za-z0-9_\-]{19,}={0,2}`)
	// Candidate URLs only. Which host a URL is actually for is decided by
	// parsing it, not by matching text: a pattern that looks like a host
	// check invites every trick that has ever been played on one
	// (userinfo before the @, a lookalike suffix, a case difference).
	// internal/gapi settles the same question the same way.
	urlPattern = regexp.MustCompile(`https?://[^\s"'` + "`" + `<>)\]]+`)
	hasCapital = regexp.MustCompile(`[A-Z]`)
	hasNumber  = regexp.MustCompile(`[0-9]`)
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
	"1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms": "an id Google publishes in its own developer documentation",
	"1ZzzMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms": "that documentation id with its head changed, to have a second value",
	"0AKl3lQ5UUqptUk9PVA":                          "a shared-drive id Google publishes in its own developer documentation",
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
	// "I found nothing" and "I had nowhere to look" print the same
	// sentence unless one of them refuses to. A gate run from the wrong
	// directory, or after the file listing stops working, would otherwise
	// report a clean tree for ever.
	if len(files) == 0 {
		return fmt.Errorf("no files were scanned: the scan found nothing because it looked at nothing")
	}
	_, _ = fmt.Fprintf(out, "leak check ok (%d files)\n", len(files))
	return nil
}

// The floor is zero rather than a count, because the thing being guarded
// against is "nowhere to look", not "a small repository". A count picked
// to suit this tree fails the gate's own tests, which run it against a
// two-commit fixture — and a gate that has to be exempted in tests is
// one nobody trusts in production. Zero is the boundary that actually
// separates "found nothing" from "looked at nothing".

func scanForLeaks(path, body string) []string {
	var found []string
	for i, line := range strings.Split(body, "\n") {
		where := fmt.Sprintf("%s:%d", path, i+1)
		for _, m := range addressPattern.FindAllStringSubmatch(line, -1) {
			if !isDocumented(m[1]) {
				found = append(found, where+": an address on a real domain: "+abbreviate(m[0]))
			}
		}
		for _, candidate := range urlPattern.FindAllString(line, -1) {
			if isDriveLink(candidate) && !allowedLink(candidate) {
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

// driveHosts are the hosts whose URLs carry a file id in the path.
var driveHosts = map[string]bool{
	"drive.google.com": true,
	"docs.google.com":  true,
}

// isDriveLink parses the candidate and asks what host it is really for.
// Anything unparseable is not a link.
func isDriveLink(candidate string) bool {
	u, err := url.Parse(candidate)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return driveHosts[host]
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

// leaksInHistory walks every blob and every message in every commit. It
// is slower than the working-tree scan and is not on the make check
// path; it is what runs before the repository is made public, and after
// anything is removed from it in a hurry.
//
// What it scans is deliberate. A blob is scanned whole. A commit or tag
// is scanned from its message down, because the lines above it are the
// author, committer and tagger that git writes itself: an identity is
// public in every commit of every repository by construction, and
// scanning it made this check fail on its own tags and pass on nothing
// — a gate that always fails is a gate that gets ignored.
func leaksInHistory(out io.Writer) error {
	revs, err := exec.Command("git", "rev-list", "--all", "--objects").Output()
	if err != nil {
		return fmt.Errorf("list objects: %w", err)
	}
	seen := map[string]bool{}
	var found []string
	blobs, messages := 0, 0

	for _, line := range strings.Split(strings.TrimSpace(string(revs)), "\n") {
		hash, path, _ := strings.Cut(line, " ")
		if hash == "" || seen[hash] {
			continue
		}
		seen[hash] = true
		kind, body, err := object(hash)
		if err != nil {
			continue
		}
		switch kind {
		case "blob":
			if path == "" || !isText(path) || skipFiles[filepath.Base(path)] {
				continue
			}
			blobs++
			where := path + "@" + hash[:8]
			if isBinary(body) {
				found = append(found, where+": a compiled binary is in the history")
				continue
			}
			found = append(found, scanForLeaks(where, string(body))...)
		case "commit", "tag":
			messages++
			found = append(found, scanForLeaks(kind+" "+hash[:8], gitMessage(string(body)))...)
		}
	}

	if len(found) > 0 {
		for _, f := range found {
			_, _ = fmt.Fprintln(out, f)
		}
		return fmt.Errorf("%d thing(s) in this repository's history look like they came from a real Drive", len(found))
	}
	if blobs == 0 || messages == 0 {
		return fmt.Errorf("scanned %d blobs and %d messages: a repository has at least one of each, so "+
			"the scan found nothing because it looked at nothing", blobs, messages)
	}
	_, _ = fmt.Fprintf(out, "history leak check ok (%d blobs, %d messages)\n", blobs, messages)
	return nil
}

// object reads one git object's type and contents.
func object(hash string) (kind string, body []byte, err error) {
	t, err := exec.Command("git", "cat-file", "-t", hash).Output()
	if err != nil {
		return "", nil, err
	}
	body, err = exec.Command("git", "cat-file", "-p", hash).Output()
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(string(t)), body, nil
}

// gitMessage is what a person wrote in a commit or tag: everything after
// the headers git generates. Those headers carry the author, committer
// and tagger identities, which are not content anyone chose to publish
// here — they are how git records who made a commit, in every repository
// there is.
func gitMessage(body string) string {
	if _, message, ok := strings.Cut(body, "\n\n"); ok {
		return message
	}
	return ""
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
