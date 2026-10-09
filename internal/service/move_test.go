package service_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v2/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v2/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v2/internal/render"
	"github.com/mmedum/google-drive-mcp/v2/internal/service"
)

// person keeps the questions a write puts, and answers each with err.
type person struct {
	asked []render.Question
	err   error
}

func (p *person) Ask(_ context.Context, q render.Question) error {
	p.asked = append(p.asked, q)
	return p.err
}
func (*person) Asks() bool  { return true }
func (*person) Shows() bool { return true }

// declines is a person who answers every question with no.
func declines() *person {
	return &person{err: service.Errorf(service.ClassBlocked, "move_file was not confirmed by the person")}
}

func TestMoveFile(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	dry, err := svc.MoveFile(t.Context(), service.MoveFileInput{
		File: "id-budget-fixture", To: "id-archive-fixture", DryRun: true,
	})
	if err != nil {
		t.Fatalf("MoveFile dry run: %v", err)
	}
	if !dry.JSON.DryRun {
		t.Error("a dry run did not mark itself as one")
	}
	if fake.Files["id-budget-fixture"].Parent() != "id-2026-fixture" {
		t.Fatal("the dry run moved the file")
	}
	if !strings.Contains(dry.Text, "My Drive/Projects/2026") || !strings.Contains(dry.Text, "My Drive/Projects/Archive") {
		t.Errorf("the dry run does not show both locations:\n%s", dry.Text)
	}

	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{File: "id-budget-fixture", To: "id-archive-fixture"})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if fake.Files["id-budget-fixture"].Parent() != "id-archive-fixture" {
		t.Errorf("parent = %q", fake.Files["id-budget-fixture"].Parent())
	}
	if len(got.JSON.Changes) != 1 || got.JSON.Changes[0].Field != "location" {
		t.Errorf("changes = %v, want the location before and after", got.JSON.Changes)
	}
}

func TestMoveFileRefusesAMyDriveFolderIntoASharedDrive(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	_, err := svc.MoveFile(t.Context(), service.MoveFileInput{
		File: "id-projects-fixture", To: "id-campaigns-fixture",
	})
	if err == nil {
		t.Fatal("a My Drive folder moved into a shared drive")
	}
	if !strings.Contains(err.Error(), "[unsupported]") || !strings.Contains(err.Error(), "create_folder") {
		t.Errorf("the refusal does not say what to do instead: %v", err)
	}
	if fake.Files["id-projects-fixture"].Parent() != fake.RootID {
		t.Error("the folder moved anyway")
	}
}

func TestMoveFileSaysWhenItIsAlreadyThere(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{File: "id-budget-fixture", To: "/Projects/2026"})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got.JSON.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", got.JSON.Action)
	}
}

func TestMoveFileMovesAFileIntoASharedDrive(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	if _, err := svc.MoveFile(t.Context(), service.MoveFileInput{
		File: "id-budget-fixture", To: "drive:Marketing/Campaigns",
	}); err != nil {
		t.Fatalf("MoveFile into a shared drive: %v", err)
	}
	moved := fake.Files["id-budget-fixture"]
	if moved.Parent() != "id-campaigns-fixture" || moved.DriveID != "id-drive-marketing" {
		t.Errorf("moved to parent %q in drive %q", moved.Parent(), moved.DriveID)
	}
}

// linkShared makes the Archive folder open to anyone with the link, so
// whatever moves into it is too.
func linkShared(fake *drivetest.Server) {
	fake.Grant("id-archive-fixture", &gdrive.Permission{Type: "anyone", Role: "reader"})
}

func TestMoveFileDryRunShowsWhoWouldReachItAndAsksNothing(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	linkShared(fake)
	p := &person{}

	got, err := svc.MoveFile(service.WithAsker(t.Context(), p), service.MoveFileInput{
		File: "id-budget-fixture", To: "id-archive-fixture", DryRun: true,
	})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got.JSON.SharingBefore != "private to you" || got.JSON.SharingAfter != "anyone with the link can view" {
		t.Errorf("sharing %q → %q, want private to you → anyone with the link can view",
			got.JSON.SharingBefore, got.JSON.SharingAfter)
	}
	if !strings.Contains(got.JSON.Note, "the move would let more people reach it, or give them more access: "+
		"anyone with the link can view") {
		t.Errorf("the note does not say who the move adds: %q", got.JSON.Note)
	}
	if len(p.asked) != 0 {
		t.Errorf("a dry run asked the person: %s", p.asked[0].Text)
	}
	if fake.Files["id-budget-fixture"].Parent() != "id-2026-fixture" {
		t.Error("the dry run moved the file")
	}
}

func TestMoveFileAsksBeforeItLetsMorePeopleReachIt(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	linkShared(fake)

	no := declines()
	if _, err := svc.MoveFile(service.WithAsker(t.Context(), no), service.MoveFileInput{
		File: "id-budget-fixture", To: "id-archive-fixture",
	}); err == nil || !strings.Contains(err.Error(), "[blocked]") {
		t.Fatalf("err = %v, want the refusal the person gave", err)
	}
	if len(no.asked) != 1 {
		t.Fatalf("asked %d questions, want 1", len(no.asked))
	}
	for _, want := range []string{
		"move_file: move the file `Budget.xlsx` into the folder `Archive`?",
		"It would reach more people there, or give them more access: anyone with the link can view",
	} {
		if !strings.Contains(no.asked[0].Text, want) {
			t.Errorf("the question does not say %q:\n%s", want, no.asked[0].Text)
		}
	}
	if fake.Files["id-budget-fixture"].Parent() != "id-2026-fixture" || fake.Count(http.MethodPatch) != 0 {
		t.Fatal("a move the person declined was made")
	}

	yes := &person{}
	got, err := svc.MoveFile(service.WithAsker(t.Context(), yes), service.MoveFileInput{
		File: "id-budget-fixture", To: "id-archive-fixture",
	})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if fake.Files["id-budget-fixture"].Parent() != "id-archive-fixture" {
		t.Fatal("an accepted move was not made")
	}
	// The after is read back from Drive once the move is made.
	if got.JSON.SharingAfter != "anyone with the link can view" {
		t.Errorf("sharing after = %q", got.JSON.SharingAfter)
	}
	if got.JSON.Note != "more people can reach it now, or have more access: anyone with the link can view." {
		t.Errorf("note = %q", got.JSON.Note)
	}
}

func TestMoveFileKeepsWhatWasGrantedOnItAndAsksNothingWhenItNarrows(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	// alice reaches the file through its folder, bob through a grant on
	// the file itself. The move takes alice's access and leaves bob's.
	fake.Grant("id-2026-fixture", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: "alice@example.com"})
	fake.Grant("id-budget-fixture", &gdrive.Permission{Type: "user", Role: "reader", EmailAddress: "bob@example.com"})
	p := declines()

	got, err := svc.MoveFile(service.WithAsker(t.Context(), p), service.MoveFileInput{
		File: "id-budget-fixture", To: "id-archive-fixture",
	})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if len(p.asked) != 0 {
		t.Errorf("a move that narrows asked the person: %s", p.asked[0].Text)
	}
	if got.JSON.SharingBefore != "shared with 2 people: 1 can edit, 1 can view (1 of them through a folder above it)" {
		t.Errorf("sharing before = %q", got.JSON.SharingBefore)
	}
	if got.JSON.SharingAfter != "shared with 1 person: 1 can view" {
		t.Errorf("sharing after = %q, want only the grant made on the file", got.JSON.SharingAfter)
	}
	if got.JSON.Note != "" {
		t.Errorf("note = %q, want nothing: the prediction held and nobody was added", got.JSON.Note)
	}
}

func TestMoveFileIntoASharedDriveAsksAboutItsMembers(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-drive-marketing", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: "alice@example.com"})
	fake.Grant("id-drive-marketing", &gdrive.Permission{Type: "domain", Role: "reader", Domain: "example.com"})
	p := declines()

	if _, err := svc.MoveFile(service.WithAsker(t.Context(), p), service.MoveFileInput{
		File: "id-budget-fixture", To: "drive:Marketing",
	}); err == nil {
		t.Fatal("a move the person declined went ahead")
	}
	if len(p.asked) != 1 {
		t.Fatalf("asked %d questions, want 1", len(p.asked))
	}
	want := "move the file `Budget.xlsx` into the shared drive `Marketing`?\n\nIt would reach more people there, " +
		"or give them more access: 1 person (1 can edit); everyone at `example.com` can view"
	if !strings.Contains(p.asked[0].Text, want) {
		t.Errorf("the question does not say %q:\n%s", want, p.asked[0].Text)
	}
}

func TestMoveFileCountsTheDestinationsOwnerAsAnEditor(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-archive-fixture", &gdrive.Permission{Type: "user", Role: "owner", EmailAddress: "carol@example.com"})

	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{
		File: "id-budget-fixture", To: "id-archive-fixture", DryRun: true,
	})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got.JSON.SharingAfter != "shared with 1 person: 1 can edit (1 of them through a folder above it)" {
		t.Errorf("sharing after = %q, want the folder's owner counted as an editor", got.JSON.SharingAfter)
	}
}

func TestMoveFileLeavesTheAccountItselfOutOfWhoItAdds(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	// The account reaches the folder through a grant of its own; that is
	// nobody's exposure, so moving into it asks nothing.
	fake.Grant("id-archive-fixture", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: drivetest.AccountEmail})
	p := declines()

	if _, err := svc.MoveFile(service.WithAsker(t.Context(), p), service.MoveFileInput{
		File: "id-budget-fixture", To: "id-archive-fixture",
	}); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if len(p.asked) != 0 {
		t.Errorf("asked about the account's own access: %s", p.asked[0].Text)
	}
}

func TestMoveFileAsksWhenItCannotReadWhoReachesTheDestination(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Fail = drivetest.FailTimes(1, "/files/id-archive-fixture/permissions", drivetest.Failure{
		Status: http.StatusForbidden, Reason: "insufficientFilePermissions", Message: "no",
	})
	p := declines()

	if _, err := svc.MoveFile(service.WithAsker(t.Context(), p), service.MoveFileInput{
		File: "id-budget-fixture", To: "id-archive-fixture",
	}); err == nil {
		t.Fatal("a move the person declined went ahead")
	}
	if len(p.asked) != 1 || !strings.Contains(p.asked[0].Text,
		"Who can reach the destination could not be read, so whether more people would reach it there is unknown.") {
		t.Fatalf("questions = %v", p.asked)
	}
}

func TestMoveFileSaysWhenDriveAnswersOtherThanPredicted(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	// Drive does something the documents did not lead anyone to expect:
	// the moved file arrives open to a whole domain.
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/files/id-budget-fixture") {
			fake.Grant("id-budget-fixture", &gdrive.Permission{Type: "domain", Role: "reader", Domain: "example.com"})
		}
		return nil
	}

	got, err := svc.MoveFile(yes(t), service.MoveFileInput{File: "id-budget-fixture", To: "id-archive-fixture"})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got.JSON.SharingAfter != "everyone at example.com can view with the link" {
		t.Errorf("sharing after = %q, want what Drive answered", got.JSON.SharingAfter)
	}
	want := "more people can reach it now, or have more access: everyone at example.com can view. " +
		"that is not who this server worked out would reach it, which was: private to you."
	if !strings.HasPrefix(got.JSON.Note, want) {
		t.Errorf("note = %q, want it to start %q", got.JSON.Note, want)
	}
}
