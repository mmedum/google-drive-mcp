package service

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v3/internal/gapi/drivetest"
)

// A personal Google address is outside whoever holds another one: two
// of them share a domain and no organization. The addresses are built
// from the list itself, so no real one is written down.
func TestAPersonalAddressIsAlwaysOutside(t *testing.T) {
	fake := drivetest.New()
	t.Cleanup(fake.Close)
	s := New(drivetest.Client(t, fake), Options{})
	if len(consumerDomains) == 0 {
		t.Fatal("no personal domains listed")
	}
	for domain := range consumerDomains {
		fake.About.User.EmailAddress = "person@" + domain
		if !s.outside(t.Context(), "someone@"+domain) {
			t.Errorf("%s: an address in the account's own personal domain counts as inside", domain)
		}
	}
	fake.About.User.EmailAddress = drivetest.AccountEmail
	if s.outside(t.Context(), "colleague@example.com") {
		t.Error("an address in the account's own domain counts as outside")
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
		"GDRIVE_ENABLE_DESTRUCTIVE=true. This is a personal Google account, which has no organization, so " +
		"every address is outside it."
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
	want := "sharing: this is a personal Google account, which has no organization, so every address is outside " +
		"it and share_file grants nobody"
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
