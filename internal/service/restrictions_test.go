package service_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
	"github.com/mmedum/google-drive-mcp/v3/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v3/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v3/internal/render"
	"github.com/mmedum/google-drive-mcp/v3/internal/service"
)

// itemRestriction is the download restriction the fake holds as set on
// a file.
func itemRestriction(fake *drivetest.Server, id string) gdrive.DownloadRestriction {
	f := fake.Files[id]
	if f.DownloadRestrictions == nil || f.DownloadRestrictions.ItemDownloadRestriction == nil {
		return gdrive.DownloadRestriction{}
	}
	return *f.DownloadRestrictions.ItemDownloadRestriction
}

func TestUpdateFileRestrictsDownloads(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	for _, tc := range []struct {
		level, from, to string
		want            gdrive.DownloadRestriction
		card            string
	}{
		{"viewers", "nobody", "viewers and commenters", gdrive.DownloadRestriction{RestrictedForReaders: true},
			"\ndownloads: viewers and commenters cannot download, print or copy it\n"},
		{"editors", "viewers and commenters", "viewers, commenters and editors",
			gdrive.DownloadRestriction{RestrictedForReaders: true, RestrictedForWriters: true},
			"\ndownloads: viewers, commenters and editors cannot download, print or copy it\n"},
		{"none", "viewers, commenters and editors", "nobody", gdrive.DownloadRestriction{}, ""},
	} {
		got, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{File: "id-budget-fixture", RestrictDownload: tc.level})
		if err != nil {
			t.Fatalf("%s: %v", tc.level, err)
		}
		want := render.Change{Field: "who cannot download, print or copy it, as set on the file", From: tc.from, To: tc.to}
		if len(got.JSON.Changes) != 1 || got.JSON.Changes[0] != want {
			t.Errorf("%s: changes = %+v, want %+v", tc.level, got.JSON.Changes, want)
		}
		if r := itemRestriction(fake, "id-budget-fixture"); r != tc.want {
			t.Errorf("%s: the file holds %+v, want %+v", tc.level, r, tc.want)
		}
		if tc.card != "" && !strings.Contains(got.Text, tc.card) || tc.card == "" && strings.Contains(got.Text, "\ndownloads:") {
			t.Errorf("%s: the card does not say who cannot download:\n%s", tc.level, got.Text)
		}
	}
}

func TestUpdateFileRefusesADownloadRestrictionItCannotSet(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-theirs-fixture", "Theirs", gdrive.MimeDocument, "id-2026-fixture",
		drivetest.WithCapabilities(gdrive.Capabilities{CanEdit: true}))
	yes := true
	for _, tc := range []struct {
		in   service.UpdateFileInput
		want string
	}{
		{service.UpdateFileInput{File: "id-budget-fixture", RestrictDownload: "everyone"},
			`[invalid] restrict_download "everyone" is not one of none, viewers, editors`},
		{service.UpdateFileInput{File: "id-budget-fixture", RestrictDownload: "viewers", CopyRequiresWriterPermission: &yes},
			"[invalid] pass restrict_download or the legacy copy_requires_writer_permission, not both"},
		{service.UpdateFileInput{File: "id-theirs-fixture", RestrictDownload: "viewers"},
			"[forbidden] this account cannot change who may download Theirs"},
	} {
		if _, err := svc.UpdateFile(t.Context(), tc.in); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want it to start %q", tc.in, err, tc.want)
		}
	}
	if fake.Count(http.MethodPatch) != 0 {
		t.Error("a refused restriction reached Drive")
	}
}

// The files guide says the legacy switch set to false lifts the
// restriction on editors too, so the result says so.
func TestTheLegacySwitchOffLetsEditorsDownloadToo(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFile("id-locked-fixture", "Locked", gdrive.MimeDocument, "id-2026-fixture",
		drivetest.DownloadsRestricted(gdrive.DownloadRestriction{RestrictedForWriters: true}))
	no := false

	got, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{File: "id-locked-fixture", CopyRequiresWriterPermission: &no})
	if err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	want := []render.Change{
		{Field: "viewers and commenters may copy, print and download", From: "no", To: "yes"},
		{Field: "editors may copy, print and download", From: "no", To: "yes"},
	}
	if len(got.JSON.Changes) != 2 || got.JSON.Changes[0] != want[0] || got.JSON.Changes[1] != want[1] {
		t.Errorf("changes = %+v, want %+v", got.JSON.Changes, want)
	}
	if r := itemRestriction(fake, "id-locked-fixture"); r != (gdrive.DownloadRestriction{}) {
		t.Errorf("the file still holds %+v", r)
	}
}

// boardInArchive is a folder inside Archive, which carol can edit, with
// a file in it.
func boardInArchive(fake *drivetest.Server, opts ...drivetest.FileOpt) {
	fake.Grant("id-archive-fixture", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: "carol@example.com"})
	fake.AddFolder("id-board-fixture", "Board", "id-archive-fixture", opts...)
	fake.AddFile("id-minutes-fixture", "Minutes", gdrive.MimeDocument, "id-board-fixture")
}

func TestLimitedAccessKeepsOutWhoReachesTheFolderFromAbove(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	boardInArchive(fake)

	got, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{File: "id-board-fixture", LimitedAccess: new(true)})
	if err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	if len(got.JSON.Changes) != 1 || got.JSON.Changes[0] != (render.Change{Field: "limited access", From: "no", To: "yes"}) ||
		!got.JSON.File.LimitedAccess {
		t.Errorf("result = %+v, changes %+v", got.JSON.File, got.JSON.Changes)
	}
	out, err := svc.ListPermissions(t.Context(), service.ListPermissionsInput{File: "id-board-fixture"})
	if err != nil {
		t.Fatalf("ListPermissions: %v", err)
	}
	for _, want := range []string{
		"exposure: shared with 1 person: 1 can see it but not open it (1 of them through a folder above it)\n",
		"can see it but not open it  carol@example.com  ",
		"\nthis folder has limited access: only people added to it directly can open it\n",
		"\na grant that \"can see it but not open it\" reaches this folder from above",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list_permissions does not say %q:\n%s", want, out)
		}
	}
	inside, err := svc.ListPermissions(t.Context(), service.ListPermissionsInput{File: "id-minutes-fixture"})
	if err != nil {
		t.Fatalf("ListPermissions: %v", err)
	}
	if strings.Contains(inside, "carol") {
		t.Errorf("carol reaches a file inside a folder with limited access:\n%s", inside)
	}
}

// Turning limited access off when the folder above reaches only people
// inside the organization is an ordinary write.
func TestTurningLimitedAccessOffToPeopleInsideGoes(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	boardInArchive(fake, drivetest.LimitedAccess())

	if _, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{File: "id-board-fixture", LimitedAccess: new(false)}); err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	if fake.Files["id-board-fixture"].InheritedPermissionsDisabled {
		t.Error("the change was not made")
	}
}

// Turning limited access off is refused when the folder above would let
// anyone with the link open it, naming them, and nothing is changed
// (§4a).
func TestTurningLimitedAccessOffToAnyoneIsRefused(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	linkShared(fake)
	boardInArchive(fake, drivetest.LimitedAccess())

	_, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{File: "id-board-fixture", LimitedAccess: new(false)})
	want := "[blocked] turning its limited access off would let more people open it and everything inside it beyond " +
		"the people and groups named inside this account's organization: anyone with the link can view. Nothing was changed on " +
		"Board. Share it with them first, with share_by_link or share_outside_organization when " +
		"GDRIVE_ENABLE_DESTRUCTIVE=true, and turning limited access off then gains them nothing. One that gains " +
		"only people inside the organization is allowed."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
	if fake.Count(http.MethodPatch) != 0 || !fake.Files["id-board-fixture"].InheritedPermissionsDisabled {
		t.Fatal("the refused change reached Drive")
	}
}

// Turning limited access off when the folder above reaches nobody the
// folder does not already let in is an ordinary write.
func TestTurningLimitedAccessOffWhenNobodyCouldOpenItGoes(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFolder("id-board-fixture", "Board", "id-archive-fixture", drivetest.LimitedAccess())

	if _, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{
		File: "id-board-fixture", LimitedAccess: new(false),
	}); err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
}

// Drive gives turning limited access on and turning it off a
// capability each, and an account may hold one without the other.
func TestLimitedAccessChecksTheCapabilityForEachWay(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFolder("id-close-only-fixture", "Close only", "id-archive-fixture",
		drivetest.WithCapabilities(gdrive.Capabilities{CanListChildren: true, CanDisableInheritedPermissions: true}))
	fake.AddFolder("id-open-only-fixture", "Open only", "id-archive-fixture", drivetest.LimitedAccess(),
		drivetest.WithCapabilities(gdrive.Capabilities{CanListChildren: true, CanEnableInheritedPermissions: true}))
	if _, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{File: "id-close-only-fixture", LimitedAccess: new(true)}); err != nil {
		t.Errorf("turning it on with the right to: %v", err)
	}
	if _, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{File: "id-open-only-fixture", LimitedAccess: new(false)}); err != nil {
		t.Errorf("turning it off with the right to: %v", err)
	}
	fake.Files["id-close-only-fixture"].InheritedPermissionsDisabled = true
	fake.Files["id-open-only-fixture"].InheritedPermissionsDisabled = false
	for id, on := range map[string]bool{"id-close-only-fixture": false, "id-open-only-fixture": true} {
		_, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{File: id, LimitedAccess: new(on)})
		if err == nil || !strings.HasPrefix(err.Error(), "[forbidden] this account cannot turn limited access") {
			t.Errorf("%s, limited access %v without the right to: err = %v", id, on, err)
		}
	}
}

// The account's own access is not who limited access keeps out: when
// only the account would open the folder from above, turning it off adds
// nobody.
func TestTurningLimitedAccessOffLeavesTheAccountOutOfWhoItAdds(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFolder("id-dana-fixture", "Dana's", fake.RootID, drivetest.Owner("Dana", "dana@example.com"))
	fake.Grant("id-dana-fixture", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: drivetest.AccountEmail})
	fake.AddFolder("id-board-fixture", "Board", "id-dana-fixture", drivetest.LimitedAccess(),
		drivetest.Owner("Dana", "dana@example.com"),
		drivetest.WithCapabilities(gdrive.Capabilities{CanListChildren: true, CanEnableInheritedPermissions: true}))
	if _, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{
		File: "id-board-fixture", LimitedAccess: new(false),
	}); err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
}

func TestLimitedAccessIsAFoldersAndNeedsTheRightToSetIt(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.AddFolder("id-theirs-fixture", "Theirs", "id-archive-fixture",
		drivetest.WithCapabilities(gdrive.Capabilities{CanListChildren: true}))
	for _, tc := range []struct {
		in   service.UpdateFileInput
		want string
	}{
		{service.UpdateFileInput{File: "id-budget-fixture", LimitedAccess: new(true)},
			"[invalid] limited access is a folder's: Drive does not offer it on files"},
		{service.UpdateFileInput{File: "id-theirs-fixture", LimitedAccess: new(true)},
			"[forbidden] this account cannot turn limited access on on Theirs"},
	} {
		if _, err := svc.UpdateFile(t.Context(), tc.in); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want it to start %q", tc.in, err, tc.want)
		}
	}
	if fake.Count(http.MethodPatch) != 0 {
		t.Error("a refused change reached Drive")
	}
}

// TestSharingOffKeepsDownloadsAndLimitedAccessTight is GDRIVE_SHARING=off
// for the two newer switches: lifting either is refused, tightening goes.
func TestSharingOffKeepsDownloadsAndLimitedAccessTight(t *testing.T) {
	svc, fake := setup(t, service.Options{Sharing: config.SharingOff})
	fake.AddFile("id-locked-fixture", "Locked", gdrive.MimeDocument, "id-2026-fixture",
		drivetest.DownloadsRestricted(gdrive.DownloadRestriction{RestrictedForWriters: true}))
	fake.AddFolder("id-board-fixture", "Board", "id-archive-fixture", drivetest.LimitedAccess())
	for _, tc := range []struct {
		in   service.UpdateFileInput
		want string
	}{
		{service.UpdateFileInput{File: "id-locked-fixture", RestrictDownload: "viewers"}, "lifting its download restriction"},
		{service.UpdateFileInput{File: "id-board-fixture", LimitedAccess: new(false)}, "turning its limited access off"},
	} {
		_, err := svc.UpdateFile(t.Context(), tc.in)
		if err == nil || !strings.HasPrefix(err.Error(), "[forbidden] this server was started with GDRIVE_SHARING=off, and "+tc.want) {
			t.Errorf("%+v: err = %v, want a refusal naming %q", tc.in, err, tc.want)
		}
	}
	if fake.Count(http.MethodPatch) != 0 {
		t.Fatal("a refused loosening reached Drive")
	}
	if _, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{File: "id-budget-fixture", RestrictDownload: "editors"}); err != nil {
		t.Errorf("tightening downloads was refused: %v", err)
	}
	if _, err := svc.UpdateFile(t.Context(), service.UpdateFileInput{File: "id-archive-fixture", LimitedAccess: new(true)}); err != nil {
		t.Errorf("turning limited access on was refused: %v", err)
	}
}

// Someone who sees a limited-access folder without opening it does not
// reach what is moved into it, so the move does not count them.
func TestAMoveIntoALimitedFolderDoesNotCountWhoOnlySeesIt(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	linkShared(fake)
	fake.AddFolder("id-board-fixture", "Board", "id-archive-fixture", drivetest.LimitedAccess())

	got, err := svc.MoveFile(t.Context(), service.MoveFileInput{
		File: "id-budget-fixture", To: "id-board-fixture",
	})
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got.JSON.SharingAfter != "private to you" || got.JSON.Note != "" {
		t.Errorf("sharing after %q, note %q", got.JSON.SharingAfter, got.JSON.Note)
	}
}
