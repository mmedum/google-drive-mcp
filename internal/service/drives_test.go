package service_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
	"github.com/mmedum/google-drive-mcp/v3/internal/service"
)

func TestListDrivesShowsWhatThisAccountMayDo(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	out, err := svc.ListDrives(t.Context(), service.ListDrivesInput{})
	if err != nil {
		t.Fatalf("ListDrives: %v", err)
	}
	if !strings.Contains(out, "Marketing") || !strings.Contains(out, "id-drive-marketing") {
		t.Errorf("the drive or its id is missing:\n%s", out)
	}
	// The id is in every row because the name is not unique and never was.
	if !strings.Contains(out, "you can: ") {
		t.Errorf("the listing does not say what this account may do:\n%s", out)
	}
}

func TestHiddenDrivesAreLeftOutUnlessAskedFor(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddDrive("id-drive-old", "Old campaigns")
	fake.Drives["id-drive-old"].Hidden = true

	out, err := svc.ListDrives(t.Context(), service.ListDrivesInput{})
	if err != nil {
		t.Fatalf("ListDrives: %v", err)
	}
	if strings.Contains(out, "Old campaigns") {
		t.Errorf("a hidden drive was listed by default:\n%s", out)
	}
	if !strings.Contains(out, "include_hidden") {
		t.Errorf("the listing does not say how to see it:\n%s", out)
	}

	out, err = svc.ListDrives(t.Context(), service.ListDrivesInput{IncludeHidden: true})
	if err != nil {
		t.Fatalf("ListDrives: %v", err)
	}
	if !strings.Contains(out, "Old campaigns (hidden)") {
		t.Errorf("include_hidden did not show it:\n%s", out)
	}
}

func TestCreateDriveRefusesASecondOfTheSameName(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	_, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "create", Name: "Marketing",
	})
	if err == nil {
		t.Fatal("a second shared drive of that name was created")
	}
	if !strings.HasPrefix(err.Error(), "[exists]") {
		t.Errorf("err = %v, want an exists class", err)
	}
}

func TestCreateDriveMakesOneAndSaysWhatItMeans(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	got, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "create", Name: "Research",
	})
	if err != nil {
		t.Fatalf("ManageDrive: %v", err)
	}
	if got.JSON.Drive == nil || got.JSON.Drive.Name != "Research" {
		t.Fatalf("drive = %+v", got.JSON.Drive)
	}
	if fake.Drives[got.JSON.Drive.ID] == nil {
		t.Fatalf("the drive was not created: %+v", fake.Drives)
	}
	// Ownership passing to the organization is the difference from a
	// folder, and it does not come undone.
	if !strings.Contains(got.Text, "belongs to the organization") {
		t.Errorf("the result does not say what a shared drive means:\n%s", got.Text)
	}
	// The requestId is what makes the create idempotent.
	if id := lastQuery(t, fake, "POST", "/drives", "requestId"); id == "" {
		t.Error("drives.create was sent without a requestId, so a retry could make a second drive")
	}
}

func TestRenameHideAndUnhideADrive(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	if _, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "rename", Drive: "Marketing", Name: "Marketing and comms",
	}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if fake.Drives["id-drive-marketing"].Name != "Marketing and comms" {
		t.Errorf("name = %q", fake.Drives["id-drive-marketing"].Name)
	}

	got, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "hide", Drive: "id-drive-marketing",
	})
	if err != nil {
		t.Fatalf("hide: %v", err)
	}
	if !fake.Drives["id-drive-marketing"].Hidden {
		t.Error("the drive is not hidden")
	}
	if !strings.Contains(got.Text, "as reachable as any other") {
		t.Errorf("the result does not say hiding is only a display choice:\n%s", got.Text)
	}

	// Hiding what is already hidden changes nothing, and says so.
	again, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "hide", Drive: "id-drive-marketing",
	})
	if err != nil {
		t.Fatalf("hide again: %v", err)
	}
	if again.JSON.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", again.JSON.Action)
	}

	if _, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "unhide", Drive: "id-drive-marketing",
	}); err != nil {
		t.Fatalf("unhide: %v", err)
	}
	if fake.Drives["id-drive-marketing"].Hidden {
		t.Error("the drive is still hidden")
	}
}

func TestRestrictKeepsTheSwitchesItWasNotAskedToChange(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	// Drive replaces the whole restrictions object, so a patch that sent
	// only the switch being changed would silently clear the rest.
	if _, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "restrict", Drive: "Marketing",
		Restrictions: map[string]bool{"members_only": true, "domain_users_only": true},
	}); err != nil {
		t.Fatalf("restrict: %v", err)
	}
	if _, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "restrict", Drive: "Marketing",
		Restrictions: map[string]bool{"copy_requires_writer_permission": true},
	}); err != nil {
		t.Fatalf("restrict again: %v", err)
	}
	r := fake.Drives["id-drive-marketing"].Restrictions
	if !r.DriveMembersOnly || !r.DomainUsersOnly || !r.CopyRequiresWriterPermission {
		t.Errorf("a later restrict cleared an earlier one: %+v", r)
	}
}

func TestRestrictReportsOnlyWhatActuallyChanged(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	got, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "restrict", Drive: "Marketing",
		Restrictions: map[string]bool{"members_only": true, "domain_users_only": false},
	})
	if err != nil {
		t.Fatalf("restrict: %v", err)
	}
	if len(got.JSON.Changes) != 1 {
		t.Fatalf("changes = %+v, want only the one that moved", got.JSON.Changes)
	}
	if !strings.Contains(got.JSON.Changes[0].Field, "members_only") {
		t.Errorf("change = %+v", got.JSON.Changes[0])
	}

	unchanged, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "restrict", Drive: "Marketing",
		Restrictions: map[string]bool{"members_only": true},
	})
	if err != nil {
		t.Fatalf("restrict: %v", err)
	}
	if unchanged.JSON.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", unchanged.JSON.Action)
	}
}

// TestRestrictTurnsASwitchOffAndSendsOnlyWhatChanges holds what a live
// probe found: Drive merges restrictions switch by switch, so turning
// one off has to send false, and it refuses downloadRestriction in the
// body, which a drive always carries.
func TestRestrictTurnsASwitchOffAndSendsOnlyWhatChanges(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	for _, on := range []bool{true, false} {
		got, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
			Action: "restrict", Drive: "Marketing",
			Restrictions: map[string]bool{"copy_requires_writer_permission": on},
		})
		if err != nil {
			t.Fatalf("restrict to %v: %v", on, err)
		}
		r := fake.Drives["id-drive-marketing"].Restrictions
		if r.CopyRequiresWriterPermission != on {
			t.Errorf("copy_requires_writer_permission = %v, want %v", r.CopyRequiresWriterPermission, on)
		}
		// The copy switch and the readers' download restriction move
		// together, and both are reported.
		if len(got.JSON.Changes) != 2 || got.JSON.Note != "" {
			t.Errorf("changes %+v, note %q", got.JSON.Changes, got.JSON.Note)
		}
	}
}

// Turning domain_users_only off lets a drive's content reach people
// outside the organization, so manage_drive refuses it, dry run
// included, naming lift_drive_restriction; the same call with it on is
// fine, and so is turning members_only off, which waits on a live probe
// (§17).
func TestManageDriveLeavesDomainUsersOnlyOffToItsOwnTool(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Drives["id-drive-marketing"].Restrictions.DomainUsersOnly = true
	fake.Drives["id-drive-marketing"].Restrictions.DriveMembersOnly = true

	for _, dry := range []bool{true, false} {
		_, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
			Action: "restrict", Drive: "Marketing", DryRun: dry,
			Restrictions: map[string]bool{"domain_users_only": false, "members_only": false},
		})
		want := "[blocked] manage_drive does not turn off domain_users_only on Marketing: that lets its content " +
			"reach people outside the organization. Nothing was changed. lift_drive_restriction does it, when " +
			"GDRIVE_ENABLE_DESTRUCTIVE=true."
		if err == nil || err.Error() != want {
			t.Errorf("dry run %v: err = %v\nwant %s", dry, err, want)
		}
	}
	if fake.Count(http.MethodPatch) != 0 {
		t.Fatal("the refused change reached Drive")
	}
	if _, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "restrict", Drive: "Marketing", Restrictions: map[string]bool{"members_only": false},
	}); err != nil {
		t.Fatalf("turning members_only off: %v", err)
	}
	if r := fake.Drives["id-drive-marketing"].Restrictions; r.DriveMembersOnly || !r.DomainUsersOnly {
		t.Errorf("restrictions = %+v, want members_only off and domain_users_only still on", r)
	}
}

// lift_drive_restriction turns domain_users_only off, on the flag and
// confirm: true, and nothing else.
func TestLiftDriveRestriction(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Drives["id-drive-marketing"].Restrictions.DomainUsersOnly = true
	in := service.LiftDriveRestrictionInput{Drive: "Marketing", Restriction: "domain_users_only", Confirm: true}
	if _, err := svc.LiftDriveRestriction(t.Context(), in); err == nil ||
		!strings.HasPrefix(err.Error(), "[forbidden] lift_drive_restriction opens a file or a shared drive past") {
		t.Fatalf("without the destructive flag: err = %v", err)
	}

	svc, fake = setup(t, service.Options{Destructive: true})
	fake.Drives["id-drive-marketing"].Restrictions.DomainUsersOnly = true
	for _, tc := range []struct {
		in   service.LiftDriveRestrictionInput
		want string
	}{
		{service.LiftDriveRestrictionInput{Drive: "Marketing", Restriction: "domain_users_only"},
			"[forbidden] lift_drive_restriction would turn off domain_users_only on the shared drive Marketing, " +
				"which lets what is in it be shared with people outside the organization. Pass confirm: true"},
		{service.LiftDriveRestrictionInput{Drive: "Marketing", Restriction: "members_only", Confirm: true},
			"[invalid] lift_drive_restriction turns off only domain_users_only; manage_drive with action restrict " +
				"turns members_only off."},
		{service.LiftDriveRestrictionInput{Drive: "Marketing", Restriction: "everything", Confirm: true},
			`[invalid] restriction "everything" is not one of domain_users_only`},
		{service.LiftDriveRestrictionInput{Restriction: "domain_users_only", Confirm: true},
			"[invalid] drive is required"},
	} {
		if _, err := svc.LiftDriveRestriction(t.Context(), tc.in); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v\nwant it to start %q", tc.in, err, tc.want)
		}
	}
	if fake.Count(http.MethodPatch) != 0 {
		t.Fatal("a refused lift reached Drive")
	}

	got, err := svc.LiftDriveRestriction(t.Context(), in)
	if err != nil {
		t.Fatalf("LiftDriveRestriction: %v", err)
	}
	if fake.Drives["id-drive-marketing"].Restrictions.DomainUsersOnly {
		t.Error("domain_users_only is still on")
	}
	if len(got.JSON.Changes) != 1 || got.JSON.Changes[0].Field != "restriction domain_users_only" {
		t.Errorf("changes = %+v", got.JSON.Changes)
	}
	again, err := svc.LiftDriveRestriction(t.Context(), in)
	if err != nil || again.JSON.Action != "unchanged" {
		t.Errorf("lifting it again: action %v, err %v; want unchanged", again, err)
	}
}

// TestSharingOffKeepsRestrictionsOn holds GDRIVE_SHARING=off to its
// word for shared drives: turning a restriction off widens who can reach
// what is inside, and turning one on is still allowed.
func TestSharingOffKeepsRestrictionsOn(t *testing.T) {
	svc, fake := setup(t, service.Options{Sharing: config.SharingOff})
	fake.Drives["id-drive-marketing"].Restrictions.DomainUsersOnly = true

	_, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "restrict", Drive: "Marketing",
		Restrictions: map[string]bool{"domain_users_only": false},
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[forbidden]") || !strings.Contains(err.Error(), "domain_users_only") {
		t.Fatalf("err = %v, want a forbidden refusal naming domain_users_only", err)
	}
	if !fake.Drives["id-drive-marketing"].Restrictions.DomainUsersOnly {
		t.Error("the refusal still turned the restriction off")
	}
	if _, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "restrict", Drive: "Marketing",
		Restrictions: map[string]bool{"members_only": true},
	}); err != nil {
		t.Fatalf("turning a restriction on was refused: %v", err)
	}
}

// TestSharingOffKeepsFileSwitchesTight is the same rule for a file's
// own sharing switches in update_file.
func TestSharingOffKeepsFileSwitchesTight(t *testing.T) {
	svc, fake := setup(t, service.Options{Sharing: config.SharingOff})
	fake.Files["id-budget-fixture"].CopyRequiresWriterPermission = true
	yes, no := true, false
	for _, in := range []service.UpdateFileInput{
		{File: "id-budget-fixture", CopyRequiresWriterPermission: &no},
		{File: "id-budget-fixture", WritersCanShare: &yes},
	} {
		if _, err := svc.UpdateFile(t.Context(), in); err == nil || !strings.HasPrefix(err.Error(), "[forbidden]") {
			t.Errorf("%+v: err = %v, want a forbidden refusal", in, err)
		}
	}
	if !fake.Files["id-budget-fixture"].CopyRequiresWriterPermission {
		t.Error("the refusal still loosened the file")
	}
	if _, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{File: "id-budget-fixture", WritersCanShare: &no}); err != nil {
		t.Errorf("tightening was refused: %v", err)
	}
}

func TestManageDriveRefusesWhatItCannotDo(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	for _, tc := range []struct {
		name string
		in   service.ManageDriveInput
		want string
	}{
		{"an unknown action", service.ManageDriveInput{Action: "destroy", Drive: "Marketing"}, "not one of"},
		{"no action at all", service.ManageDriveInput{Drive: "Marketing"}, "action is required"},
		{"create with no name", service.ManageDriveInput{Action: "create"}, "name is required"},
		{"create naming a drive", service.ManageDriveInput{Action: "create", Name: "x", Drive: "Marketing"}, "takes name, not drive"},
		{"rename with no drive", service.ManageDriveInput{Action: "rename", Name: "x"}, "drive is required"},
		{"rename with no name", service.ManageDriveInput{Action: "rename", Drive: "Marketing"}, "name is required"},
		{"a restriction that is not one", service.ManageDriveInput{Action: "restrict", Drive: "Marketing",
			Restrictions: map[string]bool{"no_such_switch": true}}, "not one of"},
		{"a drive that is not there", service.ManageDriveInput{Action: "rename", Drive: "Nope", Name: "x"}, "no shared drive \"Nope\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.ManageDrive(t.Context(), tc.in)
			if err == nil {
				t.Fatalf("%+v was accepted", tc.in)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestDryRunChangesNoDrive(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	got, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "rename", Drive: "Marketing", Name: "Renamed", DryRun: true,
	})
	if err != nil {
		t.Fatalf("ManageDrive: %v", err)
	}
	if fake.Drives["id-drive-marketing"].Name != "Marketing" {
		t.Error("a dry run renamed the drive")
	}
	if !strings.Contains(got.Text, "NOTHING WAS CHANGED") {
		t.Errorf("the dry run does not say so:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "\nwould change:\n  name: Marketing → Renamed\n") || strings.Contains(got.Text, "\nchanged:") {
		t.Errorf("the dry run heads its changes as done:\n%s", got.Text)
	}
}

func TestManageDriveIsNotAvailableReadOnly(t *testing.T) {
	svc, _ := setup(t, service.Options{ReadOnly: true})
	if _, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "create", Name: "Research",
	}); err == nil {
		t.Fatal("manage_drive worked on a read-only server")
	}
	// The read stays: seeing the drives is not changing them.
	if _, err := svc.ListDrives(t.Context(), service.ListDrivesInput{}); err != nil {
		t.Errorf("ListDrives on a read-only server: %v", err)
	}
}

func TestRestrictWithNothingToRestrictSaysSo(t *testing.T) {
	// It used to report "every restriction passed already had that
	// value" when none had been passed at all — a falsehood in the shape
	// of a reassurance.
	svc, _ := setup(t, service.Options{})
	_, err := svc.ManageDrive(t.Context(), service.ManageDriveInput{
		Action: "restrict", Drive: "Marketing",
	})
	if err == nil {
		t.Fatal("restrict with no restrictions was accepted")
	}
	if !strings.Contains(err.Error(), "restrictions is required") {
		t.Errorf("err = %v, want it to name the missing argument", err)
	}
	if !strings.Contains(err.Error(), "members_only") {
		t.Errorf("the refusal does not name what may be passed: %v", err)
	}
}

// TestAFreshlyCreatedDriveIsReachableByID holds the fix for a defect the
// first destructive live run found.
//
// `drives.list` is eventually consistent and `drives.get` is not, so a
// shared drive created moments ago answers by id while the listing still
// does not carry it. Every tool taking a `drive` argument resolved
// through the listing alone, so the driver made a scratch drive, wrote
// files into it by that id, and was then told there was no drive of that
// NAME — with a list of unrelated drive names — when it tried to empty
// its trash and delete it. The run could not clean up after itself.
func TestAFreshlyCreatedDriveIsReachableByID(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})
	fake.AddDrive("id-drive-fresh", "Fresh campaigns")
	// Exists, answers by id, and the listing has not caught up. Every
	// other drive is hidden from the listing too, so the listing comes
	// back EMPTY: an account whose only shared drive is the one just
	// created is the exact case this is for, and a fixture with another
	// drive in it would take a different branch.
	fake.UnlistedDrives = map[string]bool{"id-drive-fresh": true}
	for id := range fake.Drives {
		fake.UnlistedDrives[id] = true
	}

	out, err := svc.EmptyTrash(t.Context(), service.EmptyTrashInput{
		Drive: "id-drive-fresh", Confirm: true,
	})
	if err != nil {
		t.Fatalf("a drive missing only from the listing was unreachable by its own id: %v", err)
	}
	if out == nil {
		t.Fatal("no result")
	}

	// A name still cannot be found that way, because only the listing
	// carries names — and the refusal must not claim it looked for a
	// name when it was handed something else.
	_, err = svc.EmptyTrash(t.Context(), service.EmptyTrashInput{
		Drive: "Fresh campaigns", Confirm: true,
	})
	if err == nil {
		t.Fatal("a name absent from the listing resolved anyway")
	}
	if !strings.Contains(err.Error(), "can see no shared drives") {
		t.Errorf("an empty listing was not reported as one:\n%v", err)
	}
}

// TestANameStillNeedsTheListing keeps the fallback from being read as a
// general escape: only the listing carries names, and the refusal must
// not claim it looked for a name when it was handed something else.
func TestANameStillNeedsTheListing(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})
	fake.AddDrive("id-drive-fresh", "Fresh campaigns")
	fake.UnlistedDrives = map[string]bool{"id-drive-fresh": true}

	_, err := svc.EmptyTrash(t.Context(), service.EmptyTrashInput{
		Drive: "Nowhere", Confirm: true,
	})
	if err == nil {
		t.Fatal("a name that is in no listing resolved anyway")
	}
	if !strings.Contains(err.Error(), "no drive with that id either") {
		t.Errorf("the refusal does not say both readings were tried:\n%v", err)
	}
}
