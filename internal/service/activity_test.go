package service_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// active builds a service with activity on and the Drive Activity API
// reachable.
func active(t *testing.T) (*service.Service, *drivetest.Server) {
	t.Helper()
	svc, fake := setup(t, service.Options{Activity: true})
	fake.ActivityEnabled = true
	return svc, fake
}

func TestListActivityNamesWhatHappened(t *testing.T) {
	svc, fake := active(t)
	fake.AddActivity("id-budget-fixture", "EDIT", true, "2026-03-03T09:00:00Z")
	fake.AddActivity("id-budget-fixture", "RENAME", false, "2026-03-02T09:00:00Z")

	out, err := svc.ListActivity(t.Context(), service.ListActivityInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	for _, want := range []string{"2 events", "you edited", "somebody else renamed", "from Before to After"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not carry %q:\n%s", want, out)
		}
	}
}

// The API identifies a person by a People API resource name and gives no
// display name or address. A reader who is not told that will assume the
// names were lost in this server.
func TestListActivitySaysWhyItCannotNameAnybody(t *testing.T) {
	svc, fake := active(t)
	fake.AddActivity("id-budget-fixture", "EDIT", false, "2026-03-03T09:00:00Z")

	out, err := svc.ListActivity(t.Context(), service.ListActivityInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if !strings.Contains(out, "identifies people only as") {
		t.Errorf("the listing does not say why nobody is named:\n%s", out)
	}
	// And it must not invent one from the resource name it was given.
	if strings.Contains(out, "people/") {
		t.Errorf("the internal person id reached the output:\n%s", out)
	}
}

// A folder asked about two ways gives two very different answers, and
// the head has to say which question was asked: what happened TO the
// folder is almost always nothing.
func TestActivityOnAFolderSaysWhichQuestionItAnswered(t *testing.T) {
	svc, fake := active(t)
	fake.AddActivity("id-budget-fixture", "EDIT", true, "2026-03-03T09:00:00Z")

	out, err := svc.ListActivity(t.Context(), service.ListActivityInput{File: "id-2026-fixture"})
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if !strings.Contains(out, "on the folder itself") {
		t.Errorf("a folder's own activity did not say so:\n%s", out)
	}

	out, err = svc.ListActivity(t.Context(), service.ListActivityInput{
		File: "id-2026-fixture", Recursive: true,
	})
	if err != nil {
		t.Fatalf("ListActivity recursive: %v", err)
	}
	if !strings.Contains(out, "everything under it") {
		t.Errorf("the recursive listing did not say what it covered:\n%s", out)
	}
	if !strings.Contains(out, "1 event") {
		t.Errorf("the recursive listing did not reach the file inside the folder:\n%s", out)
	}
}

func TestActivityRecursiveIsRefusedOnAFile(t *testing.T) {
	svc, _ := active(t)
	_, err := svc.ListActivity(t.Context(), service.ListActivityInput{
		File: "id-budget-fixture", Recursive: true,
	})
	if err == nil {
		t.Error("recursive was accepted on a file, where the API's ancestorName means nothing")
	}
}

func TestActivityFiltersByKind(t *testing.T) {
	svc, fake := active(t)
	fake.AddActivity("id-budget-fixture", "EDIT", true, "2026-03-03T09:00:00Z")
	fake.AddActivity("id-budget-fixture", "COMMENT", true, "2026-03-02T09:00:00Z")
	fake.AddActivity("id-budget-fixture", "RENAME", true, "2026-03-01T09:00:00Z")

	out, err := svc.ListActivity(t.Context(), service.ListActivityInput{
		File: "id-budget-fixture", Actions: []string{"edit", "comment"},
	})
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if !strings.Contains(out, "2 events") {
		t.Errorf("the filter did not select two of the three:\n%s", out)
	}
	if strings.Contains(out, "renamed") {
		t.Errorf("a kind that was filtered out appeared:\n%s", out)
	}
}

func TestActivityRefusesAKindItDoesNotHave(t *testing.T) {
	svc, _ := active(t)
	_, err := svc.ListActivity(t.Context(), service.ListActivityInput{
		File: "id-budget-fixture", Actions: []string{"eaten"},
	})
	if err == nil {
		t.Fatal("an action filter that does not exist was sent to the API")
	}
	if !strings.Contains(err.Error(), "rename") {
		t.Errorf("the refusal does not list the kinds that do exist: %v", err)
	}
}

func TestActivityIsOffUntilTheDeployerTurnsItOn(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.ListActivity(t.Context(), service.ListActivityInput{File: "id-budget-fixture"})
	if err == nil {
		t.Fatal("list_activity answered with activity off")
	}
	if !strings.Contains(err.Error(), "GDRIVE_ACTIVITY") {
		t.Errorf("the refusal does not name the setting that turns it on: %v", err)
	}
}

// The refusal every deployer meets first, and the one they can act on.
func TestActivityRefusalNamesTheSetupStepBehindIt(t *testing.T) {
	svc, fake := active(t)
	fake.ActivityEnabled = false

	_, err := svc.ListActivity(t.Context(), service.ListActivityInput{File: "id-budget-fixture"})
	if err == nil {
		t.Fatal("the API refused the token and the call succeeded anyway")
	}
	for _, want := range []string{"Drive Activity API", "login"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// activity:query is a POST that only reads. Without saying so it would
// spend from the write budget and, worse, refuse to retry after a
// dropped connection, because a POST is not repeatable by default.
func TestActivityQueryIsTreatedAsARead(t *testing.T) {
	svc, fake := active(t)
	fake.AddActivity("id-budget-fixture", "EDIT", true, "2026-03-03T09:00:00Z")

	tries := 0
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if strings.HasSuffix(r.URL.Path, "/activity:query") {
			tries++
			if tries == 1 {
				return &drivetest.Failure{Hijack: true}
			}
		}
		return nil
	}
	if _, err := svc.ListActivity(t.Context(), service.ListActivityInput{File: "id-budget-fixture"}); err != nil {
		t.Fatalf("a dropped connection was not retried, so the query was treated as a write: %v", err)
	}
	if tries < 2 {
		t.Errorf("the query was attempted %d times; a read retries", tries)
	}
}
