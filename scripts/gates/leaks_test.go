package main

import (
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
	// Each of these is something a real session actually produced. The
	// gate exists because a person pasting a transcript into a fixture,
	// a doc or a commit message will not notice any of them.
	cases := map[string]string{
		"a colleague's address":    "modified 2026-09-04 by Someone <" + address + ">",
		"a file id in a fixture":   `s.AddFile("` + id + `", "x", "text/plain", "")`,
		"a shared-drive id":        "drive: " + driveID,
		"a link with an id in it":  "link: " + link,
		"a docs link in a comment": "// see " + docLink,
	}
	for name, line := range cases {
		if got := scanForLeaks("some/file.go", line); len(got) == 0 {
			t.Errorf("%s went undetected: %q", name, line)
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
	}
	for name, line := range cases {
		if got := scanForLeaks("some/file.go", line); len(got) != 0 {
			t.Errorf("%s was flagged: %q -> %v", name, line, got)
		}
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
