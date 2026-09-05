package model

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

// Roles as Drive names them, in order of how much they allow.
const (
	RoleOwner         = "owner"
	RoleOrganizer     = "organizer"
	RoleFileOrganizer = "fileOrganizer"
	RoleWriter        = "writer"
	RoleCommenter     = "commenter"
	RoleReader        = "reader"
)

// roleRank orders roles from most to least access, so a summary lists
// them the way a person weighs them.
var roleRank = map[string]int{
	RoleOwner: 0, RoleOrganizer: 1, RoleFileOrganizer: 2,
	RoleWriter: 3, RoleCommenter: 4, RoleReader: 5,
}

// RoleWords describes a role in plain words.
func RoleWords(role string) string {
	switch role {
	case RoleOwner:
		return "owns it"
	case RoleOrganizer:
		return "manages the drive"
	case RoleFileOrganizer:
		return "organises content"
	case RoleWriter:
		return "can edit"
	case RoleCommenter:
		return "can comment"
	case RoleReader:
		return "can view"
	case "":
		return "has no role"
	}
	return "has role " + role
}

// Grant is one principal's access, as the model shows it.
type Grant struct {
	PermissionID string
	// Type is user, group, domain or anyone.
	Type string
	Role string
	// Who is the address, domain or the word "anyone".
	Who string
	// Name is a display name when Drive supplied one.
	Name string
	// Discoverable is allowFileDiscovery: whether the file turns up in
	// search for the domain or for everyone, rather than only by link.
	Discoverable bool
	Expires      string
	PendingOwner bool
	// InheritedFrom names the shared-drive folder a grant comes from; an
	// inherited grant can only be removed at its source.
	InheritedFrom string
	Deleted       bool
}

// Inherited reports whether the grant comes from an ancestor.
func (g Grant) Inherited() bool { return g.InheritedFrom != "" }

// Label renders the principal the way a person names it.
func (g Grant) Label() string {
	switch g.Type {
	case "anyone":
		return "anyone with the link"
	case "domain":
		return "everyone at " + g.Who
	case "group":
		if g.Name != "" {
			return g.Name + " (group " + g.Who + ")"
		}
		return "group " + g.Who
	default:
		if g.Name != "" && g.Who != "" {
			return g.Name + " <" + g.Who + ">"
		}
		if g.Who != "" {
			return g.Who
		}
		return g.Name
	}
}

// Sharing is who can see a file, computed from its permissions.
type Sharing struct {
	// Shared is Drive's own flag: the file has been shared with someone.
	Shared bool
	Grants []Grant
	// People counts user and group grants other than the owner.
	People int
	// Editors, Commenters and Viewers count those people by what they may do.
	Editors, Commenters, Viewers int
	// Link is the anyone grant, when there is one.
	Link *Grant
	// Domains are the domain-wide grants.
	Domains []Grant
	// Owner is the owning principal's label, when a permission names one.
	Owner string
	// PendingOwner is set while a consumer-account transfer waits for the
	// new owner to accept.
	PendingOwner string
	// Inherited counts grants that come from a shared-drive ancestor.
	Inherited int
	// Unknown is true when permissions were not readable, so a summary
	// must say so rather than claim the file is private.
	Unknown bool
	// SharedDrive names the shared drive the file lives in. An item there
	// with no grants of its own is not private: everyone with access to
	// the drive can see it, and a summary that said "private to you"
	// would be wrong in the direction that matters.
	SharedDrive string
}

// NewSharing summarises a permission list. known says the list was read;
// an empty list that was read means "no grants", while one that could
// not be read means "unknown", and the two must not print the same.
func NewSharing(shared bool, perms []*gdrive.Permission, known bool) Sharing {
	s := Sharing{Shared: shared}
	if !known {
		s.Unknown = true
		return s
	}
	for _, p := range perms {
		if p == nil {
			continue
		}
		inherited, from := p.Inherited()
		g := Grant{
			PermissionID: p.ID, Type: p.Type, Role: p.Role, Name: p.DisplayName,
			Discoverable: p.AllowFileDiscovery, Expires: p.ExpirationTime,
			PendingOwner: p.PendingOwner, Deleted: p.Deleted,
		}
		if inherited {
			g.InheritedFrom = from
			if g.InheritedFrom == "" {
				g.InheritedFrom = "the shared drive"
			}
			s.Inherited++
		}
		switch p.Type {
		case "anyone":
			g.Who = "anyone"
			link := g
			s.Link = &link
		case "domain":
			g.Who = p.Domain
			s.Domains = append(s.Domains, g)
		default:
			g.Who = p.EmailAddress
			if p.Role == RoleOwner {
				s.Owner = g.Label()
			} else {
				s.People++
				switch p.Role {
				case RoleWriter, RoleOrganizer, RoleFileOrganizer:
					s.Editors++
				case RoleCommenter:
					s.Commenters++
				default:
					s.Viewers++
				}
			}
			if p.PendingOwner {
				s.PendingOwner = g.Label()
			}
		}
		s.Grants = append(s.Grants, g)
	}
	sort.SliceStable(s.Grants, func(i, j int) bool {
		ri, rj := roleRank[s.Grants[i].Role], roleRank[s.Grants[j].Role]
		if ri != rj {
			return ri < rj
		}
		return s.Grants[i].Who < s.Grants[j].Who
	})
	return s
}

// Summary is the one-line exposure a file card shows.
func (s Sharing) Summary() string {
	if s.Unknown {
		if s.Shared {
			return "shared, but this account cannot read the permission list"
		}
		return "sharing unknown: this account cannot read the permission list"
	}
	var parts []string
	if s.People > 0 {
		var who []string
		if s.Editors > 0 {
			who = append(who, fmt.Sprintf("%d can edit", s.Editors))
		}
		if s.Commenters > 0 {
			who = append(who, fmt.Sprintf("%d can comment", s.Commenters))
		}
		if s.Viewers > 0 {
			who = append(who, fmt.Sprintf("%d can view", s.Viewers))
		}
		parts = append(parts, fmt.Sprintf("shared with %s: %s", Plural(s.People, "person", "people"), strings.Join(who, ", ")))
	}
	for _, d := range s.Domains {
		line := "everyone at " + d.Who + " " + RoleWords(d.Role)
		if !d.Discoverable {
			line += " with the link"
		} else {
			line += ", and it turns up in their search"
		}
		parts = append(parts, line)
	}
	if s.Link != nil {
		line := "anyone with the link " + RoleWords(s.Link.Role)
		if s.Link.Discoverable {
			line = "anyone on the internet " + RoleWords(s.Link.Role) + " and can find it by search"
		}
		parts = append(parts, line)
	}
	if len(parts) == 0 {
		switch {
		case s.SharedDrive != "":
			return "no grants of its own: everyone with access to the shared drive " + s.SharedDrive + " can see it"
		case s.Shared:
			return "shared, but no grants are visible to this account"
		default:
			return "private to you"
		}
	}
	if s.SharedDrive != "" {
		parts = append(parts, "plus everyone with access to the shared drive "+s.SharedDrive)
	}
	if s.Inherited > 0 {
		parts = append(parts, fmt.Sprintf("%d inherited from the shared drive", s.Inherited))
	}
	if s.PendingOwner != "" {
		parts = append(parts, "ownership transfer to "+s.PendingOwner+" waiting to be accepted")
	}
	return strings.Join(parts, "; ")
}

// Public reports whether anyone with the link can reach the file, which
// is the exposure worth naming before and after a sharing change.
func (s Sharing) Public() bool { return s.Link != nil }

// Plural renders a count with its noun. It lives here because model owns
// the other humanising helpers (HumanSize, HumanTime, Ago) that render
// and service already call, and three copies of one English rule is how
// a tree title and a listing title come to disagree.
func Plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// capabilityWords maps the capabilities a person cares about to the verb
// this server uses for them, in the order a file card lists them.
var capabilityWords = []struct {
	name string
	get  func(*gdrive.Capabilities) bool
}{
	{"edit", func(c *gdrive.Capabilities) bool { return c.CanEdit }},
	{"comment", func(c *gdrive.Capabilities) bool { return c.CanComment }},
	{"share", func(c *gdrive.Capabilities) bool { return c.CanShare }},
	{"copy", func(c *gdrive.Capabilities) bool { return c.CanCopy }},
	{"download", func(c *gdrive.Capabilities) bool { return c.CanDownload }},
	{"rename", func(c *gdrive.Capabilities) bool { return c.CanRename }},
	{"trash", func(c *gdrive.Capabilities) bool { return c.CanTrash }},
	{"restore", func(c *gdrive.Capabilities) bool { return c.CanUntrash }},
	{"delete permanently", func(c *gdrive.Capabilities) bool { return c.CanDelete }},
	{"add items", func(c *gdrive.Capabilities) bool { return c.CanAddChildren }},
	{"list items", func(c *gdrive.Capabilities) bool { return c.CanListChildren }},
	{"read version history", func(c *gdrive.Capabilities) bool { return c.CanReadRevisions }},
	{"move", func(c *gdrive.Capabilities) bool { return c.CanMoveItemWithinDrive }},
}

// Can lists, in plain words, what the signed-in person may do. Drive
// computes these; the sharing guide asks apps to consult them rather
// than infer rights from a role, and every gate in this server does.
func Can(c *gdrive.Capabilities) []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(capabilityWords))
	for _, w := range capabilityWords {
		if w.get(c) {
			out = append(out, w.name)
		}
	}
	return out
}
