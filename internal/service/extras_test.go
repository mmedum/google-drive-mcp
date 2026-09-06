package service_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// Phase 1 read the reference, found no copyComments on files.copy, and
// recorded it as refuted (§18). The discovery document lists it. This is
// the assertion that keeps the second answer honest: if Drive drops the
// parameter again, the live run says so and this test says where.
func TestCopyCanBringTheCommentsWithIt(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddComment("id-budget-fixture", "id-comment-1", "check row 4",
		drivetest.WithReply("id-reply-1", "Other Person", "done", ""))

	res, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-budget-fixture", Name: "Budget copy", CopyComments: true,
	})
	if err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	// The copy carries the words, and the result says so: a comment is
	// somebody else's writing, and copying it moves it somewhere they
	// may not have expected.
	if !strings.Contains(res.Text, "comment threads were copied") {
		t.Errorf("the result does not say the comments came along:\n%s", res.Text)
	}
	sent := lastQuery(t, fake, "POST", "/copy", "copyComments")
	if sent != "true" {
		t.Errorf("copyComments = %q, want true", sent)
	}
}

func TestCopyLeavesTheCommentsBehindByDefault(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddComment("id-budget-fixture", "id-comment-1", "check row 4")

	if _, err := svc.CopyFile(t.Context(), service.CopyFileInput{
		File: "id-budget-fixture", Name: "Budget copy",
	}); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if sent := lastQuery(t, fake, "POST", "/copy", "copyComments"); sent != "" {
		t.Errorf("copyComments = %q on a copy that did not ask for it", sent)
	}
}

func TestMarkingAFileViewedStampsTheTimeAndReportsIt(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	res, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{
		File: "id-budget-fixture", Viewed: true,
	})
	if err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	if got := fake.Files["id-budget-fixture"].ViewedByMeTime; got == "" {
		t.Error("viewedByMeTime was not set, so the file will not appear in Recent")
	}
	if !strings.Contains(res.Text, "last opened by you") {
		t.Errorf("the result does not report the change:\n%s", res.Text)
	}
}

// The field a caller can write is viewedByMeTime; viewedByMe beside it is
// output only. Sending the wrong one is a patch Drive accepts and ignores.
func TestViewedSendsTheWritableFieldNotTheReadOnlyOne(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	if _, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{
		File: "id-budget-fixture", Viewed: true,
	}); err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	if got := fake.Files["id-budget-fixture"].ViewedByMeTime; !strings.HasPrefix(got, "2026-") {
		t.Errorf("viewedByMeTime = %q, want the server's own clock in RFC 3339", got)
	}
}

func TestSearchByPropertyMatchesAKeyAndValue(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Files["id-budget-fixture"].Properties = map[string]string{"stage": "final"}
	fake.Files["id-notes-fixture"].Properties = map[string]string{"stage": "draft"}

	cases := []struct {
		name     string
		property string
		wantHits int
	}{
		{"a key and a value match only that pair", "stage=final", 1},
		{"the other pair matches the other file", "stage=draft", 1},
		{"a value nothing carries matches nothing", "stage=archived", 0},
		{"a key nothing carries matches nothing", "absent=anything", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := svc.Search(t.Context(), service.SearchInput{Property: c.property})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if got := hitCount(t, out); got != c.wantHits {
				t.Errorf("property %q matched %d files, want %d:\n%s", c.property, got, c.wantHits, out)
			}
		})
	}
}

// Drive requires both halves. Its own search guide gives the key-only
// form as an example and the API answers it 400 "Invalid Value" —
// confirmed live in phase 4, twice in each of three spellings and for
// appProperties too. Refusing it here says why; sending it spends a
// round trip to be told less.
func TestSearchByPropertyRefusesAHalfWrittenPair(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	for _, property := range []string{"=value", "stage", "stage="} {
		_, err := svc.Search(t.Context(), service.SearchInput{Property: property})
		if err == nil {
			t.Errorf("%q was sent to Drive, which refuses it", property)
			continue
		}
		if !strings.Contains(err.Error(), "key") {
			t.Errorf("the refusal for %q does not say what is missing: %v", property, err)
		}
	}
}

func TestUploadCanAskDriveToIndexTheContent(t *testing.T) {
	dir := t.TempDir()
	svc, fake := setup(t, service.Options{LocalDir: dir})
	if err := os.WriteFile(filepath.Join(dir, "notes.log"), []byte("the words to index"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.UploadFile(t.Context(), service.UploadFileInput{
		LocalPath: "notes.log", UseContentAsIndexableText: true,
	}); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if sent := lastQuery(t, fake, "POST", "/files", "useContentAsIndexableText"); sent != "true" {
		t.Errorf("useContentAsIndexableText = %q, want true", sent)
	}
}
