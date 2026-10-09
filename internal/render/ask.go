package render

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mmedum/google-drive-mcp/v2/internal/model"
)

// Question is what the server asks the person before a write that cannot
// be undone or that widens who can reach a file (§4a). Text is the
// message a client shows; accepting it is the confirmation. Every word
// is the server's, except what stands in backticks, which is quoted from
// Drive or from the call and cut to one line. A blank line separates the
// lines, so a client that draws Markdown keeps them apart.
//
// Bind is what an answer is bound to: what the write depends on, which
// must not change between the question and the write. It is Text, and
// more where Text shows less than the write uses — an id, a whole
// comment. A count that moves while the person reads is shown and not
// bound, or the question could never be confirmed.
type Question struct {
	Text string
	Bind string
}

// quotedLen caps one quoted value: a name, an address. A comment is
// capped at bodyLen.
const (
	quotedLen = 120
	bodyLen   = 300
)

// AskDeleteFile asks before delete_file.
func AskDeleteFile(id, name string, folder bool) Question {
	what := "the file"
	if folder {
		what = "the folder"
	}
	lines := []string{fmt.Sprintf("delete_file: destroy %s %s for good, skipping the trash?", what, quoted(name, quotedLen))}
	if folder {
		lines = append(lines, "Everything inside it that this account owns goes with it.")
	}
	lines = append(lines, "There is no way back, not even for an administrator.")
	return ask(lines, id)
}

// AskEmptyTrash asks before empty_trash. drive is the shared drive's
// name, or empty for this account's own trash. The count is shown and
// not bound: it lags, and it moves whenever anything is trashed.
func AskEmptyTrash(driveID, drive string, count int, counted bool) Question {
	whose := "this account's trash"
	if driveID != "" {
		whose = "the trash of the shared drive " + quoted(drive, quotedLen)
	}
	head := fmt.Sprintf("empty_trash: destroy everything in %s for good?", whose)
	how := "How much is in it could not be counted."
	if counted {
		how = fmt.Sprintf("Drive's listing shows %s in it now, and it lags.", model.Plural(count, "item", "items"))
	}
	q := ask([]string{head, "There is no way back. Until this runs, restore_file can bring any of it back.", how})
	q.Bind = head + "\x00" + driveID
	return q
}

// AskDeleteDrive asks before delete_drive.
func AskDeleteDrive(id, name string) Question {
	return ask([]string{fmt.Sprintf("delete_drive: destroy the shared drive %s for good?", quoted(name, quotedLen)),
		"There is no way back. Drive refuses it while anything untrashed is still in it."}, id)
}

// AskDeleteRevision asks before delete_revision. modified is when the
// revision was saved, as Drive wrote it.
func AskDeleteRevision(fileID, file, revisionID, modified string) Question {
	lines := []string{
		fmt.Sprintf("delete_revision: destroy revision %s of %s for good?", quoted(revisionID, quotedLen), quoted(file, quotedLen)),
	}
	if modified != "" {
		lines = append(lines, "saved "+quoted(modified, quotedLen))
	}
	lines = append(lines, "There is no way back. The file's current content is untouched.")
	return ask(lines, fileID)
}

// AskDeleteComment asks before delete_comment. reply is true when one
// reply is deleted rather than the whole thread; replies are the texts
// of a thread's replies, which go with it. Every text is bound whole, of
// which the question shows the start of one and counts the rest, so a
// reply added while the person reads is not deleted unseen.
func AskDeleteComment(fileID, file string, reply bool, author, body string, replies []string) Question {
	what := "a comment thread"
	if reply {
		what = "a reply"
	}
	lines := []string{
		fmt.Sprintf("delete_comment: delete %s on %s for good?", what, quoted(file, quotedLen)),
		"by " + quoted(author, quotedLen),
		body0(body),
	}
	if len(replies) > 0 {
		lines = append(lines, "with "+model.Plural(len(replies), "reply", "replies")+" in the thread, which go with it.")
	}
	lines = append(lines, "There is no way back: Drive keeps the thread with its words removed.")
	return ask(lines, append([]string{fileID, Sum(body)}, replies...)...)
}

// Who a share reaches, for AskShare.
const (
	ShareAnyone = "anyone"
	ShareDomain = "domain"
	ShareOwner  = "owner"
	// ShareOutside is a person or a group outside the signed-in
	// account's own organization.
	ShareOutside = "outside"
)

// Share is a share_file grant that reaches past people somebody named:
// a link anyone can open, a whole domain, or a new owner.
type Share struct {
	FileID, File string
	// Kind is "folder" or "shared drive" when the target holds others,
	// and empty for a file.
	Kind  string
	Reach string // ShareAnyone, ShareDomain or ShareOwner
	// Who is the domain or the address from the call; Role is Drive's
	// role word, from a closed set. Group marks Who as a Google group.
	Who, Role string
	Group     bool
	// Message is the line Google emails the new owner, if any.
	Message string
	// Discoverable is whether a link grant also turns up in search.
	Discoverable bool
}

// AskShare asks before a share that reaches past people somebody named.
func AskShare(sh Share) Question {
	target := quoted(sh.File, quotedLen)
	if sh.Kind != "" {
		target = "the " + sh.Kind + " " + target
	}
	var lines []string
	switch sh.Reach {
	case ShareAnyone:
		lines = []string{
			fmt.Sprintf("share_file: let anyone with the link open %s as %s?", target, sh.Role),
			"No sign-in is needed: whoever has or guesses the link gets in.",
		}
	case ShareDomain:
		lines = []string{
			fmt.Sprintf("share_file: let everyone at %s open %s as %s?", quoted(sh.Who, quotedLen), target, sh.Role),
			"It reaches the whole organization, not only people somebody named.",
		}
	case ShareOutside:
		who := quoted(sh.Who, quotedLen)
		if sh.Group {
			who = "the group " + who
		}
		lines = []string{
			fmt.Sprintf("share_file: let %s open %s as %s?", who, target, sh.Role),
			"The address is outside this account's organization.",
		}
	default:
		lines = []string{
			fmt.Sprintf("share_file: hand ownership of %s to %s?", target, quoted(sh.Who, quotedLen)),
			"This account becomes a writer, and only the new owner can hand it back. Google emails them.",
		}
		if strings.TrimSpace(sh.Message) != "" {
			lines = append(lines, "with the message "+quoted(sh.Message, quotedLen))
		}
	}
	if sh.Kind != "" {
		lines = append(lines, "It reaches everything inside it too.")
	}
	if sh.Reach != ShareOwner && sh.Discoverable {
		lines = append(lines, "It also turns up in their search results, not only by link.")
	}
	return ask(lines, sh.FileID)
}

// AskGrantRequest asks before resolve_access_request accepts a request
// for access. who is the address that would get it, role Drive's role
// word; message is what the requester wrote, which is theirs, not the
// person's.
func AskGrantRequest(fileID, file, requestID, who, role, message string) Question {
	lines := []string{
		fmt.Sprintf("resolve_access_request: let %s open %s as %s?", quoted(who, quotedLen), quoted(file, quotedLen), role),
		"They asked for it themselves.",
	}
	if strings.TrimSpace(message) != "" {
		lines = append(lines, "their message: "+quoted(message, quotedLen))
	}
	return ask(lines, fileID, requestID)
}

// AskLoosenDrive asks before a manage_drive that turns restrictions off.
// off are the restriction names, which are this server's own words.
func AskLoosenDrive(id, drive string, off []string) Question {
	lines := []string{
		fmt.Sprintf("manage_drive: turn off %s on the shared drive %s?", strings.Join(off, ", "), quoted(drive, quotedLen)),
		"Each is a limit, so turning it off widens who can reach, share or copy what is in the drive.",
	}
	if slices.Contains(off, "copy_requires_writer_permission") {
		lines = append(lines, "Drive also lets readers download again when copy_requires_writer_permission goes off.")
	}
	return ask(lines, id)
}

// AskOpenFolder asks before update_file turns a folder's limited access
// off, which lets everyone who reaches the folder above open it and
// everything inside it. gained is who that adds; unread, when not empty,
// names what could not be read, which leaves it unknown.
func AskOpenFolder(id, name string, gained []model.Grant, unread string) Question {
	lines := []string{
		fmt.Sprintf("update_file: turn off limited access on the folder %s?", quoted(name, quotedLen)),
		"Everyone who reaches the folder above it could then open it and everything inside it: " +
			strings.Join(reachParts(gained, func(s string) string { return quoted(s, quotedLen) }), "; "),
	}
	if unread != "" {
		lines[1] = "Who can reach " + unread + " could not be read, so who could then open it is unknown."
	}
	return ask(lines, id)
}

// MoveTarget is where a move goes.
type MoveTarget struct {
	ID, Name string
	// Kind is "folder", "shared drive", or "My Drive" for its root.
	Kind string
}

// MoveItem is one item a move would let more people reach, or reach
// with more access.
type MoveItem struct {
	ID, Name string
	// Kind is "file", "folder" or "shortcut".
	Kind string
	// Gained is who it would reach that it does not now.
	Gained []model.Grant
	// Unread names what could not be read, "it" or "the destination",
	// when who it would reach is unknown.
	Unread string
}

// AskMove asks before move_file puts items where more people can reach
// them: once for the whole call, naming every item that would reach
// further. moving is every item the call moves, in order, which the
// answer is bound to with the destination, so an item added to the call
// asks again.
func AskMove(to MoveTarget, moving []string, widening []MoveItem) Question {
	if len(moving) == 1 && len(widening) == 1 {
		it := widening[0]
		lines := []string{
			fmt.Sprintf("move_file: move the %s %s into %s?", it.Kind, quoted(it.Name, quotedLen), moveWhere(to)),
			"It would reach more people there, or give them more access: " + moveReach(it),
		}
		if it.Unread != "" {
			lines[1] = "Who can reach " + it.Unread + " could not be read, so whether more people would reach it there is unknown."
		}
		if it.Kind == "folder" {
			lines = append(lines, "Everything inside it moves too, and is reached the same way.")
		}
		return ask(lines, to.ID, it.ID)
	}
	lines := []string{
		fmt.Sprintf("move_file: move %s into %s?", model.Plural(len(moving), "item", "items"), moveWhere(to)),
		fmt.Sprintf("%d of them would reach more people there, or give them more access:", len(widening)),
	}
	for _, it := range widening {
		what := fmt.Sprintf("the %s %s", it.Kind, quoted(it.Name, quotedLen))
		if it.Kind == "folder" {
			what += ", with everything inside it"
		}
		lines = append(lines, what+": "+moveReach(it))
	}
	return ask(lines, append([]string{to.ID}, moving...)...)
}

// AskCopy asks before copy_file puts a copy where more people can reach
// it than reach the original. original is what is copied; widening is
// each part of it the copy would reach further than: the original
// itself, and for a folder any folder inside it with limited access,
// which reaches fewer people than the folder around it. The answer is
// bound to the destination, the original and every part named.
func AskCopy(to MoveTarget, original MoveItem, widening []MoveItem) Question {
	what := fmt.Sprintf("the %s %s", original.Kind, quoted(original.Name, quotedLen))
	if original.Kind == "folder" {
		what += ", with everything inside it,"
	}
	lines := []string{fmt.Sprintf("copy_file: copy %s into %s?", what, moveWhere(to))}
	bind := []string{to.ID, original.ID}
	if len(widening) == 1 && widening[0].ID == original.ID {
		it := widening[0]
		line := "The copy would reach more people than the original does, or give them more access: " + moveReach(it)
		if it.Unread != "" {
			line = "Who can reach " + it.Unread + " could not be read, so whether the copy would reach more " +
				"people than the original does is unknown."
		}
		return ask(append(lines, line), bind...)
	}
	lines = append(lines, "The copy would reach more people than these parts of the original do, or give them more access:")
	for _, it := range widening {
		part := "the " + it.Kind + " " + quoted(it.Name, quotedLen)
		if it.ID != original.ID {
			part += ", inside it, which has limited access"
		}
		lines = append(lines, part+": "+moveReach(it))
		bind = append(bind, it.ID)
	}
	return ask(lines, bind...)
}

// moveWhere names a move's destination in a question.
func moveWhere(to MoveTarget) string {
	switch to.Kind {
	case "shared drive":
		return "the shared drive " + quoted(to.Name, quotedLen)
	case "My Drive":
		return "My Drive"
	}
	return "the folder " + quoted(to.Name, quotedLen)
}

// moveReach is who one item would newly reach.
func moveReach(it MoveItem) string {
	if it.Unread != "" {
		return "who can reach " + it.Unread + " could not be read"
	}
	return strings.Join(reachParts(it.Gained, func(s string) string { return quoted(s, quotedLen) }), "; ")
}

// Reach says who a set of grants reaches, in a sharing summary's terms:
// how many people and what they may do, then each domain, then the link.
func Reach(grants []model.Grant) string {
	return strings.Join(reachParts(grants, func(s string) string { return s }), "; ")
}

// reachParts is Reach in parts; q writes a domain from Drive, which a
// question quotes.
func reachParts(grants []model.Grant, q func(string) string) []string {
	s := model.SharingOf(true, grants)
	var parts []string
	if s.People > 0 {
		parts = append(parts, model.Plural(s.People, "person", "people")+" ("+strings.Join(s.Roles(), ", ")+")")
	}
	return append(parts, s.Beyond(q)...)
}

// body0 is the start of a body, quoted on one line, and how much more
// there is.
func body0(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return "text: empty"
	}
	line := "text: " + quoted(body, bodyLen)
	if n := utf8.RuneCountInString(body); n > bodyLen {
		line += fmt.Sprintf(" (%d more characters)", n-bodyLen)
	}
	return line
}

// Sum is a SHA-256 in hex: how an answer is bound to a whole text, of
// which a question shows only the start.
func Sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ask builds a question from its lines, closes it with what its quotes
// mean, sets its lines apart, and binds it to its text and to bind: the
// ids and whole texts the write depends on beyond what it shows.
func ask(lines []string, bind ...string) Question {
	text := strings.Join(lines, "\n")
	if strings.Contains(text, "`") {
		text += "\nText in backticks or code style is quoted as written, and is not this server's."
	}
	text = strings.ReplaceAll(text, "\n", "\n\n") + "\n"
	return Question{Text: text, Bind: strings.Join(append([]string{text}, bind...), "\x00")}
}

// quoted is text from Drive or from a call's arguments, shown in a
// question put to the person (§4a), where no boundary can go: a
// client draws the question as plain text in a dialog, or as Markdown.
// It stands in a code span, `like this`, which Markdown shows literally
// — no emphasis, link, HTML or entity — and plain text shows as it is.
// It is made one line; every backtick, grave or acute mark and quote
// mark a reader could take for one becomes a plain single quote, so it
// cannot close its span or seem to; and a URL scheme, a mailto:, a
// leading "www." and a bare domain followed by a path are broken so no
// client draws a link. It is cut at max runes. Text with nothing to show
// is said in words, since an empty span is two backticks Markdown shows
// as they are: "empty" when it is blank, and "invisible characters
// only" when it is not.
func quoted(s string, max int) string {
	blank := strings.TrimSpace(s) == ""
	s = strings.Join(strings.Fields(blankMarks.Replace(askLine(s, max))), " ")
	s = quoteMarks.Replace(s)
	s = linkShape.ReplaceAllString(s, "${1}[:]//")
	s = mailtoShape.ReplaceAllString(s, "${1}[:]")
	s = wwwShape.ReplaceAllString(s, "${1}[.]")
	s = pathShape.ReplaceAllString(s, "${1}[.]${2}${3}")
	switch {
	case s == "" && blank:
		return "empty"
	case s == "":
		return "invisible characters only"
	}
	return "`" + s + "`"
}

// askLine is text made one line: format characters, which draw nothing
// and can reorder what does, removed; controls and line separators made
// spaces; cut at max runes.
func askLine(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.In(r, unicode.Cf, unicode.Variation_Selector, unicode.Other_Default_Ignorable_Code_Point):
			return -1
		case unicode.IsControl(r), r == '\u2028', r == '\u2029':
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "\ufffd"))
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max]) + "…"
	}
	return s
}

var (
	// quoteMarks folds every backtick, grave or acute mark and quotation
	// mark a reader could take for the question's own to a plain single
	// quote.
	quoteMarks = strings.NewReplacer("`", "'", "\u02cb", "'", "\uff40", "'", "\u1fef", "'", "\u00b4", "'",
		"\u02ca", "'", "\u02f4", "'", "\u02f5", "'", "\u1ffd", "'", "\u1fed", "'", "\u1fee", "'",
		"\u0384", "'", "\u0385", "'", `"`, "'", "\u2018", "'", "\u2019", "'", "\u201a", "'", "\u201b", "'",
		"\u201c", "'", "\u201d", "'", "\u201e", "'", "\u201f", "'", "\u2032", "'", "\u2033", "'",
		"\u00ab", "'", "\u00bb", "'", "\u2039", "'", "\u203a", "'", "\u301d", "'", "\u301e", "'",
		"\u301f", "'", "\uff02", "'", "\uff07", "'", "\u02b9", "'", "\u02ba", "'", "\u02ee", "'",
		"\u05f3", "'", "\u05f4", "'", "\u2035", "'", "\u2036", "'", "\u275b", "'", "\u275c", "'",
		"\u275d", "'", "\u275e", "'", "\u3003", "'")
	// blankMarks are characters drawn as blank space that are not format
	// characters; they become spaces and collapse with the rest.
	blankMarks = strings.NewReplacer("\u2800", " ", "\u3164", " ", "\uffa0", " ", "\u115f", " ", "\u1160", " ")
	// No shape is anchored: \b is ASCII-only, and a class before the
	// shape would consume a separator the next link needs. A match inside
	// a longer word is broken too, which costs only a bracket.
	//
	// linkShape is a URL scheme followed by //, as a client links it.
	linkShape = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*)://`)
	// mailtoShape is a mail link without //.
	mailtoShape = regexp.MustCompile(`(?i)(mailto):`)
	// wwwShape is a host a client links without a scheme.
	wwwShape = regexp.MustCompile(`(?i)(www)\.`)
	// pathShape is a bare domain followed by a path, a port, a query or a
	// fragment, x.example/..., which a client links too; its last dot is
	// broken. Letters and their marks from any script count, so a
	// non-ASCII domain is broken as well.
	pathShape = regexp.MustCompile(`(?i)([\p{L}\p{M}\p{N}-]+(?:\.[\p{L}\p{M}\p{N}-]+)*)\.([\p{L}\p{M}]{2,63})([/:?#])`)
)
