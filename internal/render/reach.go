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
