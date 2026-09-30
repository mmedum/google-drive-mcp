package service

import (
	"testing"

	"github.com/mmedum/google-drive-mcp/v2/internal/gapi/drivetest"
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
