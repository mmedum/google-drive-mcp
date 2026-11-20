package service

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v3/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v3/internal/gdrive"
)

// A personal Google address is outside whoever holds another one: two
// of them share a domain and no organization. The addresses are built
// from the list itself, so no real one is written down.
func TestAPersonalAddressIsAlwaysOutside(t *testing.T) {
	s := &Service{}
	if len(consumerDomains) == 0 {
		t.Fatal("no personal domains listed")
	}
	for domain := range consumerDomains {
		if !s.organizationFor("person@"+domain, false).outside("someone@" + domain) {
			t.Errorf("%s: an address in the account's own personal domain counts as inside", domain)
		}
	}
	if s.organizationFor("person@example.com", true).outside("colleague@example.com") {
		t.Error("an address in the account's own domain counts as outside")
	}
	if !s.organizationFor("", false).outside("colleague@example.com") {
		t.Error("an address counts as inside an account that could not be read")
	}
}

// The three ways the inside is decided: the deployer's domains, an
// account that cannot create shared drives taken as personal at any
// domain, and otherwise the account's own domain.
func TestTheInsideIsTheSettingThenThePersonalRuleThenTheOwnDomain(t *testing.T) {
	for _, tc := range []struct {
		name            string
		domains         []string
		canCreateDrives bool
		inside, outside string
	}{
		{"the setting, which leaves out the account's own domain", []string{"example.org", "example.net"}, true,
			"colleague@example.net", "colleague@example.com"},
		{"a personal account at another provider", nil, false, "", "colleague@example.com"},
		{"a Workspace account", nil, true, "colleague@example.com", "colleague@example.org"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{opts: Options{OrganizationDomains: tc.domains}}
			org := s.organizationFor("person@example.com", tc.canCreateDrives)
			if tc.inside != "" && org.outside(tc.inside) {
				t.Errorf("%s counts as outside", tc.inside)
			}
			if !org.outside(tc.outside) {
				t.Errorf("%s counts as inside", tc.outside)
			}
		})
	}
}

// On a personal Google account every address is outside, so share_file
// refuses even one in the account's own domain, and says why, rather
// than reading as a refusal of a colleague.
func TestAPersonalAccountSharesWithNobodyThroughShareFile(t *testing.T) {
	fake := drivetest.New()
	t.Cleanup(fake.Close)
	drivetest.SmallTree(fake)
	s := New(drivetest.Client(t, fake), Options{})
	domain := aPersonalDomain(t)
	fake.About.User.EmailAddress = "person@" + domain
	_, err := s.ShareFile(t.Context(), ShareFileInput{File: "id-budget-fixture", Principal: "someone@" + domain,
		Role: "reader"})
	want := "[blocked] someone@" + domain + " is outside this account's organization, and share_file grants only " +
		"people and groups inside it. Nothing was changed. share_outside_organization does it, when " +
		"GDRIVE_ENABLE_DESTRUCTIVE=true. share_file may only narrow a grant they already have: a lower role, or for " +
		"a person or a group an expiry or an earlier one. This is taken as a personal Google account, which has no organization, so " +
		"every address is outside it; set GDRIVE_ORGANIZATION_DOMAINS if it belongs to one."
	if err == nil || err.Error() != want {
		t.Errorf("err = %v\nwant %s", err, want)
	}
	if len(fake.Permissions["id-budget-fixture"]) != 0 {
		t.Error("the refusal still wrote a permission")
	}
}

// get_account says a personal account shares with nobody through
// share_file, so the refusal is not the first a person hears of it.
func TestGetAccountSaysAPersonalAccountHasNoOrganization(t *testing.T) {
	fake := drivetest.New()
	t.Cleanup(fake.Close)
	s := New(drivetest.Client(t, fake), Options{})
	fake.About.User.EmailAddress = "person@" + aPersonalDomain(t)
	out, err := s.GetAccount(t.Context())
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	want := "sharing: this is taken as a personal Google account, which has no organization, so every address " +
		"is outside it and share_file grants nobody; set GDRIVE_ORGANIZATION_DOMAINS if it belongs to one"
	if !strings.Contains(out, want) {
		t.Errorf("get_account does not say %q:\n%s", want, out)
	}
}

// aPersonalDomain is one of consumerDomains, the same one on every run.
// It is taken from the list so that no real address is written here.
func aPersonalDomain(t *testing.T) string {
	t.Helper()
	domains := slices.Sorted(maps.Keys(consumerDomains))
	if len(domains) == 0 {
		t.Fatal("no personal domains listed")
	}
	return domains[0]
}

// The case the rule exists for: a personal account at another mail
// provider counted that provider's addresses as inside. It cannot create
// shared drives, so it shares with nobody through share_file, until the
// deployer names the organization's domains.
func TestShareFileOnAnAccountThatCannotCreateDrives(t *testing.T) {
	for _, tc := range []struct {
		name    string
		domains []string
		granted bool
	}{
		{"unset: taken as personal", nil, false},
		{"the setting names the domain", []string{"example.net"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := drivetest.New()
			t.Cleanup(fake.Close)
			drivetest.SmallTree(fake)
			fake.About.User.EmailAddress = "person@example.net"
			fake.About.CanCreateDrives = false
			s := New(drivetest.Client(t, fake), Options{OrganizationDomains: tc.domains})
			_, err := s.ShareFile(t.Context(), ShareFileInput{File: "id-budget-fixture",
				Principal: "colleague@example.net", Role: "reader"})
			if granted := err == nil; granted != tc.granted {
				t.Errorf("share_file to a colleague at the account's own domain: err = %v, want granted %t", err, tc.granted)
			}
			if !tc.granted && (err == nil || !strings.Contains(err.Error(), "GDRIVE_ORGANIZATION_DOMAINS")) {
				t.Errorf("the refusal does not name the setting that fixes it: %v", err)
			}
		})
	}
}

// On an account taken as personal, every refusal that calls a colleague
// an outsider names the setting that fixes it, so the outside tools are
// not the only way forward a model is shown.
func TestEveryPersonalRefusalNamesTheSetting(t *testing.T) {
	setup := func(t *testing.T) (*Service, *drivetest.Server) {
		fake := drivetest.New()
		t.Cleanup(fake.Close)
		drivetest.SmallTree(fake)
		fake.About.User.EmailAddress = "person@example.net"
		fake.About.CanCreateDrives = false
		return New(drivetest.Client(t, fake), Options{}), fake
	}
	t.Run("a move into a folder a colleague can reach", func(t *testing.T) {
		s, fake := setup(t)
		fake.Grant("id-archive-fixture", &gdrive.Permission{Type: "user", Role: "reader",
			EmailAddress: "colleague@example.net"})
		_, err := s.MoveFile(t.Context(), MoveFileInput{File: "id-budget-fixture", To: "id-archive-fixture"})
		if err == nil || !strings.Contains(err.Error(), "GDRIVE_ORGANIZATION_DOMAINS") {
			t.Errorf("err = %v, want a refusal naming GDRIVE_ORGANIZATION_DOMAINS", err)
		}
	})
	t.Run("accepting a colleague's request", func(t *testing.T) {
		s, fake := setup(t)
		fake.AddProposal("id-budget-fixture", "id-request-1", "colleague@example.net", "reader")
		_, err := s.ResolveAccessRequest(t.Context(), ResolveAccessRequestInput{File: "id-budget-fixture",
			Request: "id-request-1", Action: "accept", Role: "reader"})
		if err == nil || !strings.Contains(err.Error(), "GDRIVE_ORGANIZATION_DOMAINS") {
			t.Errorf("err = %v, want a refusal naming GDRIVE_ORGANIZATION_DOMAINS", err)
		}
	})
}
