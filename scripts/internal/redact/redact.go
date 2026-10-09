package redact

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mmedum/google-drive-mcp/v2/internal/model"
)

// A Redactor replaces account-specific values with stable placeholders,
// so that a transcript can be shared. The same input always gets the
// same placeholder, so the transcript still reads as a story: ID_1 in
// one result is ID_1 in the next.
//
// What it hides: file and shared-drive ids, Drive and Docs links, email
// addresses, comment and reply ids, and people's display names where
// this server prints them. With KeepNamesUnder, it also hides the names
// of files, folders and shared drives that are not the run's own, where
// this server lists them.
//
// What it CANNOT hide: a file or folder name anywhere else. Nothing
// distinguishes "Q3 revenue plan" from prose, and guessing would either
// leave names in or redact the output into uselessness. Summary says so
// every run, because a transcript that is believed to be clean and is
// not is worse than one nobody trusts.
type Redactor struct {
	// Off returns text unchanged. Only for a terminal nobody else sees.
	Off bool

	seen   map[string]string
	counts map[string]int
	// you is the signed-in account's placeholder, once one is known.
	you string
	// learned are the comment and reply ids found at a position, which
	// are then hidden wherever else they turn up.
	learned []string
	// keep, when set, is the prefix of the run's own folder and drive
	// names; the names of everything else are hidden where this server
	// lists them. See KeepNamesUnder.
	keep string
}

// pattern is one class of thing to hide, with the name its placeholders
// carry. Order matters: a link is replaced before the id inside it.
type pattern struct {
	name string
	re   *regexp.Regexp
}

var patterns = []pattern{
	// A link ends before the punctuation that ends a sentence: a period
	// after one belongs to the prose, and taking it ran two sentences
	// into one.
	{"LINK", regexp.MustCompile(`https://(?:drive|docs)\.google\.com/[^\s"'<>)\]]*[^\s"'<>)\].,;:!?]`)},
	{"EMAIL", regexp.MustCompile(`[A-Za-z0-9._%+\-…]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)},
	// A Drive id is base64url, at least 19 characters, and may carry
	// base64 padding. Requiring a capital and a digit keeps ordinary
	// words — google-drive-mcp, modified_before — readable, which
	// matters because an unreadable transcript is one nobody checks
	// before pasting it.
	//
	// A permission id for a person is the exception: it is twenty digits
	// with no letter at all, so that rule let one through in a live run.
	// It identifies a Google account, so it is an id in every sense that
	// matters here.
	{"ID", regexp.MustCompile(`[A-Za-z0-9_\-]{19,}={0,2}`)},
}

// personName is the shape of a display name: capitalized words of two
// letters or more, with the usual lowercase particles allowed between
// them. Requiring the capital keeps the match off the words around it —
// without it, "13:17Z by Kim" was swallowed whole, timestamp and all.
//
// A name has no shape of its own that tells it from a file's, which is
// why it is never matched on its own. What identifies it is the POSITION
// this server printed it in, and those positions are a closed set
// because internal/render wrote every one of them.
//
// The capital is not enough on its own, which a live run found the hard
// way: a Workspace account whose display name was never set shows the
// address's local part instead, and "first.middle.last" has no
// capital anywhere. It survived every position — beside "(you)", after
// "by", after "owner:" — because the shape, not the position, refused
// it. The fixtures could not have caught it either: every invented name
// in them was capitalized, so the test agreed with the bug.
//
// So a dotted lowercase token is a name shape too. It is safe only
// BECAUSE the positions are anchored: matched loose it would swallow
// "modified_before", and the positions are what keep it from being asked
// anywhere a field name could stand.
const personName = `(?:` + capitalizedName + `|` + dottedName + `)`

// capitalizedName is the ordinary shape: capitalized words of two
// letters or more, with the usual lowercase particles between them.
// Requiring the capital keeps the match off the words around it —
// without it, "13:17Z by Kim" was swallowed whole, timestamp and all.
const capitalizedName = `(?:\p{Lu}[\p{L}'\-.]+)(?: (?:\p{Lu}[\p{L}'\-.]+|van|von|der|den|de|del|di|du|la|le|bin|al)){0,4}`

// dottedName is what Google shows for an account with no display name
// set: the address's local part, lowercase, joined by dots, hyphens or
// underscores. At least one separator is required, so a single ordinary
// word is never a name.
const dottedName = `(?:[\p{Ll}\d]+(?:[.\-_][\p{Ll}\d]+)+)`

// personPositions are those positions. internal/model prints a person in
// exactly three forms (userWords): "Name (you)", "Name <address>", and
// the bare "Name" — and only the middle one has an address beside it to
// be found by, which is all the earlier version of this file could see.
//
// The bare form is not an edge case. Drive populates no email on a
// COMMENT author, so a comment listing carries somebody's name and
// nothing else; phase 3 added that surface and the redactor did not
// learn about it. Nor did it ever hide the signed-in account's own name,
// which prints beside "(you)" in almost every result there is.
var personPositions = []*regexp.Regexp{
	// A name opening a line with an angle bracket after it, which is how
	// get_account prints the signed-in person. The address position
	// (namesBeforeAddresses) needs the address to have been replaced
	// first, so it protects this line only while there IS an address:
	// Drive returning an account without one, or the field list dropping
	// it, would leak the name with nothing to notice. The bracket alone is
	// enough to anchor on.
	regexp.MustCompile(`(?m)^(` + personName + `) <`),
	// "modified … by Name" ending a field, in a file card, a listing or
	// a revision: "by" follows a time, which ends in ")" or "Z", or
	// "unknown" when there is none, or "shared with you". A column break,
	// a semicolon or the end of the line closes it. The time is the
	// anchor: without it, a file called "copy of a file shared by
	// link.txt" lost "link.txt" to a person placeholder in a live run.
	regexp.MustCompile(`(?m)(?:\)|\dZ|unknown|shared with you:?) by (` + personName + `)(?:  |;|$)`),
	// "trashed: yes, by Name on 2026-…", a shared drive's record of who
	// put an item in the trash.
	regexp.MustCompile(`, by (` + personName + `) on \d{4}-`),
	// A person standing as the whole value of a field whose KEY says it
	// is one. The keys are a closed set because internal/render writes
	// every one of them, and each is here because some renderer prints a
	// person under it: "owner:" in a file card, and the three an
	// approval's reviewers appear under.
	//
	// Phase 4's live run is why this is a list rather than just "owner":
	// the approvals renderer prints "approved by: Name" for a reviewer
	// Drive gives no address for, and the "by Name" position above did
	// not match it — that one wants a space after "by", and this has a
	// colon. A new key belongs here the day the renderer that prints it
	// is written.
	regexp.MustCompile(`(?m)^\s*(?:owner|asked by|approved by|declined by|waiting on|no answer from): (` +
		personName + `)\s*$`),
	// "Name, 2026-03-04 …" — a comment or reply author, which is the
	// form with nothing else beside it at all.
	regexp.MustCompile(`(` + personName + `), \d{4}-\d{2}-\d{2}`),
	// "by `Name`" alone on a line: the author of a comment, in the
	// question put to the person before it is deleted.
	regexp.MustCompile("(?m)^by `(" + personName + ")`$"),
}

// besideAnchors are what a display name stands in front of: an address
// once it is a placeholder, a group's address, and "(you)", which
// follows the signed-in account and nothing else. Each anchors a name of
// any case or shape, which is what makes these the safe positions for
// one: a live run printed a name as lowercase initials.
var (
	addressAnchor = regexp.MustCompile(` <<EMAIL_\d+>>`)
	groupAnchor   = regexp.MustCompile(` \(group <EMAIL_\d+>\)`)
	youAnchor     = regexp.MustCompile(` \(you\)`)
)

// nameWord is one word of a display name beside an address, in any
// case: the shape matters less here than anywhere else, because the
// address is the anchor. A live run printed one as lowercase initials.
var nameWord = regexp.MustCompile(`^[\p{L}\d][\p{L}\d'.\-_]*$`)

// leadIns are the words this server prints in front of a name and an
// address, which end the name rather than belong to it: "by", "access
// for", "Google sent", "this makes".
var leadIns = map[string]bool{
	"a": true, "an": true, "and": true, "as": true, "at": true, "by": true, "for": true, "from": true,
	"in": true, "is": true, "make": true, "makes": true, "now": true, "of": true, "on": true, "or": true,
	"sent": true, "the": true, "to": true, "was": true, "with": true,
}

// commentPositions are where this server prints a comment's or a
// reply's id. Drive's are eleven characters in a live run, so the ID
// pattern, which wants nineteen, never saw them, and a transcript went
// out with twenty of them in it.
var commentPositions = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"COMMENT", regexp.MustCompile(`\bcomment ([A-Za-z0-9_\-]{6,})`)},
	{"REPLY", regexp.MustCompile(`\breply ([A-Za-z0-9_\-]{6,})`)},
	{"COMMENT", regexp.MustCompile(`"comment":"([^"\\]+)"`)},
	{"REPLY", regexp.MustCompile(`"reply":"([^"\\]+)"`)},
	// list_comments: a thread opens a line with its id and its state,
	// and a reply opens a line indented two spaces under it.
	{"COMMENT", regexp.MustCompile(`(?m)^([A-Za-z0-9_\-]{6,})  (?:open|resolved|deleted)\b`)},
	{"REPLY", regexp.MustCompile(`(?m)^  ([A-Za-z0-9_\-]{6,})  `)},
}

var (
	hasUpper = regexp.MustCompile(`[A-Z]`)
	hasDigit = regexp.MustCompile(`[0-9]`)
)

// NewRedactor returns a redactor with an empty memory.
func NewRedactor(off bool) *Redactor {
	return &Redactor{Off: off, seen: map[string]string{}, counts: map[string]int{}}
}

// Do replaces every account-specific value in text.
func (r *Redactor) Do(text string) string {
	if r.Off {
		return text
	}
	for _, p := range patterns {
		text = p.re.ReplaceAllStringFunc(text, func(match string) string {
			if p.name == "ID" && !looksLikeID(match) {
				// A long lower-case word is prose, not an id.
				return match
			}
			return r.placeholder(p.name, match)
		})
	}
	text = r.commentIDs(text)
	// Names last: the address patterns above turn "<a@b>" into a
	// placeholder, and the positions below are defined by it.
	text = r.namesBefore(text, youAnchor, true)
	text = r.namesBefore(text, addressAnchor, false)
	text = r.namesBefore(text, groupAnchor, false)
	for _, re := range personPositions {
		text = re.ReplaceAllStringFunc(text, func(match string) string {
			name := re.FindStringSubmatch(match)[1]
			return strings.Replace(match, name, r.person(name, false), 1)
		})
	}
	if r.keep != "" {
		text = r.foreignNames(text)
	}
	return text
}

func (r *Redactor) placeholder(kind, value string) string {
	if got, ok := r.seen[value]; ok {
		return got
	}
	r.counts[kind]++
	out := "<" + kind + "_" + strconv.Itoa(r.counts[kind]) + ">"
	r.seen[value] = out
	return out
}

// person is the placeholder for a display name. Case does not matter:
// one person is one placeholder however an answer capitalizes them.
// Every name printed with "(you)" is the signed-in account's, so those
// share one placeholder however the answers spell it: a live run found
// approvals giving the account another display name than files.get, and
// the account appeared as two people.
func (r *Redactor) person(name string, you bool) string {
	key := "person:" + strings.ToLower(name)
	if got, ok := r.seen[key]; ok {
		if you && r.you == "" {
			r.you = got
		}
		return got
	}
	if you && r.you != "" {
		r.seen[key] = r.you
		return r.you
	}
	got := r.placeholder("PERSON", key)
	if you {
		r.you = got
	}
	return got
}

// namesBefore hides the display name in front of each anchor, whatever
// its case or shape: the anchor says a name is there, so the name needs
// no shape of its own. A live run printed one as lowercase initials,
// which no name shape here accepted, beside an address that was hidden.
func (r *Redactor) namesBefore(text string, anchor *regexp.Regexp, you bool) string {
	locs := anchor.FindAllStringIndex(text, -1)
	for i := len(locs) - 1; i >= 0; i-- {
		end := locs[i][0]
		if start := nameStart(text, end); start < end {
			text = text[:start] + r.person(text[start:end], you) + text[end:]
		}
	}
	return text
}

// nameStart walks back from end over at most five words that can be a
// name, and says where the name begins; end itself when there is none.
// The words of one name are one space apart, so two spaces, a tab, a
// line break, the start of the text or a word this server prints in
// front of a name ends it.
func nameStart(text string, end int) int {
	start, pos := end, end
	for range 5 {
		i := strings.LastIndexAny(text[:pos], " \t\n")
		word := text[i+1 : pos]
		if !nameWord.MatchString(word) || leadIns[word] {
			break
		}
		start = i + 1
		if i <= 0 || text[i] != ' ' || strings.ContainsRune(" \t\n", rune(text[i-1])) {
			break
		}
		pos = i
	}
	return start
}

// commentIDs hides comment and reply ids at the positions this server
// prints them, and then wherever else an id it has seen turns up.
func (r *Redactor) commentIDs(text string) string {
	for _, p := range commentPositions {
		text = p.re.ReplaceAllStringFunc(text, func(match string) string {
			id := p.re.FindStringSubmatch(match)[1]
			if !looksLikeCommentID(id) {
				// A word, such as the made-up id of a refusal the driver
				// asks for on purpose, stays readable.
				return match
			}
			if _, ok := r.seen[id]; !ok {
				r.learned = append(r.learned, id)
			}
			return strings.Replace(match, id, r.placeholder(p.kind, id), 1)
		})
	}
	for _, id := range r.learned {
		text = replaceWord(text, id, r.seen[id])
	}
	return text
}

// looksLikeCommentID separates a comment id from a word: an id has a
// capital or a digit in it.
func looksLikeCommentID(v string) bool {
	return len(v) >= 6 && (hasUpper.MatchString(v) || hasDigit.MatchString(v))
}

// replaceWord replaces value wherever it stands on its own, not inside a
// longer id.
func replaceWord(text, value, with string) string {
	var b strings.Builder
	for {
		i := strings.Index(text, value)
		if i < 0 {
			b.WriteString(text)
			return b.String()
		}
		after := i + len(value)
		alone := (i == 0 || !idChar(text[i-1])) && (after == len(text) || !idChar(text[after]))
		b.WriteString(text[:i])
		if alone {
			b.WriteString(with)
		} else {
			b.WriteString(value)
		}
		text = text[after:]
	}
}

// idChar reports whether a byte can be part of a Drive id.
func idChar(c byte) bool {
	return c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

// KeepNamesUnder hides the names of files, folders and shared drives
// that are not the run's own, where this server lists them: a listing
// or a search row, a path, a card's head and location, the drives
// get_account names, and a list of shared drives. A name is the run's
// own when it starts with prefix, or sits in a path through a folder or
// drive whose name does.
//
// A live run read the account's own Drive before it made its scratch
// folder, and its transcript carried the names of the organization's
// drives, its customers and its contracts. Ids and addresses were
// hidden; those were not, and they say more.
func (r *Redactor) KeepNamesUnder(prefix string) { r.keep = prefix }

var (
	// listingHead is the head of a folder listing or a tree, which names
	// the folder its rows are in.
	listingHead = regexp.MustCompile(`^(.+?) — (?:\d+ items?\b|tree,)`)
	// locationField is a card's location, or where a move took an item
	// from.
	locationField = regexp.MustCompile(`^(\s*(location|from): )(.+)$`)
	// cardHead is a card's first line: the name, then its kind, with a
	// word for what was done to it in front.
	cardHead = regexp.MustCompile(`^((?:would have )?[a-z][a-z ]*: )?(.+?) — `)
	// drivesField is get_account's list of shared drives.
	drivesField = regexp.MustCompile(`^(drives you can see: )(.+)$`)
	// restricted is a shared drive listing's line about one drive.
	restricted = regexp.MustCompile(`^(.+) is restricted: `)
	// idCell is a row's id column, bracketed in a tree.
	idCell = regexp.MustCompile(`^\[?<ID_\d+>\]?$`)
	// cellGap separates a row's columns.
	cellGap = regexp.MustCompile(`\s{2,}`)
	// treeGlyphs open a row of a tree.
	treeGlyphs = regexp.MustCompile(`^[│├└─\s]*`)
)

// roleCells open a permission row, whose second column is a person or a
// link and never a file's name.
var roleCells = func() map[string]bool {
	out := map[string]bool{"can see it but not open it": true}
	for _, role := range []string{model.RoleOwner, model.RoleOrganizer, model.RoleFileOrganizer,
		model.RoleWriter, model.RoleCommenter, model.RoleReader} {
		out[model.RoleWords(role)] = true
	}
	return out
}()

// foreignNames hides the names in one result that are not the run's
// own. The result's first line and its location line say where a card
// or a listing is; a row with its own location column says where the
// row is.
func (r *Redactor) foreignNames(text string) string {
	lines := strings.Split(text, "\n")
	headForeign, drives := false, false
	if m := listingHead.FindStringSubmatch(lines[0]); m != nil && isLocation(strings.TrimSuffix(m[1], "/")) {
		if !r.kept(m[1]) {
			headForeign = true
			lines[0] = r.foreignPath(m[1]) + lines[0][len(m[1]):]
		}
	}
	drives = strings.HasPrefix(lines[0], "shared drives this account can see")
	cardForeign := false
	for i, line := range lines {
		if m := locationField.FindStringSubmatch(line); m != nil && isLocation(m[3]) && !r.kept(m[3]) {
			cardForeign = cardForeign || m[2] == "location"
			line = m[1] + r.foreignPath(m[3])
		}
		if m := drivesField.FindStringSubmatch(line); m != nil {
			names := strings.Split(m[2], ", ")
			for j, n := range names {
				names[j] = r.name(n)
			}
			line = m[1] + strings.Join(names, ", ")
		}
		if m := restricted.FindStringSubmatch(line); m != nil && drives {
			line = r.name(m[1]) + line[len(m[1]):]
		}
		lines[i] = r.foreignRow(line, headForeign, drives)
	}
	if cardForeign {
		if m := cardHead.FindStringSubmatchIndex(lines[0]); m != nil {
			lines[0] = lines[0][:m[4]] + r.name(lines[0][m[4]:m[5]]) + lines[0][m[5]:]
		}
	}
	return strings.Join(lines, "\n")
}

// foreignRow hides the name in one row of a listing, a search, a tree,
// a changes feed or a list of shared drives, when the row is not the
// run's own.
func (r *Redactor) foreignRow(line string, headForeign, drives bool) string {
	gaps := cellGap.FindAllStringIndex(line, -1)
	// Cells as spans of the line, so a replacement keeps the columns.
	var cells [][2]int
	from := 0
	for _, g := range gaps {
		if g[0] > from {
			cells = append(cells, [2]int{from, g[0]})
		}
		from = g[1]
	}
	if from < len(line) {
		cells = append(cells, [2]int{from, len(line)})
	}
	id := -1
	for k := 1; k < len(cells); k++ {
		if idCell.MatchString(line[cells[k][0]:cells[k][1]]) {
			id = k
			break
		}
	}
	if id < 0 || roleCells[line[cells[0][0]:cells[0][1]]] {
		return line
	}
	nameAt := cells[id-1]
	name := line[nameAt[0]:nameAt[1]]
	if id == 1 {
		// A tree row opens with the branches it hangs from.
		glyphs := treeGlyphs.FindString(name)
		nameAt[0] += len(glyphs)
		name = name[len(glyphs):]
	}
	foreign := headForeign
	locAt := [2]int{-1, -1}
	for k := id + 1; k < len(cells); k++ {
		if cell := line[cells[k][0]:cells[k][1]]; isLocation(cell) {
			locAt, foreign = cells[k], !r.kept(cell)
			break
		}
	}
	if drives {
		foreign = !strings.HasPrefix(name, r.keep)
	}
	if !foreign {
		return line
	}
	out := line
	if locAt[0] >= 0 {
		out = out[:locAt[0]] + r.foreignPath(out[locAt[0]:locAt[1]]) + out[locAt[1]:]
	}
	hidden := r.name(strings.TrimSuffix(name, "/"))
	if trimmed := strings.TrimSuffix(name, "/"); isLocation(trimmed) {
		// A tree's root is labeled with its whole path.
		hidden = r.foreignPath(trimmed)
	}
	if strings.HasSuffix(name, "/") {
		hidden += "/"
	}
	return out[:nameAt[0]] + hidden + out[nameAt[1]:]
}

// isLocation reports whether a column is a location as this server
// prints one: My Drive, a shared drive, Shared with me, or the place for
// an item in no folder this account can see.
func isLocation(s string) bool {
	head, _, _ := strings.Cut(s, "/")
	return head == "My Drive" || head == "Shared with me" || head == "(no folder this account can see)" ||
		strings.HasSuffix(head, " (shared drive)")
}

// kept reports whether a location is the run's own: it passes through a
// folder or a shared drive whose name starts with the prefix.
func (r *Redactor) kept(location string) bool {
	for _, segment := range strings.Split(location, "/") {
		if strings.HasPrefix(strings.TrimSuffix(segment, " (shared drive)"), r.keep) {
			return true
		}
	}
	return false
}

// foreignPath hides every folder and shared drive named in a location,
// keeping the words this server writes itself.
func (r *Redactor) foreignPath(location string) string {
	segments := strings.Split(location, "/")
	for i, s := range segments {
		switch {
		case s == "My Drive", s == "Shared with me", s == "(no folder this account can see)", s == "…", s == "":
		case strings.HasSuffix(s, " (shared drive)"):
			segments[i] = r.name(strings.TrimSuffix(s, " (shared drive)")) + " (shared drive)"
		default:
			segments[i] = r.name(s)
		}
	}
	return strings.Join(segments, "/")
}

// name is the placeholder for a file, folder or shared drive name that
// is not the run's own.
func (r *Redactor) name(v string) string {
	if v == "" || strings.HasPrefix(v, r.keep) {
		return v
	}
	return r.placeholder("NAME", "name:"+v)
}

// Summary lists what was hidden, by kind, so the reader knows the
// transcript was redacted at all.
func (r *Redactor) Summary() string {
	if r.Off {
		return "redaction off: this transcript carries real ids, links and addresses"
	}
	if len(r.seen) == 0 {
		if r.keep != "" {
			return "nothing needed redacting. A file or folder name is hidden only where this server lists it: " +
				"read the transcript before sharing it."
		}
		return "nothing needed redacting. File and folder names are never redacted: read the transcript before sharing it."
	}
	kinds := make([]string, 0, len(r.counts))
	for k := range r.counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, strconv.Itoa(r.counts[k])+" "+strings.ToLower(k))
	}
	names := ".\nFile and folder names are NOT redacted — nothing distinguishes them from prose. "
	if r.keep != "" {
		names = ".\nFile and folder names outside this run's own are hidden where this server lists them: " +
			"rows, paths, card heads and drive lists. A name anywhere else is NOT redacted. "
	}
	return "redacted: " + strings.Join(parts, ", ") + names + "Read the transcript before sharing it."
}

// looksLikeID separates an id from an ordinary long word. A Drive file
// id mixes case and digits; a permission id for a person is all digits.
// Neither shape occurs in prose at this length, and a word that is
// neither is left readable on purpose.
func looksLikeID(v string) bool {
	if allDigits.MatchString(v) {
		return true
	}
	return hasUpper.MatchString(v) && hasDigit.MatchString(v)
}

// allDigits matches the numeric form, which is what a permission id for
// a person looks like.
var allDigits = regexp.MustCompile(`^[0-9]+$`)
