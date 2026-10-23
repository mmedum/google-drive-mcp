package service_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
	"github.com/mmedum/google-drive-mcp/v3/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v3/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v3/internal/service"
)

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

// A move that would let anyone with the link reach the file is refused,
// dry run included, naming who, and nothing moves (§4a).
func TestAMoveThatReachesAnyoneIsRefusedNamingThem(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	linkShared(fake)

	want := "[blocked] the move would let more people reach it beyond the people and groups named inside this account's " +
		"organization: anyone with the link can view. Nothing was moved. Share it with them first, with " +
		"share_by_link or share_outside_organization when GDRIVE_ENABLE_DESTRUCTIVE=true, and the move then " +
		"gains them nothing. One that gains only people inside the organization is allowed."
	for _, dry := range []bool{true, false} {
		_, err := svc.MoveFile(t.Context(), service.MoveFileInput{
			File: "id-budget-fixture", To: "id-archive-fixture", DryRun: dry,
		})
		if err == nil || err.Error() != want {
			t.Errorf("dry run %v: err = %v\nwant %s", dry, err, want)
		}
	}
	if fake.Files["id-budget-fixture"].Parent() != "id-2026-fixture" || fake.Count(http.MethodPatch) != 0 {
		t.Fatal("a refused move was made")
	}
}

// A move that adds only people inside the organization is an ordinary
// write: it goes, and the result says who it added.
func TestAMoveThatReachesOnlyPeopleInsideGoes(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-archive-fixture", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: "alice@example.com"})

	dry, err := svc.MoveFile(t.Context(), service.MoveFileInput{
		File: "id-budget-fixture", To: "id-archive-fixture", DryRun: true,
	})
	if err != nil {
		t.Fatalf("MoveFile dry run: %v", err)
	}
	if want := "the move would let more people reach it, all of them inside this account's organization, or give " +
		"them more access: 1 person (1 can edit). A real move does that."; dry.JSON.Note != want {
		t.Errorf("dry run note = %q\nwant %q", dry.JSON.Note, want)
	}
	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{File: "id-budget-fixture", To: "id-archive-fixture"})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if fake.Files["id-budget-fixture"].Parent() != "id-archive-fixture" {
		t.Fatal("the move was not made")
	}
	if got.JSON.Note != "more people can reach it now, or have more access: 1 person (1 can edit)." {
		t.Errorf("note = %q", got.JSON.Note)
	}
}

func TestMoveFileKeepsWhatWasGrantedOnIt(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	// alice reaches the file through its folder, bob through a grant on
	// the file itself. The move takes alice's access and leaves bob's.
	fake.Grant("id-2026-fixture", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: "alice@example.com"})
	fake.Grant("id-budget-fixture", &gdrive.Permission{Type: "user", Role: "reader", EmailAddress: "bob@example.com"})

	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{
		File: "id-budget-fixture", To: "id-archive-fixture",
	})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
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

// Moving into a shared drive reaches its members: one inside the
// organization is ordinary, and a whole domain among them refuses the
// move, naming only the domain.
func TestMoveFileIntoASharedDriveIsRefusedForADomainAmongItsMembers(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-drive-marketing", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: "alice@example.com"})
	fake.Grant("id-drive-marketing", &gdrive.Permission{Type: "domain", Role: "reader", Domain: "example.com"})

	_, err := svc.MoveFile(t.Context(), service.MoveFileInput{File: "id-budget-fixture", To: "drive:Marketing"})
	want := "[blocked] the move would let more people reach it beyond the people and groups named inside this account's " +
		"organization: everyone at example.com can view. Nothing was moved."
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %v\nwant it to start %s", err, want)
	}
	if fake.Count(http.MethodPatch) != 0 {
		t.Error("the refused move was made")
	}
}

// A person outside the organization among who a folder reaches refuses
// a move into it, named by address; one inside it does not.
func TestAMoveThatReachesSomeoneOutsideIsRefused(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-archive-fixture", &gdrive.Permission{Type: "user", Role: "reader", EmailAddress: "alice@example.com"})
	fake.Grant("id-archive-fixture", &gdrive.Permission{Type: "group", Role: "writer", EmailAddress: "team@example.org"})

	_, err := svc.MoveFile(t.Context(), service.MoveFileInput{File: "id-budget-fixture", To: "id-archive-fixture"})
	want := "[blocked] the move would let more people reach it beyond the people and groups named inside this account's " +
		"organization: group team@example.org can edit. Nothing was moved."
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %v\nwant it to start %s", err, want)
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
	// nobody's exposure, so moving into it adds nobody.
	fake.Grant("id-archive-fixture", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: drivetest.AccountEmail})

	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{File: "id-budget-fixture", To: "id-archive-fixture"})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got.JSON.Note != "" {
		t.Errorf("note = %q, want nothing: the account's own access is nobody's exposure", got.JSON.Note)
	}
}

// A move whose destination's reach cannot be read is refused: nothing
// shows it does not reach past the organization.
func TestAMoveWhoseReachCannotBeReadIsRefused(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Fail = drivetest.FailTimes(1, "/files/id-archive-fixture/permissions", drivetest.Failure{
		Status: http.StatusForbidden, Reason: "insufficientFilePermissions", Message: "no",
	})

	_, err := svc.MoveFile(t.Context(), service.MoveFileInput{File: "id-budget-fixture", To: "id-archive-fixture"})
	want := "[blocked] who can reach the destination could not be read, so whether the move would let more " +
		"people reach it beyond the people and groups named inside this account's organization is unknown. Nothing was moved. " +
		"list_permissions shows whether who can reach each can be read now."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
	if fake.Count(http.MethodPatch) != 0 {
		t.Error("the refused move was made")
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

	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{File: "id-budget-fixture", To: "id-archive-fixture"})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got.JSON.SharingAfter != "everyone at example.com can view with the link" {
		t.Errorf("sharing after = %q, want what Drive answered", got.JSON.SharingAfter)
	}
	want := "more people can reach it now, or have more access: everyone at example.com can view with the link. " +
		"that is not who this server worked out would reach it, which was: private to you."
	if !strings.HasPrefix(got.JSON.Note, want) {
		t.Errorf("note = %q, want it to start %q", got.JSON.Note, want)
	}
}

// The account counts when Drive's answer is checked against what was
// shown before: an answer that differs only in the account's own access
// still differs from the summary the person saw.
func TestMoveFileSaysWhenDriveAnswersOtherThanPredictedAboutTheAccount(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Files["id-budget-fixture"].Owners = []*gdrive.User{{DisplayName: "Jane", EmailAddress: "jane@example.com"}}
	fake.Grant("id-budget-fixture", &gdrive.Permission{Type: "user", Role: "reader", EmailAddress: drivetest.AccountEmail})
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/files/id-budget-fixture") {
			fake.Grant("id-budget-fixture", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: drivetest.AccountEmail})
		}
		return nil
	}

	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{File: "id-budget-fixture", To: "id-archive-fixture"})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	want := "that is not who this server worked out would reach it, which was: shared with 1 person: 1 can view."
	if got.JSON.SharingAfter != "shared with 1 person: 1 can edit" || !strings.Contains(got.JSON.Note, want) {
		t.Errorf("sharing after = %q, note %q; want Drive's answer and a note containing %q",
			got.JSON.SharingAfter, got.JSON.Note, want)
	}
}

// TestSharingOffRefusesAMoveThatWidens holds GDRIVE_SHARING=off to its
// word for moves: a move that lets more people reach an item is refused,
// dry run included, and one that reaches nobody new goes.
func TestSharingOffRefusesAMoveThatWidens(t *testing.T) {
	svc, fake := setup(t, service.Options{Sharing: config.SharingOff})
	linkShared(fake)
	ctx := t.Context()

	for _, dry := range []bool{true, false} {
		_, err := svc.MoveFile(ctx, service.MoveFileInput{File: "id-budget-fixture", To: "id-archive-fixture", DryRun: dry})
		want := "[forbidden] this server was started with GDRIVE_SHARING=off, and the move would let more people " +
			"reach it, or give them more access: anyone with the link can view. Nothing was moved."
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("dry run %v: err = %v, want it to start %q", dry, err, want)
		}
	}
	if fake.Count(http.MethodPatch) != 0 {
		t.Fatal("a refused move was made")
	}
	if _, err := svc.MoveFile(ctx, service.MoveFileInput{File: "id-budget-fixture", To: "root"}); err != nil {
		t.Errorf("a move that reaches nobody new was refused: %v", err)
	}
}

func TestSharingOffRefusesAMoveWhoseReachCannotBeRead(t *testing.T) {
	svc, fake := setup(t, service.Options{Sharing: config.SharingOff})
	fake.Fail = drivetest.FailTimes(1, "/files/id-archive-fixture/permissions", drivetest.Failure{
		Status: http.StatusForbidden, Reason: "insufficientFilePermissions", Message: "no",
	})

	_, err := svc.MoveFile(t.Context(), service.MoveFileInput{File: "id-budget-fixture", To: "id-archive-fixture"})
	if err == nil || !strings.Contains(err.Error(), "who can reach the destination could not be read, so the move "+
		"may let more people reach it.") {
		t.Fatalf("err = %v, want a refusal saying the destination could not be read", err)
	}
	if fake.Count(http.MethodPatch) != 0 {
		t.Error("the refused move was made")
	}
}

func TestSharingOffRefusesEachItemThatWidensAndMovesTheRest(t *testing.T) {
	svc, fake := setup(t, service.Options{Sharing: config.SharingOff})
	linkShared(fake)
	// The notes are already open to anyone with the link, so the folder
	// adds nobody to them.
	fake.Grant("id-notes-fixture", &gdrive.Permission{Type: "anyone", Role: "reader"})

	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{To: "id-archive-fixture",
		Files: []string{"id-budget-fixture", "id-notes-fixture"}})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got := outcomes(got); got != "refused moved" {
		t.Errorf("outcomes = %q", got)
	}
	refused := got.JSON.Items[0]
	if refused.Reason != "this server was started with GDRIVE_SHARING=off, and the move would let more people "+
		"reach it, or give them more access: anyone with the link can view. Moving it where nobody new can "+
		"reach it is still allowed." || !refused.Widens || refused.To != "" {
		t.Errorf("refused item = %+v", refused)
	}
	if fake.Files["id-budget-fixture"].Parent() != "id-2026-fixture" || fake.Files["id-notes-fixture"].Parent() != "id-archive-fixture" {
		t.Error("the widening item moved, or the other did not")
	}
}

// outcomes is each item's outcome in a move of several, in order.
func outcomes(r *service.Result) string {
	var out []string
	for _, it := range r.JSON.Items {
		out = append(out, it.Outcome)
	}
	return strings.Join(out, " ")
}

func TestMoveSeveralGivesEachItemItsOwnOutcome(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-ledger-fixture", "Ledger", "text/csv", "id-archive-fixture")

	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{To: "id-archive-fixture", Files: []string{
		"id-budget-fixture", "id-notes-fixture", "id-archive-fixture", "id-budget-fixture",
		"id-ledger-fixture", "id-nope",
	}})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got := outcomes(got); got != "moved moved refused refused unchanged refused" {
		t.Errorf("outcomes = %q", got)
	}
	for i, want := range []string{"", "", "Archive cannot be moved into itself", "listed more than once",
		"already in Archive", "[not_found]"} {
		if !strings.Contains(got.JSON.Items[i].Reason, want) {
			t.Errorf("item %d: reason %q does not say %q", i, got.JSON.Items[i].Reason, want)
		}
	}
	for _, id := range []string{"id-budget-fixture", "id-notes-fixture"} {
		if fake.Files[id].Parent() != "id-archive-fixture" {
			t.Errorf("%s was not moved", id)
		}
	}
	if !strings.HasPrefix(got.Text, "moved 2 of 6 items into My Drive/Projects/Archive\n") || got.JSON.Action != "moved" {
		t.Errorf("the result does not lead with how many moved:\n%s", got.Text)
	}
}

// A move of several that moves nothing because every item failed or was
// refused is not "unchanged", which says there was nothing to do.
func TestMoveSeveralThatMovesNothingSaysItFailed(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	two := service.MoveFileInput{To: "id-archive-fixture", Files: []string{"id-budget-fixture", "id-notes-fixture"}}
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if r.Method == http.MethodPatch {
			return &drivetest.Failure{Status: http.StatusForbidden, Reason: "insufficientFilePermissions", Message: "no"}
		}
		return nil
	}
	got, err := svc.MoveFile(t.Context(), two)
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got.JSON.Action != "failed" || got.JSON.Note != "nothing moved: 2 items failed, and each says why." {
		t.Errorf("action %q, note %q; want failed, and why", got.JSON.Action, got.JSON.Note)
	}
	fake.Fail = nil
	mixed := service.MoveFileInput{To: "id-archive-fixture", Files: []string{"id-nope", "id-archive-fixture"}}
	got, err = svc.MoveFile(t.Context(), mixed)
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got.JSON.Action != "failed" || got.JSON.Note != "nothing moved: 2 items refused, and each says why." {
		t.Errorf("action %q, note %q; want failed, and why", got.JSON.Action, got.JSON.Note)
	}
	if _, err := svc.MoveFile(t.Context(), two); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	got, err = svc.MoveFile(t.Context(), two)
	if err != nil {
		t.Fatalf("MoveFile again: %v", err)
	}
	if got.JSON.Action != "unchanged" {
		t.Errorf("a move of items already there: action %q, want unchanged", got.JSON.Action)
	}
}

// The account's own address, which a move needs to leave the account
// out of who it adds, is read once for the whole call.
func TestMoveSeveralReadsTheAccountOnce(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFolder("id-dana-fixture", "Dana's", fake.RootID, drivetest.Owner("Dana", "dana@example.com"))
	fake.Grant("id-dana-fixture", &gdrive.Permission{Type: "user", Role: "reader", EmailAddress: "reader@example.com"})
	var files []string
	for i := range 3 {
		id := fmt.Sprintf("id-theirs-%d-fixture", i)
		fake.AddFile(id, fmt.Sprintf("theirs %d.txt", i), "text/plain", "id-2026-fixture", drivetest.Owner("Jane", "jane@example.com"))
		files = append(files, id)
	}
	fake.Requested()
	if _, err := svc.MoveFile(t.Context(), service.MoveFileInput{Files: files, To: "id-dana-fixture"}); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	about := 0
	for _, r := range fake.Requested() {
		if strings.HasSuffix(r.Path, "/about") {
			about++
		}
	}
	if about != 1 {
		t.Errorf("the account was read %d times, want once", about)
	}
}

// When this account's own address cannot be read, a colleague cannot be
// told from an outsider. The move is refused, saying so, rather than
// calling the colleague outside the organization.
func TestAMoveIsRefusedWithoutCallingAColleagueOutsideWhenTheAccountCannotBeRead(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-archive-fixture", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: "alice@example.com"})
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if strings.HasSuffix(r.URL.Path, "/about") {
			return &drivetest.Failure{Status: http.StatusInternalServerError, Message: "no"}
		}
		return nil
	}
	_, err := svc.MoveFile(t.Context(), service.MoveFileInput{File: "id-budget-fixture", To: "id-archive-fixture"})
	want := "[blocked] this account's own address could not be read, so whether the move would let more people " +
		"reach it beyond the people and groups named inside this account's organization cannot be told: " +
		"alice@example.com can edit. Nothing was moved. get_account shows whether the address can be read now."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
	if fake.Files["id-budget-fixture"].Parent() != "id-2026-fixture" {
		t.Error("the move was made")
	}
}

// In a move of several, an item the destination would open to anyone
// with the link is refused on its own, naming who, and the rest move.
func TestMoveSeveralRefusesEachItemThatReachesPastTheOrganization(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	linkShared(fake)
	// The notes already open to anyone with the link, so the folder adds
	// nobody to them.
	fake.Grant("id-notes-fixture", &gdrive.Permission{Type: "anyone", Role: "reader"})

	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{To: "id-archive-fixture",
		Files: []string{"id-budget-fixture", "id-notes-fixture"}})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got := outcomes(got); got != "refused moved" {
		t.Errorf("outcomes = %q", got)
	}
	want := "the move would let more people reach it beyond the people and groups named inside this account's organization: " +
		"anyone with the link can view. Share it with them first"
	if r := got.JSON.Items[0]; !strings.HasPrefix(r.Reason, want) || !r.Widens || r.To != "" {
		t.Errorf("refused item = %+v\nwant a reason starting %s", r, want)
	}
	if fake.Files["id-budget-fixture"].Parent() != "id-2026-fixture" || fake.Files["id-notes-fixture"].Parent() != "id-archive-fixture" {
		t.Error("the widening item moved, or the other did not")
	}
}

func TestMoveSeveralKeepsWhatMovedWhenOneFails(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/files/id-budget-fixture") {
			return &drivetest.Failure{Status: http.StatusForbidden, Reason: "insufficientFilePermissions", Message: "no"}
		}
		return nil
	}

	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{To: "id-archive-fixture",
		Files: []string{"id-budget-fixture", "id-notes-fixture"}})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got := outcomes(got); got != "failed moved" {
		t.Errorf("outcomes = %q", got)
	}
	if fake.Files["id-notes-fixture"].Parent() != "id-archive-fixture" {
		t.Error("a failure stopped the moves after it")
	}
	if reason := got.JSON.Items[0].Reason; !strings.HasPrefix(reason, "[forbidden]") ||
		!strings.HasSuffix(reason, "It is in My Drive/Projects/2026.") {
		t.Errorf("the failure does not give Drive's answer and where the item is: %q", reason)
	}
	if got.JSON.Items[0].To != "" || got.JSON.Items[0].SharingAfter != "" {
		t.Errorf("a failed item claims where it went: %+v", got.JSON.Items[0])
	}
	if got.JSON.Note != "each item moved or failed on its own: the ones that moved stay moved." {
		t.Errorf("note = %q", got.JSON.Note)
	}
}

func TestMoveSeveralReportsAnItemThatMovedDespiteAnError(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/files/id-budget-fixture") {
			// Drive made the move and lost the answer.
			fake.AddFile("id-budget-fixture", "Budget.xlsx", "text/csv", "id-archive-fixture")
			return &drivetest.Failure{Status: http.StatusForbidden, Reason: "insufficientFilePermissions", Message: "no"}
		}
		return nil
	}

	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{To: "id-archive-fixture", Files: []string{"id-budget-fixture"}})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if outcomes(got) != "moved" || !strings.HasPrefix(got.JSON.Items[0].Reason,
		"Drive answered with an error, and it is in the destination all the same. The error: [forbidden]") {
		t.Errorf("item = %+v", got.JSON.Items[0])
	}
}

func TestMoveSeveralDryRunListsEachOutcomeAndMovesNothing(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-archive-fixture", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: "alice@example.com"})

	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{To: "id-archive-fixture",
		Files: []string{"id-budget-fixture", "id-archive-fixture"}, DryRun: true})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got := outcomes(got); got != "would_move refused" {
		t.Errorf("outcomes = %q", got)
	}
	first := got.JSON.Items[0]
	if !first.Widens || first.SharingBefore != "private to you" ||
		first.SharingAfter != "shared with 1 person: 1 can edit (1 of them through a folder above it)" ||
		first.From != "My Drive/Projects/2026" || first.To != "My Drive/Projects/Archive" {
		t.Errorf("first item = %+v", first)
	}
	if !got.JSON.DryRun || !strings.HasPrefix(got.Text, "would move 1 of 2 items into My Drive/Projects/Archive\n") {
		t.Errorf("the dry run does not say what it would do:\n%s", got.Text)
	}
	if fake.Count(http.MethodPatch) != 0 {
		t.Error("a dry run moved")
	}
}

func TestMoveSeveralRefusesAMalformedList(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	many := make([]string, service.MaxMoveFiles+1)
	for i := range many {
		many[i] = "id-budget-fixture"
	}
	for _, tc := range []struct {
		in   service.MoveFileInput
		want string
	}{
		{service.MoveFileInput{To: "root", Files: many}, "files holds 51 items, and one call moves at most 50"},
		{service.MoveFileInput{To: "root", File: "id-budget-fixture", Files: []string{"id-notes-fixture"}}, "not both"},
		{service.MoveFileInput{To: "root", Files: []string{"id-notes-fixture", " "}}, "files[1] is empty"},
		{service.MoveFileInput{To: "root"}, "file is required"},
	} {
		if _, err := svc.MoveFile(t.Context(), tc.in); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("err = %v, want %q", err, tc.want)
		}
	}
}
