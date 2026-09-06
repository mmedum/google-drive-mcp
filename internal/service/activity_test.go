package service_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
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

// TestTheTwoKindsOfUndescribedActivityAreToldApart holds the two apart
// on the shapes Drive really sends, which is where phase 4 went wrong.
//
// It read the ordinary case as a MISSING primaryActionDetail. A probe of
// 400 activities finds that shape zero times: Drive sends `{}`, ten
// times out of 400. So every ordinary entry went on being reported as a
// thirteenth kind — the exact confusion the split was written to end —
// and the test agreed, because both fixtures were built from the belief
// rather than from a response.
//
// The fixtures below are therefore written as JSON reaches the decoder,
// and the fake serialises them, so the round trip is the one the client
// makes. A thirteenth kind is a member name nothing here covers; the
// empty object is the ordinary one.
func TestTheTwoKindsOfUndescribedActivityAreToldApart(t *testing.T) {
	svc, fake := active(t)
	fake.AddActivity("id-budget-fixture", "EDIT", true, "2026-03-03T09:00:00Z")
	setDetail(t, fake, fake.AddActivity("id-budget-fixture", "EDIT", true, "2026-03-02T09:00:00Z"),
		`{}`)
	setDetail(t, fake, fake.AddActivity("id-budget-fixture", "EDIT", true, "2026-03-01T09:00:00Z"),
		`{"approvalChange":{"approvalId":"a"}}`)

	out, err := svc.ListActivity(t.Context(), service.ListActivityInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if !strings.Contains(out, "1 entry Drive recorded without saying what happened") {
		t.Errorf("an empty action detail was not reported as the ordinary case:\n%s", out)
	}
	if !strings.Contains(out, "a kind Drive has grown that this server cannot name (approvalChange)") {
		t.Errorf("a kind this server cannot name was not reported and named:\n%s", out)
	}
	if !strings.Contains(out, "1 event") {
		t.Errorf("the count does not match the one describable event:\n%s", out)
	}
}

// TestAnEmptyActionDetailIsNotAThirteenthKind is the regression on its
// own, because the assertion above passes if either half is right and
// this is the half that was wrong live. An account whose feed holds
// nothing but ordinary entries must not be told Google changed the API.
func TestAnEmptyActionDetailIsNotAThirteenthKind(t *testing.T) {
	svc, fake := active(t)
	fake.AddActivity("id-budget-fixture", "EDIT", true, "2026-03-03T09:00:00Z")
	setDetail(t, fake, fake.AddActivity("id-budget-fixture", "EDIT", true, "2026-03-02T09:00:00Z"),
		`{}`)

	out, err := svc.ListActivity(t.Context(), service.ListActivityInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if strings.Contains(out, "Drive has grown") {
		t.Errorf("an empty action detail was reported as a new kind:\n%s", out)
	}
	if !strings.Contains(out, "without saying what happened") {
		t.Errorf("an empty action detail went unaccounted for:\n%s", out)
	}
}

// setDetail gives an activity the action detail it should be served
// with, as JSON. A struct literal cannot express the difference these
// tests are about — a member gdrive.ActionDetail has no field for
// vanishes the moment the fake re-encodes one.
func setDetail(t *testing.T, fake *drivetest.Server, a *gdrive.DriveActivity, detail string) {
	t.Helper()
	if err := fake.SetActivityDetail(a, detail); err != nil {
		t.Fatalf("set action detail %s: %v", detail, err)
	}
}

// TestOneNewKindTwiceIsStillOneKind is the shape the note exists for and
// the shape it got wrong: Google adds a kind, so it arrives on several
// entries of the same page.
//
// The names are deduplicated for the list but the count came from the
// undeduplicated slice, so two entries of one new kind announced "kinds"
// over a list of one — a count saying what the evidence does not, which
// is the defect this whole area was fixed for.
func TestOneNewKindTwiceIsStillOneKind(t *testing.T) {
	svc, fake := active(t)
	fake.AddActivity("id-budget-fixture", "EDIT", true, "2026-03-03T09:00:00Z")
	for _, at := range []string{"2026-03-02T09:00:00Z", "2026-03-01T09:00:00Z"} {
		setDetail(t, fake, fake.AddActivity("id-budget-fixture", "EDIT", true, at),
			`{"approvalChange":{"approvalId":"a"}}`)
	}

	out, err := svc.ListActivity(t.Context(), service.ListActivityInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if !strings.Contains(out, "2 entries of a kind Drive has grown") {
		t.Errorf("two entries of one new kind did not report one kind:\n%s", out)
	}
	if strings.Contains(out, "of kinds Drive has grown") {
		t.Errorf("one kind was announced as several:\n%s", out)
	}
}
