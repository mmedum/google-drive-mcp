package service

import (
	"context"

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
	"slices"
	"strings"
)

// organization is who counts as inside the signed-in account's
// organization. It is the one rule the share tools, the access requests,
// moves and copies read to decide what reaches past it, so none of them
// can draw the line somewhere else.
type organization struct {
	// domains are the deployer's GDRIVE_ORGANIZATION_DOMAINS; when set,
	// they are the whole of the inside.
	domains map[string]bool
	// domain is the account's email domain, empty when the account could
	// not be read.
	domain string
	// personal is an account taken as a personal Google account, which
	// has no organization.
	personal bool
}

// consumerDomains are config.PersonalDomains, as a set.
var consumerDomains = func() map[string]bool {
	out := map[string]bool{}
	for _, d := range config.PersonalDomains {
		out[d] = true
	}
	return out
}()

// organization is the signed-in account's, read through accountInfo,
// which asks Drive once per session.
func (s *Service) organization(ctx context.Context) organization {
	if len(s.opts.OrganizationDomains) > 0 {
		return s.organizationFor("", false)
	}
	return s.organizationFor(s.accountInfo(ctx))
}

// organizationFor is the organization of an account with this address
// and canCreateDrives. The deployer's domains win. Without them, an
// account at a personal Google domain is personal, and so is one that
// cannot create shared drives: about.get has no field naming an
// organization, and a personal account always answers false there, at
// any domain. A Workspace account whose administrator turned shared drives
// off is then refused one share too many, and the refusal names the
// setting that fixes it.
// The live step "the signed-in account is read" reads canCreateDrives.
// https://developers.google.com/workspace/drive/api/reference/rest/v3/about
func (s *Service) organizationFor(account string, canCreateDrives bool) organization {
	if len(s.opts.OrganizationDomains) > 0 {
		set := map[string]bool{}
		for _, d := range s.opts.OrganizationDomains {
			set[strings.ToLower(strings.TrimSpace(d))] = true
		}
		return organization{domains: set}
	}
	d := domainOf(account)
	return organization{domain: d, personal: consumerDomains[d] || (d != "" && !canCreateDrives)}
}

// known reports whether the inside could be told: the deployer named it,
// or the account's own address could be read.
func (o organization) known() bool { return o.domains != nil || o.domain != "" }

// outside reports whether address is outside the organization. Without
// the deployer's domains, the inside is the account's own email domain: a
// secondary domain of the same organization counts as outside, and so does
// everything when the account cannot be read or is personal. Each refuses
// a share too many in the ordinary tools rather than one too few.
func (o organization) outside(address string) bool {
	theirs := domainOf(address)
	if o.domains != nil {
		// A personal domain is outside even when listed: Build refuses one,
		// and this holds for a caller that did not go through Build.
		return theirs == "" || consumerDomains[theirs] || !o.domains[theirs]
	}
	return theirs == "" || consumerDomains[theirs] || o.personal || !o.known() || o.domain != theirs
}

// inside names the domains counted inside, for a person to read.
func (o organization) inside() []string {
	if o.domains == nil {
		if o.domain == "" || o.personal {
			return nil
		}
		return []string{o.domain}
	}
	out := make([]string, 0, len(o.domains))
	for d := range o.domains {
		out = append(out, d)
	}
	slices.Sort(out)
	return out
}

// domainOf is an address's domain, in lower case, or empty when it has
// none.
func domainOf(address string) string {
	_, domain, _ := strings.Cut(strings.ToLower(strings.TrimSpace(address)), "@")
	return domain
}
