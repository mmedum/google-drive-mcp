package model

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/mmedum/google-drive-mcp/v2/internal/gdrive"
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

// RoleWidens reports whether moving a grant from one role to another
// gives more access.
func RoleWidens(from, to string) bool { return roleRank[to] < roleRank[from] }

// RoleWords describes a role in plain words.
func RoleWords(role string) string {
	switch role {
	case RoleOwner:
		return "owns it"
	case RoleOrganizer:
		return "manages the drive"
	case RoleFileOrganizer:
		return "organizes content"
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
	// NameOnly is a grant a limited-access folder keeps out: Drive's
	// metadata view, which shows the folder without opening it.
	NameOnly bool
}

// Words says what the grant lets its principal do, in plain words.
func (g Grant) Words() string {
	if g.NameOnly {
		return "can see it but not open it"
	}
	return RoleWords(g.Role)
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
	// Editors, Commenters and Viewers count those people by what they may
	// do, and NameOnly the ones a limited-access folder keeps out.
	Editors, Commenters, Viewers, NameOnly int
	// Link is the anyone grant, when there is one.
	Link *Grant
	// Domains are the domain-wide grants.
	Domains []Grant
	// Owner is the owning principal's label, when a permission names one.
	Owner string
	// PendingOwner is set while a consumer-account transfer waits for the
	// new owner to accept.
	PendingOwner string
	// Inherited counts the People whose access comes from a folder above
	// or the shared drive. A link or a domain grant from above is not one
	// of them: the summary says "N of them" of the people.
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

// NewSharing summarizes a permission list. known says the list was read;
// an empty list that was read means "no grants", while one that could
// not be read means "unknown", and the two must not print the same.
func NewSharing(shared bool, perms []*gdrive.Permission, known bool) Sharing {
	if !known {
		return Sharing{Shared: shared, Unknown: true}
	}
	grants := make([]Grant, 0, len(perms))
	for _, p := range perms {
		if p != nil {
			grants = append(grants, GrantOf(p))
		}
	}
	return SharingOf(shared, grants)
}

// GrantOf is one permission as the model shows it.
func GrantOf(p *gdrive.Permission) Grant {
	g := Grant{
		PermissionID: p.ID, Type: p.Type, Role: p.Role, Name: p.DisplayName,
		Discoverable: p.AllowFileDiscovery, Expires: p.ExpirationTime,
		PendingOwner: p.PendingOwner, Deleted: p.Deleted, NameOnly: p.View == gdrive.ViewMetadata,
	}
	if inherited, from := p.Inherited(); inherited {
		g.InheritedFrom = from
		if g.InheritedFrom == "" {
			// The reference says inheritedFrom "is only populated for
			// items in shared drives", so an empty one is a My Drive
			// item inheriting from a folder above it, never a drive.
			g.InheritedFrom = "a folder above it"
		}
	}
	switch p.Type {
	case "anyone":
		g.Who = "anyone"
	case "domain":
		g.Who = p.Domain
	default:
		g.Who = p.EmailAddress
	}
	return g
}

// SharingOf summarizes grants already in the model's terms: a
// permission list read from Drive, or one worked out before a change
// that has not happened yet.
func SharingOf(shared bool, grants []Grant) Sharing {
	s := Sharing{Shared: shared, Grants: slices.Clone(grants)}
	for _, g := range grants {
		switch g.Type {
		case "anyone":
			link := g
			s.Link = &link
		case "domain":
			s.Domains = append(s.Domains, g)
		default:
			if g.Role == RoleOwner {
				s.Owner = g.Label()
			} else {
				s.People++
				if g.Inherited() {
					s.Inherited++
				}
				switch {
				case g.NameOnly:
					s.NameOnly++
				case g.Role == RoleWriter || g.Role == RoleOrganizer || g.Role == RoleFileOrganizer:
					s.Editors++
				case g.Role == RoleCommenter:
					s.Commenters++
				default:
					s.Viewers++
				}
			}
			if g.PendingOwner {
				s.PendingOwner = g.Label()
			}
		}
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

// Roles is what the people a sharing reaches may do, by count, in the
// words a summary and a question both use: "2 can edit", "1 can view".
func (s Sharing) Roles() []string {
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
	if s.NameOnly > 0 {
		who = append(who, fmt.Sprintf("%d can see it but not open it", s.NameOnly))
	}
	return who
}

// Beyond is who a sharing reaches past the people on it: each domain,
// then the link. name writes a domain the way the caller shows it, which
// a question quotes.
func (s Sharing) Beyond(name func(string) string) []string {
	var parts []string
	for _, d := range s.Domains {
		line := "everyone at " + name(d.Who) + " " + d.Words()
		if d.Discoverable {
			line += ", and it turns up in their search"
		} else {
			line += " with the link"
		}
		parts = append(parts, line)
	}
	if s.Link != nil {
		line := "anyone with the link " + s.Link.Words()
		if s.Link.Discoverable {
			line = "anyone on the internet " + s.Link.Words() + " and can find it by search"
		}
		parts = append(parts, line)
	}
	return parts
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
		line := fmt.Sprintf("shared with %s: %s", Plural(s.People, "person", "people"), strings.Join(s.Roles(), ", "))
		// Where the grants come from decides what can be done about
		// them: an inherited one is removed at the drive or the folder,
		// not here. It is said as part of the one count, "4 people, all
		// of them through the shared drive", never as a second count
		// beside it, which reads as more people.
		switch {
		case s.Inherited >= s.People && s.SharedDrive != "":
			line += ", all of them through the shared drive " + s.SharedDrive
		case s.Inherited > 0 && s.SharedDrive != "":
			line += fmt.Sprintf(" (%d of them through the shared drive %s)", s.Inherited, s.SharedDrive)
		case s.Inherited > 0:
			// Without a shared drive's name this is a My Drive item, whose
			// inherited grants come from its folders.
			line += fmt.Sprintf(" (%d of them through a folder above it)", s.Inherited)
		}
		parts = append(parts, line)
	}
	parts = append(parts, s.Beyond(func(domain string) string { return domain })...)
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
		// Naming the drive twice in one line reads as two drives.
		if s.Inherited > 0 {
			parts = append(parts, "and everyone else with access to that drive can see it")
		} else {
			parts = append(parts, "plus everyone with access to the shared drive "+s.SharedDrive)
		}
	}
	if s.PendingOwner != "" {
		parts = append(parts, "ownership transfer to "+s.PendingOwner+" waiting to be accepted")
	}
	return strings.Join(parts, "; ")
}

// Public reports whether anyone with the link can reach the file, which
// is the exposure worth naming before and after a sharing change.
func (s Sharing) Public() bool { return s.Link != nil }

// DirectRole is the role a permission gives on the item itself, apart
// from what the item inherits, and false when every way it reaches the
// item is inherited. A permission with no details counts as direct: the
// reference says inherited "is always populated", so only a hand-made
// answer lacks it. A moved item keeps what is direct and loses what it
// inherited where it was.
func DirectRole(p *gdrive.Permission) (string, bool) {
	if p == nil {
		return "", false
	}
	if len(p.Details) == 0 {
		return p.Role, true
	}
	role, found := "", false
	for _, d := range p.Details {
		if !d.Inherited && (!found || RoleWidens(role, d.Role)) {
			role, found = d.Role, true
		}
	}
	return role, found
}

// Key is who a grant reaches, which is what two lists of grants are
// compared on: the same person, group, domain or link.
func (g Grant) Key() string { return g.Type + ":" + strings.ToLower(g.Who) }

// Gained is what after reaches that before did not: a person, group,
// domain or link with no access before, more access than before, a
// folder opened to one who could only see it, or a link grant that now
// turns up in search. self is the signed-in
// account's address, whose own access is nobody's exposure. An owner who
// appears is counted as someone who can edit, which is what that is to
// the people asking who can reach a file.
func Gained(before, after Sharing, self string) []Grant {
	had := map[string]Grant{}
	for _, g := range MergeGrants(before.Grants...) {
		had[g.Key()] = g
	}
	self = strings.TrimSpace(self)
	var out []Grant
	for _, g := range after.Grants {
		if g.Deleted || self != "" && g.Type == "user" && strings.EqualFold(g.Who, self) {
			continue
		}
		e, ok := had[g.Key()]
		// Opening what one could only see is more access, whatever the
		// two roles say: a limited-access folder's metadata view is
		// always a reader.
		opened := e.NameOnly && !g.NameOnly
		if ok && !RoleWidens(e.Role, g.Role) && (!g.Discoverable || e.Discoverable) && !opened {
			continue
		}
		if g.Role == RoleOwner {
			g.Role = RoleWriter
		}
		out = append(out, g)
	}
	return out
}

// MergeGrants folds the grants to one principal into one, in the order
// each principal first appears: the wider role wins, it turns up in
// search when either does, it only shows the item when both only show
// it, and it comes from above when a later one does.
func MergeGrants(grants ...Grant) []Grant {
	var out []Grant
	at := map[string]int{}
	for _, g := range grants {
		i, ok := at[g.Key()]
		if !ok {
			at[g.Key()] = len(out)
			out = append(out, g)
			continue
		}
		e := &out[i]
		if RoleWidens(e.Role, g.Role) {
			e.Role = g.Role
		}
		e.Discoverable = e.Discoverable || g.Discoverable
		e.NameOnly = e.NameOnly && g.NameOnly
		if g.Inherited() {
			e.InheritedFrom = g.InheritedFrom
		}
	}
	return out
}

// SameReach reports whether two summaries reach the same people, groups,
// domains and links with the same access, wherever it comes from. The
// signed-in account counts like anyone else, as it does in a summary. It
// compares the grants each holds, so it means something only when both
// lists were read.
func SameReach(a, b Sharing) bool {
	return len(Gained(a, b, "")) == 0 && len(Gained(b, a, "")) == 0
}

// Plural renders a count with its noun. It lives here because model owns
// the other humanizing helpers (HumanSize, HumanTime, Ago) that render
// and service already call, and three copies of one English rule is how
// a tree title and a listing title come to disagree.
func Plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Word is Plural's other half: the singular or plural form with no count
// in front of it, for a sentence that has already given the number or
// must not repeat it. Hand-written copies of this rule are how two lines
// about the same thing come to disagree, which is Plural's own argument.
func Word(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
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

// ChecksumState is what came of comparing a downloaded file with the
// checksum Drive publishes for it.
type ChecksumState int

// The four outcomes of a checksum comparison. They are distinct because
// "there was nothing to compare against" and "it matched" must never
// read the same way: one is a guarantee and the other is its absence.
const (
	// ChecksumNotPublished means Drive holds no md5 for this file, which
	// is the case for every Google-native document.
	ChecksumNotPublished ChecksumState = iota
	// ChecksumNotComparable means a checksum exists but is not this
	// content's: an export, or an older revision.
	ChecksumNotComparable
	ChecksumMatch
	ChecksumMismatch
)

// Checksum is the verdict on a downloaded file, as facts rather than as
// a sentence: internal/render decides how to say it.
type Checksum struct {
	State ChecksumState
	// Expected is what Drive published, Actual what the bytes on disk
	// hash to. Both are empty unless the comparison happened.
	Expected string
	Actual   string
	// Why explains a ChecksumNotComparable in the caller's terms.
	Why string
}
