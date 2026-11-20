package service

import (
	"context"
	"strings"
)

// organization is who counts as inside the signed-in account's
// organization. It is the one rule the share tools, the access requests,
// moves and copies read to decide what reaches past it, so none of them
// can draw the line somewhere else.
type organization struct {
	// domain is the account's email domain, empty when the account could
	// not be read.
	domain string
	// personal is a personal Google account, which has no organization.
	personal bool
}

// consumerDomains are the addresses of personal Google accounts. Two of
// them share a domain and no organization, so an address there is
// outside whoever holds the other.
var consumerDomains = map[string]bool{"gmail.com": true, "googlemail.com": true}

// organization is the signed-in account's, read through account, which
// asks Drive once per session.
func (s *Service) organization(ctx context.Context) organization {
	return organizationOf(s.account(ctx))
}

// organizationOf is the organization an account's address belongs to.
func organizationOf(account string) organization {
	d := domainOf(account)
	return organization{domain: d, personal: consumerDomains[d]}
}

// known reports whether the account's own address could be read.
func (o organization) known() bool { return o.domain != "" }

// outside reports whether address is outside the organization, taken as
// the account's email domain. A secondary domain of the same organization
// counts as outside, and so does everything when the account cannot be
// read or is a personal Google account: each refuses a share too many in
// the ordinary tools rather than one too few.
func (o organization) outside(address string) bool {
	theirs := domainOf(address)
	return theirs == "" || consumerDomains[theirs] || o.personal || !o.known() || o.domain != theirs
}

// domainOf is an address's domain, in lower case, or empty when it has
// none.
func domainOf(address string) string {
	_, domain, _ := strings.Cut(strings.ToLower(strings.TrimSpace(address)), "@")
	return domain
}
