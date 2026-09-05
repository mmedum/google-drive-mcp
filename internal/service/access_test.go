package service_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/config"
	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

func TestShareFileGrantsAndShowsExposureBeforeAndAfter(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	got, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com", Role: "writer",
	})
	if err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	perms := fake.Permissions["id-budget-fixture"]
	if len(perms) != 1 || perms[0].EmailAddress != "alice@example.com" || perms[0].Role != "writer" {
		t.Fatalf("permissions = %+v", perms)
	}
	if got.JSON.SharingBefore == "" || got.JSON.SharingAfter == "" {
		t.Errorf("a share reported no before and after: %+v", got.JSON)
	}
	if got.JSON.SharingBefore == got.JSON.SharingAfter {
		t.Errorf("exposure did not change in the report: %q", got.JSON.SharingAfter)
	}
	if !strings.Contains(got.JSON.SharingAfter, "1 can edit") {
		t.Errorf("the exposure after does not describe the new grant: %q", got.JSON.SharingAfter)
	}
	// No mail unless it is asked for; Drive's own default is the other
	// way round, which is why this is asserted on the wire.
	if sent := lastQuery(t, fake, http.MethodPost, "/permissions", "sendNotificationEmail"); sent != "false" {
		t.Errorf("sendNotificationEmail = %q, want false by default", sent)
	}
	if !strings.Contains(got.Text, "no email was sent") {
		t.Errorf("the result does not say no mail went out:\n%s", got.Text)
	}
}

func TestShareFileUpdatesAnExistingGrantRatherThanAddingASecond(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-budget-fixture", &gdrive.Permission{
		Type: "user", Role: "reader", EmailAddress: "alice@example.com",
	})

	got, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com", Role: "writer",
	})
	if err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	perms := fake.Permissions["id-budget-fixture"]
	if len(perms) != 1 {
		t.Fatalf("granting again made %d permissions, want the first one changed", len(perms))
	}
	if perms[0].Role != "writer" {
		t.Errorf("role = %q", perms[0].Role)
	}
	if !strings.Contains(got.Text, "can view → can edit") {
		t.Errorf("the result does not show the role before and after:\n%s", got.Text)
	}
}

func TestShareFileSaysNothingChangedWhenTheGrantIsAlreadyThere(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-budget-fixture", &gdrive.Permission{
		Type: "user", Role: "writer", EmailAddress: "alice@example.com",
	})
	before := fake.Count(http.MethodPost)

	got, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com", Role: "writer",
	})
	if err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	if got.JSON.Action != "unchanged" {
		t.Errorf("action = %q, want unchanged", got.JSON.Action)
	}
	if after := fake.Count(http.MethodPost) - before; after != 0 {
		t.Errorf("a repeat of an identical grant made %d writes", after)
	}
}

func TestAnyoneLinkNeedsTheAcknowledgement(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	_, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "anyone", Role: "reader",
	})
	if err == nil {
		t.Fatal("a public link was granted without allow_anyone")
	}
	if !strings.HasPrefix(err.Error(), "[forbidden]") {
		t.Errorf("err = %v, want a forbidden class", err)
	}
	if len(fake.Permissions["id-budget-fixture"]) != 0 {
		t.Error("the refusal still wrote a permission")
	}

	got, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "anyone", Role: "reader", AllowAnyone: true,
	})
	if err != nil {
		t.Fatalf("ShareFile with allow_anyone: %v", err)
	}
	if !strings.Contains(got.Text, "anyone with the link") {
		t.Errorf("the result does not name the exposure it created:\n%s", got.Text)
	}
	// Drive refuses sendNotificationEmail for anything but a user or a
	// group, so it must not be on the request at all.
	if sent := lastQuery(t, fake, http.MethodPost, "/permissions", "sendNotificationEmail"); sent != "" {
		t.Errorf("sendNotificationEmail = %q on an anyone grant, want it absent", sent)
	}
}

func TestOwnershipTransferNeedsItsOwnAcknowledgement(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	_, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com", Role: "owner",
	})
	if err == nil {
		t.Fatal("ownership was transferred without transfer_ownership")
	}
	if !strings.Contains(err.Error(), "transfer_ownership") {
		t.Errorf("the refusal does not name what to pass: %v", err)
	}

	got, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com", Role: "owner",
		TransferOwnership: true,
	})
	if err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	// Google always mails a transfer and refuses to have it turned off,
	// so the request must ask for it and the result must say so.
	if sent := lastQuery(t, fake, http.MethodPost, "/permissions", "sendNotificationEmail"); sent != "true" {
		t.Errorf("sendNotificationEmail = %q on a transfer, want true", sent)
	}
	if !strings.Contains(got.Text, "always mails") {
		t.Errorf("the result does not say mail is unavoidable:\n%s", got.Text)
	}
}

func TestSharedDriveRolesAreRefusedInMyDriveAndTheOtherWayRound(t *testing.T) {
	svc, _ := setup(t, service.Options{})

	_, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com", Role: "organizer",
	})
	if err == nil || !strings.Contains(err.Error(), "shared-drive roles") {
		t.Errorf("organizer in My Drive: %v", err)
	}

	_, err = svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-q3-plan-fixture", Principal: "alice@example.com", Role: "owner",
		TransferOwnership: true,
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[unsupported]") {
		t.Errorf("ownership transfer inside a shared drive: %v", err)
	}
}

func TestShareRefusedWhenDriveSaysThisAccountCannotShare(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Files["id-budget-fixture"].Capabilities = &gdrive.Capabilities{CanEdit: true, CanShare: false}

	_, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com", Role: "reader",
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[forbidden]") {
		t.Fatalf("err = %v, want forbidden", err)
	}
	if fake.Count("/permissions") != 0 {
		t.Error("the capability check did not happen before the call")
	}
}

func TestAPolicyRefusalComesBackAsBlockedWithGooglesOwnWords(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Fail = drivetest.FailTimes(1, "/permissions", drivetest.Failure{
		Status: http.StatusForbidden, Reason: "domainPolicy",
		Message: "The domain administrators have disabled Drive apps.",
	})

	_, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "outsider@example.net", Role: "reader",
	})
	if err == nil {
		t.Fatal("a policy refusal was not reported")
	}
	if !strings.HasPrefix(err.Error(), "[blocked]") {
		t.Errorf("err = %v, want a blocked class", err)
	}
	if !strings.Contains(err.Error(), "The domain administrators have disabled Drive apps.") {
		t.Errorf("Google's own message is missing: %v", err)
	}
	if !strings.Contains(err.Error(), "administrator") {
		t.Errorf("the refusal does not say who decides: %v", err)
	}
}

func TestExpiryIsCheckedAgainstDrivesOwnRules(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	for _, tc := range []struct {
		name, principal, expires, want string
	}{
		{"a link grant cannot expire", "anyone", "30d", "person or a group"},
		{"the past is refused", "alice@example.com", "2020-01-01", "not in the future"},
		{"more than a year is refused", "alice@example.com", "400d", "more than a year"},
		{"nonsense is refused", "alice@example.com", "next tuesday", "neither a date"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.ShareFile(t.Context(), service.ShareFileInput{
				File: "id-budget-fixture", Principal: tc.principal, Role: "reader",
				Expires: tc.expires, AllowAnyone: true,
			})
			if err == nil {
				t.Fatalf("%s was accepted", tc.expires)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %v, want it to mention %q", err, tc.want)
			}
		})
	}

	if _, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com", Role: "reader", Expires: "30d",
	}); err != nil {
		t.Fatalf("a valid expiry was refused: %v", err)
	}
	perms := fake.Permissions["id-budget-fixture"]
	if len(perms) != 1 || perms[0].ExpirationTime == "" {
		t.Fatalf("no expiry was sent: %+v", perms)
	}
	want := testNow.Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if perms[0].ExpirationTime != want {
		t.Errorf("expiry = %q, want %q", perms[0].ExpirationTime, want)
	}
}

func TestPrincipalFormsAreParsed(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	for _, tc := range []struct{ in, kind, who string }{
		{"alice@example.com", "user", "alice@example.com"},
		{"user:bob@example.com", "user", "bob@example.com"},
		{"group:team@example.com", "group", "team@example.com"},
		{"domain:example.com", "domain", "example.com"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			if _, err := svc.ShareFile(t.Context(), service.ShareFileInput{
				File: "id-notes-fixture", Principal: tc.in, Role: "reader",
			}); err != nil {
				t.Fatalf("ShareFile(%q): %v", tc.in, err)
			}
			var got *gdrive.Permission
			for _, p := range fake.Permissions["id-notes-fixture"] {
				if p.EmailAddress == tc.who || p.Domain == tc.who {
					got = p
				}
			}
			if got == nil {
				t.Fatalf("no grant for %q: %+v", tc.who, fake.Permissions["id-notes-fixture"])
			}
			if got.Type != tc.kind {
				t.Errorf("type = %q, want %q", got.Type, tc.kind)
			}
		})
	}
	for _, bad := range []string{"", "alice", "domain:alice@example.com", "group:team"} {
		if _, err := svc.ShareFile(t.Context(), service.ShareFileInput{
			File: "id-notes-fixture", Principal: bad, Role: "reader",
		}); err == nil {
			t.Errorf("%q was accepted as a principal", bad)
		}
	}
}

func TestUnshareRemovesOneGrantAndSaysWhatIsLeft(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-budget-fixture", &gdrive.Permission{Type: "user", Role: "writer", EmailAddress: "alice@example.com"})
	fake.Grant("id-budget-fixture", &gdrive.Permission{Type: "user", Role: "reader", EmailAddress: "bob@example.com"})

	got, err := svc.UnshareFile(t.Context(), service.UnshareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com",
	})
	if err != nil {
		t.Fatalf("UnshareFile: %v", err)
	}
	left := fake.Permissions["id-budget-fixture"]
	if len(left) != 1 || left[0].EmailAddress != "bob@example.com" {
		t.Fatalf("left = %+v", left)
	}
	if !strings.Contains(got.JSON.SharingAfter, "1 can view") {
		t.Errorf("the exposure after does not describe what is left: %q", got.JSON.SharingAfter)
	}
	if got.JSON.Action != "unshared" {
		t.Errorf("action = %q", got.JSON.Action)
	}
}

func TestUnshareRemovesTheLinkGrant(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-budget-fixture", &gdrive.Permission{Type: "anyone", Role: "reader"})

	got, err := svc.UnshareFile(t.Context(), service.UnshareFileInput{
		File: "id-budget-fixture", RemoveLink: true,
	})
	if err != nil {
		t.Fatalf("UnshareFile: %v", err)
	}
	if len(fake.Permissions["id-budget-fixture"]) != 0 {
		t.Errorf("the link grant is still there: %+v", fake.Permissions["id-budget-fixture"])
	}
	if !strings.Contains(got.Text, "request-access") {
		t.Errorf("the result does not say what happens to the old link:\n%s", got.Text)
	}
}

func TestUnshareNeedsExactlyOneWayOfNamingTheGrant(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	for _, in := range []service.UnshareFileInput{
		{File: "id-budget-fixture"},
		{File: "id-budget-fixture", Principal: "alice@example.com", RemoveLink: true},
		{File: "id-budget-fixture", Principal: "alice@example.com", PermissionID: "id-permission-1"},
	} {
		if _, err := svc.UnshareFile(t.Context(), in); err == nil {
			t.Errorf("%+v was accepted", in)
		}
	}
}

func TestAnInheritedGrantIsRefusedWithItsSourceNamed(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	// A member of the shared drive reaches everything in it. The grant
	// lives on the drive, and Drive reports it on the item as inherited.
	fake.Grant("id-drive-marketing", &gdrive.Permission{
		Type: "user", Role: "writer", EmailAddress: "alice@example.com",
	})

	out, err := svc.ListPermissions(t.Context(), service.ListPermissionsInput{File: "id-q3-plan-fixture"})
	if err != nil {
		t.Fatalf("ListPermissions: %v", err)
	}
	if !strings.Contains(out, "inherited from") {
		t.Errorf("the listing does not mark the inherited grant:\n%s", out)
	}
	if !strings.Contains(out, "only be removed where it was granted") {
		t.Errorf("the listing does not say an inherited grant cannot be removed here:\n%s", out)
	}

	_, err = svc.UnshareFile(t.Context(), service.UnshareFileInput{
		File: "id-q3-plan-fixture", Principal: "alice@example.com",
	})
	if err == nil {
		t.Fatal("an inherited grant was removed on the file")
	}
	if !strings.HasPrefix(err.Error(), "[forbidden]") {
		t.Errorf("err = %v, want a forbidden class", err)
	}
	if !strings.Contains(err.Error(), "id-drive-marketing") {
		t.Errorf("the refusal does not name where the grant came from: %v", err)
	}
	if len(fake.Permissions["id-drive-marketing"]) != 1 {
		t.Error("the refusal still removed the grant at its source")
	}
}

func TestAnOwnersAccessIsTransferredNotRevoked(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-budget-fixture", &gdrive.Permission{
		Type: "user", Role: "owner", EmailAddress: drivetest.AccountEmail,
	})

	_, err := svc.UnshareFile(t.Context(), service.UnshareFileInput{
		File: "id-budget-fixture", Principal: drivetest.AccountEmail,
	})
	if err == nil || !strings.Contains(err.Error(), "transfer_ownership") {
		t.Errorf("err = %v, want a pointer at the transfer", err)
	}
}

func TestDryRunSharesNothing(t *testing.T) {
	svc, fake := setup(t, service.Options{})

	got, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com", Role: "writer", DryRun: true,
	})
	if err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	if len(fake.Permissions["id-budget-fixture"]) != 0 {
		t.Fatal("a dry run granted access")
	}
	if !got.JSON.DryRun || !strings.Contains(got.Text, "NOTHING WAS CHANGED") {
		t.Errorf("the dry run does not say so:\n%s", got.Text)
	}
	if !strings.HasPrefix(got.Text, "would have shared") {
		t.Errorf("the lead line reads as though it happened:\n%s", got.Text)
	}
}

func TestSharingOffRemovesTheWritesAndKeepsTheRead(t *testing.T) {
	svc, fake := setup(t, service.Options{Sharing: config.SharingOff})
	fake.Grant("id-budget-fixture", &gdrive.Permission{Type: "anyone", Role: "reader"})

	for _, call := range []func() error{
		func() error {
			_, err := svc.ShareFile(t.Context(), service.ShareFileInput{
				File: "id-budget-fixture", Principal: "alice@example.com", Role: "reader"})
			return err
		},
		func() error {
			_, err := svc.UnshareFile(t.Context(), service.UnshareFileInput{
				File: "id-budget-fixture", RemoveLink: true})
			return err
		},
	} {
		err := call()
		if err == nil {
			t.Fatal("a sharing write went through with GDRIVE_SHARING=off")
		}
		if !strings.Contains(err.Error(), "GDRIVE_SHARING=off") {
			t.Errorf("the refusal does not name the setting: %v", err)
		}
	}

	// Seeing exposure is not widening it, so the read stays.
	out, err := svc.ListPermissions(t.Context(), service.ListPermissionsInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("ListPermissions with sharing off: %v", err)
	}
	if !strings.Contains(out, "anyone with the link") {
		t.Errorf("the listing hid the exposure:\n%s", out)
	}
}

func TestListPermissionsOnASharedDriveShowsItsMembers(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-drive-marketing", &gdrive.Permission{
		Type: "user", Role: "organizer", EmailAddress: "alice@example.com",
	})

	out, err := svc.ListPermissions(t.Context(), service.ListPermissionsInput{File: "drive:Marketing"})
	if err != nil {
		t.Fatalf("ListPermissions: %v", err)
	}
	if !strings.Contains(out, "the shared drive's members") {
		t.Errorf("the listing does not say these are members:\n%s", out)
	}
	if !strings.Contains(out, "manages the drive") {
		t.Errorf("the organizer role is not in plain words:\n%s", out)
	}
}

// lastQuery returns a query parameter from the most recent request with
// this method whose path contains the fragment given, so a test can
// assert what actually went on the wire rather than what the code meant
// to send. The method matters: a share is a POST followed by the GET
// that reads the exposure back, and without it every assertion would
// land on the read.
func lastQuery(t *testing.T, fake *drivetest.Server, method, pathContains, key string) string {
	t.Helper()
	got := ""
	found := false
	for _, r := range fake.Requests {
		if r.Method == method && strings.Contains(r.Path, pathContains) {
			got, found = r.Query.Get(key), true
		}
	}
	if !found {
		t.Fatalf("no %s to a path containing %q was made", method, pathContains)
	}
	return got
}

func TestUnshareSaysSoWhenItCannotReadWhoHasAccess(t *testing.T) {
	// "They have no grant" would be a guess dressed as a fact: the list
	// could not be read, so what is on the file is unknown and removing
	// the right thing is impossible.
	svc, fake := setup(t, service.Options{})
	fake.Fail = func(r *http.Request) *drivetest.Failure {
		if strings.HasSuffix(r.URL.Path, "/permissions") && r.Method == http.MethodGet {
			return &drivetest.Failure{Status: http.StatusForbidden,
				Reason: "insufficientFilePermissions", Message: "cannot read permissions"}
		}
		return nil
	}
	_, err := svc.UnshareFile(t.Context(), service.UnshareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com",
	})
	if err == nil {
		t.Fatal("an unshare went ahead with the permission list unreadable")
	}
	if strings.Contains(err.Error(), "no grant on this file") {
		t.Errorf("the refusal claims the grant is absent rather than unknown: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot read who has access") {
		t.Errorf("the refusal does not say what actually went wrong: %v", err)
	}
}

func TestChangingARoleDoesNotSilentlyNarrowALinkGrant(t *testing.T) {
	// discoverable was a plain bool, so "not passed" and "false" were the
	// same request: changing the role on a file that was findable by
	// search quietly made it by-link-only, which the caller never asked
	// for and could not opt out of.
	svc, fake := setup(t, service.Options{})
	fake.Grant("id-budget-fixture", &gdrive.Permission{
		Type: "anyone", Role: "reader", AllowFileDiscovery: true,
	})

	got, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "anyone", Role: "writer", AllowAnyone: true,
	})
	if err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	if !fake.Permissions["id-budget-fixture"][0].AllowFileDiscovery {
		t.Error("changing the role turned off findability nobody asked to change")
	}
	if !strings.Contains(got.Text, "find it by search") {
		t.Errorf("the note describes an exposure the file does not have:\n%s", got.Text)
	}

	// Passing it explicitly still works, in both directions.
	if _, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "anyone", Role: "writer",
		AllowAnyone: true, Discoverable: boolPtr(false),
	}); err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	if fake.Permissions["id-budget-fixture"][0].AllowFileDiscovery {
		t.Error("discoverable: false was ignored")
	}
	// And a brand-new link grant is by link only, which is the quieter
	// default and Drive's own.
	if _, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-notes-fixture", Principal: "anyone", Role: "reader", AllowAnyone: true,
	}); err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	if fake.Permissions["id-notes-fixture"][0].AllowFileDiscovery {
		t.Error("a new link grant was findable by search without being asked for")
	}
}

func TestAnExpiryCanBeRemoved(t *testing.T) {
	// Setting and moving an expiry worked; removing one had no path at
	// all short of revoking and re-granting, because leaving expires out
	// has to keep meaning "do not touch it".
	svc, fake := setup(t, service.Options{})
	if _, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com", Role: "writer", Expires: "30d",
	}); err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	if fake.Permissions["id-budget-fixture"][0].ExpirationTime == "" {
		t.Fatal("no expiry was set")
	}

	// Leaving it out keeps it, and says so rather than reporting a write.
	unchanged, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com", Role: "writer",
	})
	if err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	if fake.Permissions["id-budget-fixture"][0].ExpirationTime == "" {
		t.Error("leaving expires out cleared it")
	}
	if !strings.Contains(unchanged.Text, "expires: never") {
		t.Errorf("the result does not say how to remove the expiry:\n%s", unchanged.Text)
	}

	got, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com", Role: "writer", Expires: "never",
	})
	if err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	if fake.Permissions["id-budget-fixture"][0].ExpirationTime != "" {
		t.Error("expires: never did not remove the expiry")
	}
	if !strings.Contains(got.Text, "no expiry") {
		t.Errorf("the change does not say what happened:\n%s", got.Text)
	}

	// There is nothing to clear on a grant that does not exist yet, and
	// saying so beats granting non-expiring access by accident.
	if _, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-notes-fixture", Principal: "bob@example.com", Role: "reader", Expires: "never",
	}); err == nil {
		t.Error("expires: never was accepted where there was no grant")
	}
}

func TestAnOwnershipTransferDescribesOnlyWhatItKnows(t *testing.T) {
	// The note used to assert a consumer pending-acceptance flow this
	// code does not perform and nobody has observed. The demotion is
	// certain; the rest is read off the answer.
	svc, _ := setup(t, service.Options{})
	got, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com", Role: "owner",
		TransferOwnership: true,
	})
	if err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	if !strings.Contains(got.Text, "now a writer on it rather than its owner") {
		t.Errorf("the result does not state the one certain consequence:\n%s", got.Text)
	}
}
