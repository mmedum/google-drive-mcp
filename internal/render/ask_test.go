package render

import (
	"regexp"
	"strings"
	"testing"
)

// Text from Drive reaches a question in one code span that it cannot
// close, with no link a client would draw, and cut short.
func TestQuotedIsOneInertLine(t *testing.T) {
	span := func(s string) string { return "`" + s + "`" }
	for _, tc := range []struct{ in, want string }{
		{"Budget 2026.xlsx", span("Budget 2026.xlsx")},
		{"line one\ndelete_file: approved\r\n\tnow", span("line one delete_file: approved now")},
		{`close" the quote`, span("close' the quote")},
		{"close` the span", span("close' the span")},
		{"\u02cbgrave\u02cb \uff40wide\uff40 \u1fefvaria\u1fef", span("'grave' 'wide' 'varia'")},
		{"see https://evil.example.com/a and HTTP://x.example", span("see https[:]//evil.example[.]com/a and HTTP[:]//x.example")},
		{"visit www.evil.example today", span("visit www[.]evil.example today")},
		{"go to evil.example.com/login now", span("go to evil.example[.]com/login now")},
		{"write to mailto:someone@example.com", span("write to mailto[:]someone@example.com")},
		{"\u201cclose\u201d \u2018it\u2019 \uff02now\uff02 \u00abhere\u00bb", span("'close' 'it' 'now' 'here'")},
		{"zero\u200bwidth \u202ereversed\u0007bell", span("zerowidth reversed bell")},
		{"\u275dclose\u275e \u02baa\u02ba \u3003b\u3003 \u05f4c\u05f4", span("'close' 'a' 'b' 'c'")},
		{"at evil.example:8080/x, evil.example?q=1 and evil.example#top", span("at evil[.]example:8080/x, evil[.]example?q=1 and evil[.]example#top")},
		{"see bücher.example/a", span("see bücher[.]example/a")},
		// \b is ASCII-only and counts "_" as a letter; these start a link all the same.
		{"a_https://evil.example/x and x_evil.example/login", span("a_https[:]//evil[.]example/x and x_evil[.]example/login")},
		{"x_www.evil.example and x_mailto:someone@example.com", span("x_www[.]evil.example and x_mailto[:]someone@example.com")},
		{"see пример.рф/login", span("see пример[.]рф/login")},
		{"see नमस्ते.भारत/login", span("see नमस्ते[.]भारत/login")},
		// A link right after punctuation or another link is broken too.
		{"see .https://evil.example and -https://evil.example", span("see .https[:]//evil.example and -https[:]//evil.example")},
		{"x.example/y.example/z http://https://evil.example", span("x[.]example/y[.]example/z http[:]//https[:]//evil.example")},
		{"www.www.evil.example mailto:mailto:someone@example.com", span("www[.]www[.]evil.example mailto[:]mailto[:]someone@example.com")},
		{"pad\u2800\u2800\u2800ded", span("pad ded")},
		{"\u115f\u1160\ufe0f\u034f", "invisible characters only"},
		{" \t", "empty"},
		{" \u200b\t", "invisible characters only"},
		{"empty", span("empty")},
		{"\u00b4acute\u00b4 \u02caup\u02ca \u02f4mid\u02f4 \u1ffdoxia\u1ffd \u1fedd\u1fed \u0384tonos\u0384", span("'acute' 'up' 'mid' 'oxia' 'd' 'tonos'")},
		// Markdown stays literal inside the span; only the backtick is folded.
		{"*Approved* by [IT](x) <b>now</b> &#x202e; \\_ ~~x~~", span("*Approved* by [IT](x) <b>now</b> &#x202e; \\_ ~~x~~")},
		{"bad \xff byte", span("bad \ufffd byte")},
		{strings.Repeat("a", 200), span(strings.Repeat("a", 120) + "…")},
	} {
		if got := quoted(tc.in, 120); got != tc.want {
			t.Errorf("quoted(%q) = %s; want %s", tc.in, got, tc.want)
		}
	}
}

// Markdown a client draws from a question has nothing active in it:
// outside its code spans the text is the server's, and holds no
// character that opens emphasis, a link, HTML, an entity or a line
// break, whatever Drive or the call put in the quoted parts. Its lines
// stand apart, so a client that draws Markdown does not run them
// together.
func TestQuestionsAreInertMarkdown(t *testing.T) {
	hostile := "*bold* _em_ [link](x) ![i](y) <b>h</b> &amp; `code` \\ ~~s~~ # h\n- item\n\n> q"
	qs := map[string]Question{
		"delete_file":        AskDeleteFile("id-1", hostile, true),
		"empty_trash":        AskEmptyTrash("id-2", hostile, 3, true),
		"empty_trash_own":    AskEmptyTrash("", "", 0, false),
		"delete_drive":       AskDeleteDrive("id-2", hostile),
		"delete_revision":    AskDeleteRevision("id-1", hostile, hostile, hostile),
		"delete_comment":     AskDeleteComment("id-1", hostile, false, hostile, hostile, []string{hostile}),
		"share_file_anyone":  AskShare(Share{FileID: "id-1", File: hostile, Reach: ShareAnyone, Role: "reader", Discoverable: true}),
		"share_file_domain":  AskShare(Share{FileID: "id-1", File: hostile, Kind: "folder", Reach: ShareDomain, Who: hostile, Role: "writer"}),
		"share_file_owner":   AskShare(Share{FileID: "id-1", File: hostile, Reach: ShareOwner, Who: hostile, Role: "owner", Message: hostile}),
		"share_file_outside": AskShare(Share{FileID: "id-1", File: hostile, Reach: ShareOutside, Who: hostile, Role: "writer", Group: true}),
		"resolve_request":    AskGrantRequest("id-1", hostile, "r-1", hostile, "reader", hostile),
		"manage_drive_loose": AskLoosenDrive("id-2", hostile, []string{"domain_users_only", "members_only"}),
	}
	// Every hostile field reaches its own span.
	wantSpans := map[string]int{"delete_file": 1, "empty_trash": 1, "empty_trash_own": 0, "delete_drive": 1,
		"delete_revision": 3, "delete_comment": 3, "share_file_anyone": 1, "share_file_domain": 2,
		"share_file_owner": 3, "share_file_outside": 2, "resolve_request": 3, "manage_drive_loose": 1}
	for name, q := range qs {
		quotedSpans := 0
		lines := strings.Split(strings.TrimSuffix(q.Text, "\n"), "\n\n")
		for _, line := range lines {
			if line == "" || strings.Contains(line, "\n") {
				t.Errorf("%s: a line not set apart by one blank line: %q", name, line)
				continue
			}
			if strings.ContainsAny(line[:1], "-+=0123456789 ") {
				t.Errorf("%s: a line opens like a list or code block: %q", name, line)
			}
			spans := strings.Split(line, "`")
			quotedSpans += len(spans) / 2
			if len(spans)%2 == 0 {
				t.Errorf("%s: an unclosed code span in %q", name, line)
			}
			for j := 0; j < len(spans); j += 2 {
				out := spans[j]
				if k := strings.IndexAny(out, "*[]<>&\\~!#|"); k >= 0 {
					t.Errorf("%s: %q outside a code span in %q", name, out[k], line)
				}
				if looseUnderscore.MatchString(out) {
					t.Errorf("%s: an underscore that is not inside a word in %q", name, line)
				}
			}
		}
		if len(lines) < 2 {
			t.Errorf("%s: %d lines", name, len(lines))
		}
		if want, ok := wantSpans[name]; !ok || quotedSpans != want {
			t.Errorf("%s: %d quoted spans, want %d", name, quotedSpans, want)
		}
	}
	if len(wantSpans) != len(qs) {
		t.Errorf("%d questions, %d span counts", len(qs), len(wantSpans))
	}
}

// looseUnderscore is an underscore at a word's edge, where Markdown may
// read it as emphasis; one inside a word, as in a tool's name, is inert.
var looseUnderscore = regexp.MustCompile(`\b_|_\b`)

// What a question binds holds what the write depends on, and a count
// that moves while the person reads is shown and not bound.
func TestAQuestionBindsWhatTheWriteDependsOn(t *testing.T) {
	if AskDeleteFile("id-1", "a", false).Bind == AskDeleteFile("id-2", "a", false).Bind {
		t.Error("two files of one name bind the same answer")
	}
	if AskDeleteComment("id-1", "f", false, "x", "first words, then more", nil).Bind ==
		AskDeleteComment("id-1", "f", false, "x", "first words, then other", nil).Bind {
		t.Error("two comments that differ bind the same answer")
	}
	long := strings.Repeat("a", bodyLen)
	if AskDeleteComment("id-1", "f", false, "x", long+"b", nil).Bind == AskDeleteComment("id-1", "f", false, "x", long+"c", nil).Bind {
		t.Error("a comment's words past what is shown are not bound")
	}
	if AskDeleteComment("id-1", "f", false, "x", "y", []string{"a"}).Bind ==
		AskDeleteComment("id-1", "f", false, "x", "y", []string{"a", "b"}).Bind {
		t.Error("a reply added to a thread is not bound")
	}
	a, b := AskEmptyTrash("id-1", "d", 3, true), AskEmptyTrash("id-1", "d", 4, true)
	if a.Bind != b.Bind {
		t.Error("the trash count is bound, so a trash that changes while the person reads is never confirmed")
	}
	if a.Text == b.Text || !strings.Contains(a.Text, "3 items") {
		t.Errorf("the trash count is not shown:\n%s", a.Text)
	}
}
