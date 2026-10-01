package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The samples below are assembled at run time rather than written out.
// A leak-shaped literal in this file would make the gate flag its own
// test, and the fix for that is always to allowlist the file — after
// which the file is no longer covered.
func sample(parts ...string) string { return strings.Join(parts, "") }

func TestCatchesWhatALiveRunCanDragIn(t *testing.T) {
	var (
		id      = sample("1a2B3c4D5e", "6F7g8H9i0J", "kLmNoPqRsTuVw")
		driveID = sample("0AL1qP2r", "S3tU4vW5xY6z")
		address = sample("someone@", "a-real-", "company.com")
		link    = sample("https://drive.google.com/file/d/", id, "/view")
		docLink = sample("https://docs.google.com/document/d/", id, "/edit")
	)
	const (
		isAddress = "an address on a real domain"
		isLink    = "a Drive link carrying an id"
		isID      = "something shaped like a Drive id"
	)
	// Each of these is something a real session actually produced. The
	// gate exists because a person pasting a transcript into a fixture,
	// a doc or a commit message will not notice any of them. Each names
	// the rule that must fire, since the id rule alone would catch most.
	cases := []struct{ name, line, rule string }{
		{"a colleague's address", "modified 2026-09-04 by Someone <" + address + ">", isAddress},
		{"a file id in a fixture", `s.AddFile("` + id + `", "x", "text/plain", "")`, isID},
		{"a shared-drive id", "drive: " + driveID, isID},
		{"a link with an id in it", "link: " + link, isLink},
		{"a docs link in a comment", "// see " + docLink, isLink},
		// The leak is the second match of its kind on the line: a scan
		// that stopped at the first would pass every one of these.
		{"a real address after a documented one", "cc: person@example.com, " + address, isAddress},
		{"a drive link after a harmless url", "see https://example.com/a and " + link, isLink},
		{"an id after a selector", "x.SomeLongSelectorName2026() " + id, isID},
		{"a link whose second id is real", sample("https://drive.google.com/file/d/1SyntheticFixtureFileIdAAAAAAAAAAAA",
			"/view?resourcekey=0-", id), isLink},
	}
	for _, c := range cases {
		got := scanForLeaks("some/file.go", c.line)
		if !strings.Contains(strings.Join(got, "\n"), c.rule) {
			t.Errorf("%s: %q went undetected as %q; findings: %q", c.name, c.line, c.rule, got)
		}
	}
}

func TestLeavesTheRepositoryAlone(t *testing.T) {
	// Things that are in this repository on purpose. A gate that cries
	// wolf gets switched off, so these matter as much as the catches.
	cases := map[string]string{
		"a documentation address":  "owner: A Person <person@example.com>",
		"the commit trailer":       "Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>",
		"a synthetic fixture id":   `{"file":"1SyntheticFixtureFileIdAAAAAAAAAAAA"}`,
		"an id from Google's docs": "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms", // allowlisted, with its reason
		"a pinned action":          "      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1",
		"a module checksum":        "golang.org/x/oauth2 v0.36.0 h1:" + sample("aBcD1234eF", "gHiJkLmNoPqRsTuVwXyZ0123456789abc="),
		"a Go selector":            "oauth2.S256ChallengeOption(verifier),",
		"a kebab-case identifier":  "id-projects-fixture and id-drive-marketing",
		"an md5 in a test":         `drivetest.Size(4096, "d41d8cd98f00b204e9800998ecf8427e")`,
		"a plain sentence":         "the folder holds Budget.xlsx, Meeting notes, and more",
		"a link to a synthetic id": "https://drive.google.com/file/d/1SyntheticFixtureFileIdAAAAAAAAAAAA/view",
		"a field selector":         "timeout := cfg.HTTPTimeoutForEverything2026, nil",
	}
	for name, line := range cases {
		if got := scanForLeaks("some/file.go", line); len(got) != 0 {
			t.Errorf("%s was flagged: %q -> %v", name, line, got)
		}
	}
}

// A finding names the file and the line it is on.
func TestAFindingSaysWhereItIs(t *testing.T) {
	address := sample("someone@", "a-real-", "company.com")
	got := scanForLeaks("some/file.go", "line one\nline two "+address+"\n")
	if len(got) != 1 || !strings.HasPrefix(got[0], "some/file.go:2: ") {
		t.Errorf("findings = %q, want one starting %q", got, "some/file.go:2: ")
	}
}

func TestFindingsDoNotReprintTheSecret(t *testing.T) {
	// The report goes to a terminal and to CI output, so it says enough
	// to find the thing and no more.
	address := sample("someone@", "a-real-", "company.com")
	got := scanForLeaks("f.go", address)
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	if strings.Contains(got[0], address) {
		t.Errorf("the finding reprinted the address in full: %s", got[0])
	}
}

func TestEveryAllowedIDCarriesAReason(t *testing.T) {
	// An entry without a reason is how a gate like this quietly stops
	// working: someone allowlists a real id to make the build pass.
	for id, reason := range allowedIDs {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("allowedIDs[%q] has no reason", id)
		}
	}
}

func TestSyntheticIdsSaySoInTheirOwnText(t *testing.T) {
	// The convention that keeps the allowlist from growing: an id
	// invented for a fixture carries the marker, so no exception has to
	// be written down for it. Drive builds ids from random bytes and
	// cannot produce this word by chance.
	invented := "1Zzyzx" + syntheticMarker + "FileIdAAAAAAA"
	if got := scanForLeaks("f.go", invented); len(got) != 0 {
		t.Errorf("a self-declaring fixture id was flagged: %v", got)
	}
	// The same id without the marker is indistinguishable from a real
	// one, and is treated as real.
	// Assembled, like the other samples: a literal here would make the
	// gate flag its own test.
	real := sample("1Zzyzx", "SomethingFileId", "AAAAAAAAAAAA")
	if got := scanForLeaks("f.go", real); len(got) == 0 {
		t.Errorf("an id with no marker should be treated as real: %q", real)
	}
	// The marker cannot be used to smuggle anything else through.
	address := sample("someone@", "a-real-", "company.com")
	if got := scanForLeaks("f.go", syntheticMarker+" "+address); len(got) == 0 {
		t.Error("the marker must not exempt an address")
	}
}

func TestLinkDetectionAsksWhatHostAURLIsReallyFor(t *testing.T) {
	id := sample("1a2B3c4D5e", "6F7g8H9i0J", "kLmNoPqRsTuVw")
	// A host that merely starts with Drive's is a different host, and
	// nothing there belongs to anybody's Drive. Parsing settles it;
	// a pattern that matched the host as text would not.
	for _, notALink := range []string{
		"https://drive.google.com.example.invalid/file/d/" + id,
		"https://notdrive.google.com/file/d/" + id,
	} {
		for _, f := range scanForLeaks("f.go", notALink) {
			if strings.Contains(f, "Drive link") {
				t.Errorf("matched a lookalike host: %q -> %s", notALink, f)
			}
		}
	}
	// A real link embedded in a longer token still counts. This is a
	// detector, not a validator: the id is in the text either way, and
	// missing it because of a stray character before the scheme would be
	// the wrong way to be wrong.
	embedded := "see(https://drive.google.com/file/d/" + id + "/view)"
	var sawEmbedded bool
	for _, f := range scanForLeaks("f.go", embedded) {
		if strings.Contains(f, "Drive link") {
			sawEmbedded = true
		}
	}
	if !sawEmbedded {
		t.Error("a link embedded in surrounding text went undetected")
	}
	// The real thing still matches, wherever it sits in the line.
	for _, link := range []string{
		"https://drive.google.com/file/d/" + id + "/view",
		`link: "https://docs.google.com/document/d/` + id + `/edit"`,
	} {
		var sawLink bool
		for _, f := range scanForLeaks("f.go", link) {
			if strings.Contains(f, "Drive link") {
				sawLink = true
			}
		}
		if !sawLink {
			t.Errorf("a real link went undetected: %q", link)
		}
	}
}

// gitRepo makes a repository with one commit and one annotated tag, both
// carrying the identity given, and returns its directory.
func gitRepo(t *testing.T, identity, message string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=A Tester", "GIT_AUTHOR_EMAIL="+identity,
			"GIT_COMMITTER_NAME=A Tester", "GIT_COMMITTER_EMAIL="+identity,
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("nothing to see\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", message)
	run("tag", "-a", "v1.0.0", "-m", message)
	return dir
}

func TestHistoryScansMessagesAndNotTheIdentitiesGitWrites(t *testing.T) {
	// The same address in two places, with two verdicts. In the header it
	// is how git records who made the commit — public in every repository
	// by construction, and unremovable without rewriting every commit. In
	// the message it is something a person typed, which is exactly what
	// this gate is for.
	//
	// Assembled rather than written out, like every other sample here: a
	// leak-shaped literal in this file makes the gate flag its own test,
	// and the fix for that is always to allowlist the file, after which
	// the file is no longer covered. The comment at the top of this file
	// says so, and I wrote the literal anyway; the gate caught it.
	identity := sample("a.tester", "@", "gmail", ".com")

	t.Run("an identity header is not a leak", func(t *testing.T) {
		t.Chdir(gitRepo(t, identity, "Add a file\n\nNothing disclosing here.\n"))
		var out strings.Builder
		if err := leaksInHistory(&out); err != nil {
			t.Errorf("the gate failed on git's own identity records: %v\n%s", err, out.String())
		}
	})

	t.Run("an address in a message is", func(t *testing.T) {
		t.Chdir(gitRepo(t, identity, "Add a file\n\nReported by "+identity+" from their Drive.\n"))
		var out strings.Builder
		err := leaksInHistory(&out)
		if err == nil {
			t.Fatalf("an address written into a commit message was not caught:\n%s", out.String())
		}
		if !strings.Contains(out.String(), "commit ") {
			t.Errorf("the report does not say which commit: %s", out.String())
		}
	})

	t.Run("an id in a message is", func(t *testing.T) {
		// Id-shaped, with the capital and the digit the pattern wants, and
		// without the marker that says a value was invented for a test.
		id := sample("1BhX8kk0TbMD", "8KXojnKfZ6b7", "vTAVNz3ni")
		t.Chdir(gitRepo(t, identity, "Add a file\n\nSeen on "+id+" today.\n"))
		var out strings.Builder
		if err := leaksInHistory(&out); err == nil {
			t.Fatalf("an id in a commit message was not caught:\n%s", out.String())
		}
	})
}

// TestASubdomainOfADocumentedDomainIsDocumented is RFC 2606 read
// properly: it reserves example.com, .org and .net and everything under
// them. The exact-match rule flagged someone@corp.example.net while a
// test was being written to close a real leak, which is the wrong way
// round — a gate that makes fixtures weaker is a gate working against
// its own purpose.
func TestASubdomainOfADocumentedDomainIsDocumented(t *testing.T) {
	for _, safe := range []string{
		"example.com", "example.net", "example.org",
		"corp.example.net", "mail.corp.example.com", "anthropic.com",
	} {
		if !isDocumented(safe) {
			t.Errorf("isDocumented(%q) = false, and RFC 2606 reserves it", safe)
		}
	}
	// The suffix must be a label boundary: notexample.net and
	// example.net.example-of-a-real-host.com are somebody's.
	for _, real := range []string{
		"notexample.net", "example.net.co", "myexample.com", "example.company.com", "",
	} {
		if isDocumented(real) {
			t.Errorf("isDocumented(%q) = true, and it is a host somebody could own", real)
		}
	}
}

// TestAFileNobodyHasStagedIsStillScanned. `git ls-files` lists the
// INDEX, so the working-tree scan used to be blind to a file nobody had
// staged yet — and a phase's new files are precisely the ones nobody has
// scanned before. `make check` would go green all afternoon over the
// last phase's files while this phase's fixtures went unread, and the
// leak would arrive with the commit that finally staged them, in front
// of whoever was trying to push.
//
// A sibling repository found the same hole in its own copy the same
// week, and found a real fixture id with the fix.
func TestAFileNobodyHasStagedIsStillScanned(t *testing.T) {
	dir := gitRepo(t, sample("a.tester", "@", "example", ".com"), "Add a file\n")
	// Assembled rather than written out, for the reason at the top of
	// this file: a leak-shaped literal here makes the gate flag its own
	// test.
	id := sample("1QwErTyUiOp2", "AsDfGhJkL3", "ZxCvBnM4", "pLmNbVcXz")
	if err := os.WriteFile(filepath.Join(dir, "unstaged.md"), []byte("an id: "+id+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var out strings.Builder
	if err := leaks(&out, nil); err == nil {
		t.Errorf("a file nobody has staged was not scanned:\n%s", out.String())
	}

	// And .gitignore is still kept: an ignored build output is not
	// something to scan, and keeping it out of the tree is what the
	// ignore file is for.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("unstaged.md\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := leaks(&out, nil); err != nil {
		t.Errorf("an ignored file was scanned: %v\n%s", err, out.String())
	}
}

// TestACompiledBinaryIsAFindingAndNotASkip.
//
// Three sessions across sibling repositories concluded from reading this
// gate that a compiled artifact is invisible to it — that a content
// scanner cannot see a binary by construction, so only .gitignore stands
// between a build output and the history. All three were wrong here, and
// all three found out the same way: by building one and watching the
// gate name it. Nothing in this file had ever run that check, which is
// what left the question to be settled by reading.
//
// A NUL byte in the first few kilobytes is what git itself uses to
// decide, so the fixture needs no real executable.
func TestACompiledBinaryIsAFindingAndNotASkip(t *testing.T) {
	dir := gitRepo(t, sample("a.tester", "@", "example", ".com"), "Add a file\n")
	body := append([]byte("\x7fELF\x00\x00\x00"), make([]byte, 4096)...)
	if err := os.WriteFile(filepath.Join(dir, "built"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var out strings.Builder
	err := leaks(&out, nil)
	if err == nil {
		t.Fatalf("a compiled artifact in the tree was accepted:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "compiled binary") {
		t.Errorf("the report does not say what was found:\n%s", out.String())
	}
	// And it fails while the file is still untracked, which is the
	// ordering that matters: the alternative is failing after the
	// `git add -A` that would have swept it into a commit.
	if !strings.Contains(out.String(), "built") {
		t.Errorf("the report does not name the file:\n%s", out.String())
	}
}

// The scan reports how much it read, and refuses to pass having read
// nothing. The fixture holds exactly one file, one commit and one tag.
func TestTheLeakScanSaysHowMuchItReadAndRefusesNothing(t *testing.T) {
	dir := gitRepo(t, sample("a.tester", "@", "example", ".com"), "Add a file\n")
	t.Chdir(dir)
	var out strings.Builder
	if err := leaks(&out, nil); err != nil {
		t.Fatalf("leaks: %v\n%s", err, out.String())
	}
	if got, want := out.String(), "leak check ok (1 files)\n"; got != want {
		t.Errorf("tree scan said %q, want %q", got, want)
	}
	out.Reset()
	if err := leaksInHistory(&out); err != nil {
		t.Fatalf("leaksInHistory: %v\n%s", err, out.String())
	}
	if got, want := out.String(), "history leak check ok (1 blobs, 2 messages)\n"; got != want {
		t.Errorf("history scan said %q, want %q", got, want)
	}

	// Nothing to read: a repository whose only commit is empty.
	empty := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=A Tester", "-c", "user.email=" + sample("a.tester", "@", "example", ".com"),
			"commit", "-q", "--allow-empty", "-m", "Nothing yet"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = empty
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	t.Chdir(empty)
	if err := leaks(&out, nil); err == nil || !strings.Contains(err.Error(), "looked at nothing") {
		t.Errorf("a tree with no files: err = %v, want the looked-at-nothing refusal", err)
	}
	if err := leaksInHistory(&out); err == nil || !strings.Contains(err.Error(), "looked at nothing") {
		t.Errorf("a history with no blobs: err = %v, want the looked-at-nothing refusal", err)
	}
}

// The skip list is keyed by path from the repository root. The same
// name anywhere else is scanned like any other file.
func TestOnlyTheListedPathIsSkipped(t *testing.T) {
	dir := gitRepo(t, sample("a.tester", "@", "example", ".com"), "Add a file\n")
	address := sample("someone@", "a-real-", "company.com")
	for _, p := range []string{"testdata/api-fields.json", "elsewhere/api-fields.json"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(address+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	var out strings.Builder
	if err := leaks(&out, nil); err == nil {
		t.Fatalf("a leak in elsewhere/api-fields.json passed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "elsewhere/api-fields.json:1") {
		t.Errorf("the unlisted copy was not reported:\n%s", out.String())
	}
	if strings.Contains(out.String(), "testdata/api-fields.json") {
		t.Errorf("the listed path was scanned:\n%s", out.String())
	}
}

// What history mode is for: a leak deleted at the tip is still in the
// log. So is a binary that was committed and then removed.
func TestHistoryFindsWhatTheTipNoLongerHas(t *testing.T) {
	dir := gitRepo(t, sample("a.tester", "@", "example", ".com"), "Add a file\n")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=A Tester",
			"-c", "user.email=" + sample("a.tester", "@", "example", ".com")}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	id := sample("1QwErTyUiOp2", "AsDfGhJkL3", "ZxCvBnM4", "pLmNbVcXz")
	if err := os.WriteFile(filepath.Join(dir, "pasted.md"), []byte("an id: "+id+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "built"), append([]byte("\x7fELF\x00"), make([]byte, 64)...), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "pasted.md", "built")
	git("commit", "-q", "-m", "Add two things")
	git("rm", "-q", "pasted.md", "built")
	git("commit", "-q", "-m", "Remove them")
	t.Chdir(dir)

	var out strings.Builder
	if err := leaks(&out, nil); err != nil {
		t.Fatalf("the tip is clean, and the tree scan failed: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := leaksInHistory(&out); err == nil {
		t.Fatalf("history passed with a deleted leak in it:\n%s", out.String())
	}
	for _, want := range []string{"pasted.md@", "something shaped like a Drive id", "built@", "compiled binary is in the history"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the history report does not carry %q:\n%s", want, out.String())
		}
	}
}
