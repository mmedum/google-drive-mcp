package service_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
	"github.com/mmedum/google-drive-mcp/v3/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v3/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/v3/internal/service"
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

// A group is shared with as a group: who is in it is nowhere in the call,
// and Google decides each member's access when they open the file. So
// the result says who a group grant reaches, dry run included.
func TestAGroupGrantSaysWhoItReaches(t *testing.T) {
	svc, _ := setup(t, service.Options{})
	in := service.ShareFileInput{File: "id-budget-fixture", Principal: "group:team@example.com", Role: "reader",
		DryRun: true}
	dry, err := svc.ShareFile(t.Context(), in)
	if err != nil {
		t.Fatalf("ShareFile dry run: %v", err)
	}
	if want := "no email would be sent; pass notify: true if they should hear about it. The grant would reach " +
		"everyone in the group, including people added later; a member outside the organization too, if the " +
		"organization's sharing policy lets them in."; dry.JSON.Note != want {
		t.Errorf("dry run note = %q\nwant %q", dry.JSON.Note, want)
	}
	in.DryRun = false
	got, err := svc.ShareFile(t.Context(), in)
	if err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	if want := "no email was sent; pass notify: true if they should hear about it. The grant reaches everyone in " +
		"the group, including people added later; a member outside the organization too, if the " +
		"organization's sharing policy lets them in."; got.JSON.Note != want {
		t.Errorf("note = %q\nwant %q", got.JSON.Note, want)
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

// share_file grants named people and groups inside the organization.
// A link for anyone, a domain, someone outside and a new owner are each
// another tool's, behind the destructive flag, and are refused [blocked]
// naming it, before anything is written (§4a).
func TestShareFileRefusesWhatReachesPastTheOrganization(t *testing.T) {
	for _, tc := range []struct{ principal, role, tool string }{
		{"anyone", "reader", "share_by_link"},
		{"domain:example.com", "reader", "share_by_link"},
		{"someone@example.org", "writer", "share_outside_organization"},
		{"group:team@example.net", "reader", "share_outside_organization"},
		{"alice@example.com", "owner", "transfer_ownership"},
	} {
		svc, fake := setup(t, service.Options{Destructive: true})
		_, err := svc.ShareFile(t.Context(), service.ShareFileInput{
			File: "id-budget-fixture", Principal: tc.principal, Role: tc.role,
		})
		if err == nil || !strings.HasPrefix(err.Error(), "[blocked]") ||
			!strings.Contains(err.Error(), tc.tool+" does it, when GDRIVE_ENABLE_DESTRUCTIVE=true") {
			t.Errorf("%s as %s: err = %v, want [blocked] naming %s", tc.principal, tc.role, err, tc.tool)
		}
		if fake.Count("/permissions") != 0 {
			t.Errorf("%s as %s: the refusal reached the permission list", tc.principal, tc.role)
		}
	}
}

// When the account's own address cannot be read, share_file cannot tell
// inside from outside, and refuses rather than guess.
func TestShareFileRefusesWhenItCannotReadTheAccount(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	fake.Fail = drivetest.FailTimes(5, "/about", drivetest.Failure{Status: http.StatusInternalServerError, Message: "no"})
	_, err := svc.ShareFile(t.Context(), service.ShareFileInput{
		File: "id-budget-fixture", Principal: "alice@example.com", Role: "reader",
	})
	if err == nil || !strings.HasPrefix(err.Error(), "[blocked] this account's own address could not be read") {
		t.Fatalf("err = %v, want a refusal saying the account could not be read", err)
	}
	if len(fake.Permissions["id-budget-fixture"]) != 0 {
		t.Error("the refusal still wrote a permission")
	}
}

// The three wide shares exist only behind GDRIVE_ENABLE_DESTRUCTIVE, and
// the service refuses them without it too: the second line, should a
// tool be registered without asking.
func TestTheWideSharesNeedTheDestructiveFlag(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	for name, call := range map[string]func() error{
		"share_by_link": func() error {
			_, err := svc.ShareByLink(t.Context(), service.ShareByLinkInput{File: "id-budget-fixture", FileName: "Budget.xlsx",
				Principal: "anyone", Role: "reader", Confirm: true})
			return err
		},
		"share_outside_organization": func() error {
			_, err := svc.ShareOutside(t.Context(), service.ShareOutsideInput{File: "id-budget-fixture", FileName: "Budget.xlsx",
				Principal: "someone@example.org", Role: "reader", Confirm: true})
			return err
		},
		"transfer_ownership": func() error {
			_, err := svc.TransferOwnership(t.Context(), service.TransferOwnershipInput{File: "id-budget-fixture", FileName: "Budget.xlsx",
				To: "alice@example.com", Confirm: true})
			return err
		},
	} {
		want := "[forbidden] " + name + " opens a file or a shared drive past the people inside this account's " +
			"organization, and this server was not started with GDRIVE_ENABLE_DESTRUCTIVE=true"
		if err := call(); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to start %q", name, err, want)
		}
	}
	if fake.Count("/permissions") != 0 {
		t.Error("a refused wide share reached the permission list")
	}
}

// Each wide share takes only its own kind of grantee, and names the tool
// for the others.
func TestEachWideShareTakesOnlyItsOwnKindOfGrantee(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})
	for _, tc := range []struct {
		name string
		call func() error
		want string
	}{
		{"an inside address to share_outside_organization", func() error {
			_, err := svc.ShareOutside(t.Context(), service.ShareOutsideInput{File: "id-budget-fixture", FileName: "Budget.xlsx",
				Principal: "alice@example.com", Role: "reader", Confirm: true})
			return err
		}, "[invalid] alice@example.com is inside this account's organization; share_file grants it."},
		{"anyone to share_outside_organization", func() error {
			_, err := svc.ShareOutside(t.Context(), service.ShareOutsideInput{File: "id-budget-fixture", FileName: "Budget.xlsx",
				Principal: "anyone", Role: "reader", Confirm: true})
			return err
		}, "[invalid] share_outside_organization grants people and groups by address; share_by_link grants anyone with the link."},
		{"an address to share_by_link", func() error {
			_, err := svc.ShareByLink(t.Context(), service.ShareByLinkInput{File: "id-budget-fixture", FileName: "Budget.xlsx",
				Principal: "someone@example.org", Role: "reader", Confirm: true})
			return err
		}, "[invalid] share_by_link grants anyone with the link or a domain:, not an address"},
		{"a group as the new owner", func() error {
			_, err := svc.TransferOwnership(t.Context(), service.TransferOwnershipInput{File: "id-budget-fixture", FileName: "Budget.xlsx",
				To: "group:team@example.com", Confirm: true})
			return err
		}, "[invalid] Drive does not make a group the owner of a file"},
	} {
		if err := tc.call(); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: err = %v\nwant it to start %q", tc.name, err, tc.want)
		}
	}
	if fake.Count(http.MethodPost) != 0 {
		t.Error("a refused share wrote a permission")
	}
}

// A wide share needs confirm: true on the call, after its dry run.
func TestAWideShareNeedsConfirm(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})
	in := service.ShareByLinkInput{File: "id-budget-fixture", FileName: "Budget.xlsx", Principal: "anyone", Role: "reader"}
	_, err := svc.ShareByLink(t.Context(), in)
	want := "[forbidden] share_by_link would let anyone with the link open Budget.xlsx, where they can view. " +
		"Pass confirm: true on the same call"
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %v\nwant it to start %q", err, want)
	}
	in.DryRun = true
	if _, err := svc.ShareByLink(t.Context(), in); err != nil {
		t.Fatalf("a dry run needed confirm: %v", err)
	}
	if fake.Count(http.MethodPost) != 0 {
		t.Fatal("an unconfirmed share, or its dry run, wrote a permission")
	}
}

// A wide share carries the item's name, compared exactly with Drive's,
// so the client's prompt names what is opened up. Dry runs included.
func TestAWideShareHoldsItsFileToTheName(t *testing.T) {
	calls := map[string]func(svc *service.Service, name string, dryRun bool) error{
		"share_by_link": func(svc *service.Service, name string, dryRun bool) error {
			_, err := svc.ShareByLink(t.Context(), service.ShareByLinkInput{File: "id-budget-fixture",
				FileName: name, Principal: "anyone", Role: "reader", Confirm: true, DryRun: dryRun})
			return err
		},
		"share_outside_organization": func(svc *service.Service, name string, dryRun bool) error {
			_, err := svc.ShareOutside(t.Context(), service.ShareOutsideInput{File: "id-budget-fixture",
				FileName: name, Principal: "someone@example.org", Role: "reader", Confirm: true, DryRun: dryRun})
			return err
		},
		"transfer_ownership": func(svc *service.Service, name string, dryRun bool) error {
			_, err := svc.TransferOwnership(t.Context(), service.TransferOwnershipInput{File: "id-budget-fixture",
				FileName: name, To: "alice@example.com", Confirm: true, DryRun: dryRun})
			return err
		},
	}
	for tool, call := range calls {
		svc, fake := setup(t, service.Options{Destructive: true})
		for _, tc := range []struct {
			name   string
			dryRun bool
			want   string
		}{
			{"", false, "[invalid] file_name is required: the name exactly as get_file shows it"},
			{"  ", true, "[invalid] file_name is required"},
			{"Budget", false, `[stale] file_name is "Budget", but what this call reaches is named "Budget.xlsx". ` +
				"Nothing was changed. Read it again with get_file"},
			{"budget.xlsx", true, `[stale] file_name is "budget.xlsx", but what this call reaches is named "Budget.xlsx".`},
		} {
			if err := call(svc, tc.name, tc.dryRun); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("%s with file_name %q: err = %v\nwant it to start %q", tool, tc.name, err, tc.want)
			}
		}
		if fake.Count("/permissions") != 0 {
			t.Errorf("%s: a share under the wrong name reached the permission list", tool)
		}
		if err := call(svc, "Budget.xlsx", false); err != nil {
			t.Errorf("%s under the file's own name: %v", tool, err)
		}
	}
}

// A shared drive's root goes by the drive's name too, which is the one
// list_drives and a drive: reference use, whatever its root folder reads.
func TestAShareOnASharedDriveTakesTheDrivesName(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})
	fake.Files["id-drive-marketing"].Name = "Drive"
	for _, name := range []string{"Marketing", "Drive"} {
		if _, err := svc.ShareOutside(t.Context(), service.ShareOutsideInput{File: "drive:Marketing",
			FileName: name, Principal: "someone@example.org", Role: "reader", DryRun: true}); err != nil {
			t.Errorf("file_name %q: %v", name, err)
		}
	}
	_, err := svc.ShareOutside(t.Context(), service.ShareOutsideInput{File: "drive:Marketing",
		FileName: "Sales", Principal: "someone@example.org", Role: "reader", DryRun: true})
	if want := `[stale] file_name is "Sales", but what this call reaches is named "Drive".`; err == nil ||
		!strings.HasPrefix(err.Error(), want) {
		t.Errorf("err = %v\nwant it to start %q", err, want)
	}
}

func TestShareByLinkOpensAFileToAnyoneWithTheLink(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})

	got, err := svc.ShareByLink(t.Context(), service.ShareByLinkInput{
		File: "id-budget-fixture", FileName: "Budget.xlsx", Principal: "anyone", Role: "reader", Confirm: true,
	})
	if err != nil {
		t.Fatalf("ShareByLink: %v", err)
	}
	if !strings.Contains(got.Text, "anyone with the link") {
		t.Errorf("the result does not name the exposure it created:\n%s", got.Text)
	}
	// Drive refuses sendNotificationEmail for anything but a user or a
	// group, so it must not be on the request at all.
	if sent := lastQuery(t, fake, http.MethodPost, "/permissions", "sendNotificationEmail"); sent != "" {
		t.Errorf("sendNotificationEmail = %q on an anyone grant, want it absent", sent)
	}
	if _, err := svc.ShareByLink(t.Context(), service.ShareByLinkInput{
		File: "id-notes-fixture", FileName: "Meeting notes", Principal: "domain:example.com", Role: "reader", Confirm: true,
	}); err != nil {
		t.Fatalf("ShareByLink to a domain: %v", err)
	}
	if p := fake.Permissions["id-notes-fixture"]; len(p) != 1 || p[0].Type != "domain" || p[0].Domain != "example.com" {
		t.Errorf("permissions = %+v, want one domain grant", p)
	}
}

func TestShareOutsideGrantsAnAddressOutsideTheOrganization(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})
	got, err := svc.ShareOutside(t.Context(), service.ShareOutsideInput{
		File: "id-budget-fixture", FileName: "Budget.xlsx", Principal: "someone@example.org", Role: "commenter", Confirm: true,
	})
	if err != nil {
		t.Fatalf("ShareOutside: %v", err)
	}
	p := fake.Permissions["id-budget-fixture"]
	if len(p) != 1 || p[0].EmailAddress != "someone@example.org" || p[0].Role != "commenter" {
		t.Fatalf("permissions = %+v", p)
	}
	if sent := lastQuery(t, fake, http.MethodPost, "/permissions", "sendNotificationEmail"); sent != "false" {
		t.Errorf("sendNotificationEmail = %q, want false by default", sent)
	}
	if got.JSON.SharingAfter != "shared with 1 person: 1 can comment" {
		t.Errorf("sharing after = %q", got.JSON.SharingAfter)
	}
}

func TestTransferOwnershipMailsAndSaysSo(t *testing.T) {
	svc, fake := setup(t, service.Options{Destructive: true})
	got, err := svc.TransferOwnership(t.Context(), service.TransferOwnershipInput{
		File: "id-budget-fixture", FileName: "Budget.xlsx", To: "alice@example.com", Confirm: true,
	})
	if err != nil {
		t.Fatalf("TransferOwnership: %v", err)
	}
	// Google always mails a transfer and refuses to have it turned off,
	// so the request must ask for it and the result must say so.
	if sent := lastQuery(t, fake, http.MethodPost, "/permissions", "sendNotificationEmail"); sent != "true" {
		t.Errorf("sendNotificationEmail = %q on a transfer, want true", sent)
	}
	if transfer := lastQuery(t, fake, http.MethodPost, "/permissions", "transferOwnership"); transfer != "true" {
		t.Errorf("transferOwnership = %q, want true", transfer)
	}
	if !strings.Contains(got.Text, "always mails") {
		t.Errorf("the result does not say mail is unavoidable:\n%s", got.Text)
	}
	if _, err := svc.TransferOwnership(t.Context(), service.TransferOwnershipInput{File: "id-budget-fixture", FileName: "Budget.xlsx",
		Confirm: true}); err == nil || !strings.HasPrefix(err.Error(), "[invalid] to is required") {
		t.Errorf("a transfer to nobody: err = %v", err)
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

	wide, _ := setup(t, service.Options{Destructive: true})
	_, err = wide.TransferOwnership(t.Context(), service.TransferOwnershipInput{
		File: "id-q3-plan-fixture", FileName: "Q3 plan", To: "alice@example.com", Confirm: true,
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
	svc, fake := setup(t, service.Options{Destructive: true})
	fake.Fail = drivetest.FailTimes(1, "/permissions", drivetest.Failure{
		Status: http.StatusForbidden, Reason: "domainPolicy",
		Message: "The domain administrators have disabled Drive apps.",
	})

	_, err := svc.ShareOutside(t.Context(), service.ShareOutsideInput{
		File: "id-budget-fixture", FileName: "Budget.xlsx", Principal: "outsider@example.net", Role: "reader", Confirm: true,
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
		{"a group grant expiring in the past", "group:team@example.com", "2020-01-01", "not in the future"},
		{"the past is refused", "alice@example.com", "2020-01-01", "not in the future"},
		{"more than a year is refused", "alice@example.com", "400d", "more than a year"},
		{"nonsense is refused", "alice@example.com", "next tuesday", "neither a date"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.ShareFile(t.Context(), service.ShareFileInput{
				File: "id-budget-fixture", Principal: tc.principal, Role: "reader",
				Expires: tc.expires,
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

// Drive lists a My Drive owner's grant with two details: one made on
// the item, and one from the folder above that the owner also owns. It
// is the owner's own grant, not one inherited from that folder.
func TestTheOwnersGrantIsNotCalledInherited(t *testing.T) {
	svc, fake := setup(t, service.Options{})
	for _, id := range []string{"id-2026-fixture", "id-budget-fixture"} {
		fake.Grant(id, &gdrive.Permission{Type: "user", Role: "owner", EmailAddress: drivetest.AccountEmail})
	}

	out, err := svc.ListPermissions(t.Context(), service.ListPermissionsInput{File: "id-budget-fixture"})
	if err != nil {
		t.Fatalf("ListPermissions: %v", err)
	}
	if strings.Contains(out, "inherited") {
		t.Errorf("the owner's grant reads as inherited:\n%s", out)
	}
	_, err = svc.UnshareFile(t.Context(), service.UnshareFileInput{File: "id-budget-fixture", Principal: drivetest.AccountEmail})
	if err == nil || !strings.Contains(err.Error(), "an owner's access is not revoked but transferred") {
		t.Errorf("err = %v, want the owner's refusal rather than an inherited one", err)
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
	// The before-and-after lines sit under NOTHING WAS CHANGED, so they
	// are headed as what would change.
	if !strings.Contains(got.Text, "\nwould change:\n  access for alice@example.com: no access → can edit\n") ||
		strings.Contains(got.Text, "\nchanged:") {
		t.Errorf("the dry run heads its changes as done:\n%s", got.Text)
	}
}

// A dry run's note says what the call would do. Under "NOTHING WAS
// CHANGED", a note saying the link is open, mail went out or the link
// is dead is false.
func TestADryRunsNoteSaysWhatWouldHappen(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*service.Service) (*service.Result, error)
		want string
	}{
		{"a link", func(svc *service.Service) (*service.Result, error) {
			return svc.ShareByLink(t.Context(), service.ShareByLinkInput{File: "id-budget-fixture", FileName: "Budget.xlsx", Principal: "anyone",
				Role: "reader", DryRun: true})
		}, "anyone with the link would reach it without signing in."},
		{"a notified grant", func(svc *service.Service) (*service.Result, error) {
			return svc.ShareFile(t.Context(), service.ShareFileInput{File: "id-budget-fixture",
				Principal: "alice@example.com", Role: "reader", Notify: true, DryRun: true})
		}, "Google would send alice@example.com a notification email."},
		{"a quiet grant", func(svc *service.Service) (*service.Result, error) {
			return svc.ShareFile(t.Context(), service.ShareFileInput{File: "id-budget-fixture",
				Principal: "alice@example.com", Role: "reader", DryRun: true})
		}, "no email would be sent; pass notify: true if they should hear about it."},
		{"a transfer", func(svc *service.Service) (*service.Result, error) {
			return svc.TransferOwnership(t.Context(), service.TransferOwnershipInput{File: "id-budget-fixture", FileName: "Budget.xlsx",
				To: "alice@example.com", DryRun: true})
		}, "this account would become a writer on it rather than its owner, and could not take that back: " +
			"only the new owner can hand it on. Google always mails an ownership transfer; that cannot be turned off."},
	} {
		svc, _ := setup(t, service.Options{Destructive: true})
		got, err := tc.call(svc)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got.JSON.Note != tc.want {
			t.Errorf("%s: note = %q\nwant %q", tc.name, got.JSON.Note, tc.want)
		}
	}

	svc, fake := setup(t, service.Options{})
	fake.Grant("id-budget-fixture", &gdrive.Permission{Type: "anyone", Role: "reader"})
	got, err := svc.UnshareFile(t.Context(), service.UnshareFileInput{File: "id-budget-fixture", RemoveLink: true, DryRun: true})
	if err != nil {
		t.Fatalf("UnshareFile: %v", err)
	}
	want := "the link that grant made would stop working; anyone holding it would see a request-access page"
	if got.JSON.Note != want {
		t.Errorf("unshare: note = %q\nwant %q", got.JSON.Note, want)
	}
}

func TestSharingOffRemovesTheWritesAndKeepsTheRead(t *testing.T) {
	svc, fake := setup(t, service.Options{Sharing: config.SharingOff, Destructive: true})
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
		func() error {
			_, err := svc.ShareByLink(t.Context(), service.ShareByLinkInput{
				File: "id-budget-fixture", FileName: "Budget.xlsx", Principal: "anyone", Role: "writer", Confirm: true})
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
	svc, fake := setup(t, service.Options{Destructive: true})
	fake.Grant("id-budget-fixture", &gdrive.Permission{
		Type: "anyone", Role: "reader", AllowFileDiscovery: true,
	})

	got, err := svc.ShareByLink(t.Context(), service.ShareByLinkInput{
		File: "id-budget-fixture", FileName: "Budget.xlsx", Principal: "anyone", Role: "writer", Confirm: true,
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
	if _, err := svc.ShareByLink(t.Context(), service.ShareByLinkInput{
		File: "id-budget-fixture", FileName: "Budget.xlsx", Principal: "anyone", Role: "writer",
		Discoverable: boolPtr(false), Confirm: true,
	}); err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	if fake.Permissions["id-budget-fixture"][0].AllowFileDiscovery {
		t.Error("discoverable: false was ignored")
	}
	// And a brand-new link grant is by link only, which is the quieter
	// default and Drive's own.
	if _, err := svc.ShareByLink(t.Context(), service.ShareByLinkInput{
		File: "id-notes-fixture", FileName: "Meeting notes", Principal: "anyone", Role: "reader", Confirm: true,
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
	svc, _ := setup(t, service.Options{Destructive: true})
	got, err := svc.TransferOwnership(t.Context(), service.TransferOwnershipInput{
		File: "id-budget-fixture", FileName: "Budget.xlsx", To: "alice@example.com", Confirm: true,
	})
	if err != nil {
		t.Fatalf("TransferOwnership: %v", err)
	}
	if !strings.Contains(got.Text, "now a writer on it rather than its owner") {
		t.Errorf("the result does not state the one certain consequence:\n%s", got.Text)
	}
}

func TestTheCardAfterASharingChangeShowsTheNewExposure(t *testing.T) {
	// The card's "sharing:" line is the one a person reads to see what
	// they just exposed. It was rendered from the file read BEFORE the
	// write, so share_file on a private file reported "sharing: private
	// to you" in the same result whose change line said the file was now
	// reachable by anyone with the link. Live runs showed it twice.
	svc, fake := setup(t, service.Options{Destructive: true})

	got, err := svc.ShareByLink(t.Context(), service.ShareByLinkInput{
		File: "id-budget-fixture", FileName: "Budget.xlsx", Principal: "anyone", Role: "reader", Confirm: true,
	})
	if err != nil {
		t.Fatalf("ShareByLink: %v", err)
	}
	if strings.Contains(got.Text, "sharing: private to you") {
		t.Errorf("the card still says the file is private after making it public:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "sharing: anyone with the link can view") {
		t.Errorf("the card does not show the exposure the call created:\n%s", got.Text)
	}
	if got.JSON.File.Sharing != got.JSON.SharingAfter {
		t.Errorf("the card and the after-summary disagree: %q vs %q",
			got.JSON.File.Sharing, got.JSON.SharingAfter)
	}

	// And removing the last grant leaves it private, not "shared, but no
	// grants are visible to this account" — which is what the stale
	// `shared` flag produced.
	back, err := svc.UnshareFile(t.Context(), service.UnshareFileInput{
		File: "id-budget-fixture", RemoveLink: true,
	})
	if err != nil {
		t.Fatalf("UnshareFile: %v", err)
	}
	if strings.Contains(back.JSON.SharingAfter, "no grants are visible") {
		t.Errorf("removing the last grant reported unknown exposure: %q", back.JSON.SharingAfter)
	}
	if !strings.Contains(back.Text, "sharing: private to you") {
		t.Errorf("the card does not show the file is private again:\n%s", back.Text)
	}
	if len(fake.Permissions["id-budget-fixture"]) != 0 {
		t.Errorf("the grant is still there: %+v", fake.Permissions["id-budget-fixture"])
	}
}
