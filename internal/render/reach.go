package render

import (
	"strings"

	"github.com/mmedum/google-drive-mcp/v3/internal/model"
)

// Reach says who a set of grants reaches, in a sharing summary's terms:
// how many people and what they may do, then each domain, then the link.
func Reach(grants []model.Grant) string {
	s := model.SharingOf(true, grants)
	var parts []string
	if s.People > 0 {
		parts = append(parts, model.Plural(s.People, "person", "people")+" ("+strings.Join(s.Roles(), ", ")+")")
	}
	return strings.Join(append(parts, s.Beyond()...), "; ")
}

// WideReach names each grant a refusal of reaching past the organization
// names, and what it may do. A person or a group is named by address,
// which is what share_outside_organization takes, and never by a display
// name Drive attached.
func WideReach(grants []model.Grant) string {
	words := make([]string, 0, len(grants))
	for _, g := range grants {
		if g.Who != "" {
			g.Name = ""
		}
		words = append(words, g.Label()+" "+g.Words())
	}
	return strings.Join(words, "; ")
}
