package model

import (
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
)

func TestKindNames(t *testing.T) {
	cases := map[string]string{
		gdrive.MimeFolder:   "folder",
		gdrive.MimeDocument: "Google Doc",
		gdrive.MimeSheet:    "Google Sheet",
		gdrive.MimeSlides:   "Google Slides",
		gdrive.MimeForm:     "Google Form",
		gdrive.MimeScript:   "Apps Script",
		"application/pdf":   "PDF",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "Word document",
		"text/plain":            "text file",
		"text/csv":              "CSV file",
		"image/png":             "PNG image",
		"image/jpeg":            "JPEG image",
		"video/x-matroska":      "MATROSKA video",
		"audio/mpeg":            "MPEG audio",
		"text/x-go":             "go text file",
		"image/svg+xml":         "SVG image",
		"application/x-unknown": "application/x-unknown",
		"":                      "file",
	}
	for mime, want := range cases {
		if got := KindName(mime); got != want {
			t.Errorf("KindName(%q) = %q, want %q", mime, got, want)
		}
	}
}

func TestKindFollowsShortcuts(t *testing.T) {
	f := &gdrive.File{MimeType: gdrive.MimeShortcut, ShortcutDetails: &gdrive.ShortcutDetails{TargetMimeType: gdrive.MimeDocument}}
	if got := Kind(f); got != "shortcut to a Google Doc" {
		t.Errorf("Kind = %q", got)
	}
	bare := &gdrive.File{MimeType: gdrive.MimeShortcut}
	if got := Kind(bare); got != "shortcut" {
		t.Errorf("Kind of a shortcut without target details = %q", got)
	}
	if got := Kind(nil); got != "file" {
		t.Errorf("Kind(nil) = %q", got)
	}
}

func TestIsTextLike(t *testing.T) {
	yes := []string{"text/plain", "text/csv", "text/markdown", "application/json", "application/xml",
		"application/x-yaml", "image/svg+xml", "application/vnd.api+json", "text/plain; charset=utf-8", "TEXT/PLAIN"}
	no := []string{"application/pdf", gdrive.MimeDocument, "image/png", "video/mp4", "application/octet-stream"}
	for _, m := range yes {
		if !IsTextLike(m) {
			t.Errorf("IsTextLike(%q) should be true", m)
		}
	}
	for _, m := range no {
		if IsTextLike(m) {
			t.Errorf("IsTextLike(%q) should be false", m)
		}
	}
}

func TestLocationString(t *testing.T) {
	cases := []struct {
		loc  Location
		want string
	}{
		{Location{Drive: "My Drive", Folders: []string{"Projects", "2026"}}, "My Drive/Projects/2026"},
		{Location{Drive: "Marketing", SharedDrive: true, Folders: []string{"Q3"}}, "Marketing (shared drive)/Q3"},
		{Location{}, "My Drive"},
		{Location{Drive: "My Drive", Folders: []string{"2026"}, Above: true}, "My Drive/…/2026"},
		{Location{SharedWithMe: true}, "Shared with me"},
		{Location{Orphaned: true}, "(no folder this account can see)"},
	}
	for _, c := range cases {
		if got := c.loc.String(); got != c.want {
			t.Errorf("Location.String() = %q, want %q", got, c.want)
		}
	}
}

func TestNewFile(t *testing.T) {
	f := &gdrive.File{
		ID: "id-budget-fixture", Name: "Budget.xlsx",
		MimeType:          "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		Description:       "the numbers",
		Size:              "4096",
		MD5Checksum:       "d41d8cd98f00b204e9800998ecf8427e",
		CreatedTime:       "2026-01-02T10:00:00Z",
		ModifiedTime:      "2026-03-04T09:00:00Z",
		Owners:            []*gdrive.User{{DisplayName: "Test Person", EmailAddress: "person@example.com", Me: true}},
		LastModifyingUser: &gdrive.User{DisplayName: "Other Person", EmailAddress: "other@example.com"},
		WebViewLink:       "https://drive.google.com/file/d/id-budget-fixture/view",
		Starred:           true,
		OwnedByMe:         true,
		Capabilities:      &gdrive.Capabilities{CanEdit: true, CanShare: true, CanTrash: true, CanDownload: true},
	}
	m := New(f, Options{Location: Location{Drive: "My Drive", Folders: []string{"Projects"}}, ExportFormats: nil})
	if m.Kind != "Excel spreadsheet" {
		t.Errorf("kind = %q", m.Kind)
	}
	if !m.HasSize || m.Size != 4096 {
		t.Errorf("size = %d (has %t)", m.Size, m.HasSize)
	}
	if m.Owner != "Test Person (you)" {
		t.Errorf("owner = %q", m.Owner)
	}
	if m.ModifiedBy != "Other Person <other@example.com>" {
		t.Errorf("modified by = %q", m.ModifiedBy)
	}
	if m.Location.String() != "My Drive/Projects" {
		t.Errorf("location = %q", m.Location)
	}
	want := []string{"edit", "share", "download", "trash"}
	for _, w := range want {
		if !containsString(m.Can, w) {
			t.Errorf("capabilities %v missing %q", m.Can, w)
		}
	}
	if containsString(m.Can, "delete permanently") {
		t.Errorf("capabilities %v claims a right Drive did not grant", m.Can)
	}
	if New(nil, Options{}) != nil {
		t.Error("New(nil) should be nil")
	}
}

func TestNewFileShortcut(t *testing.T) {
	f := &gdrive.File{
		ID: "id-shortcut-fixture", Name: "Notes shortcut", MimeType: gdrive.MimeShortcut,
		ShortcutDetails: &gdrive.ShortcutDetails{TargetID: "id-notes-fixture", TargetMimeType: gdrive.MimeDocument},
	}
	m := New(f, Options{})
	if !m.IsShortcut || m.ShortcutTargetID != "id-notes-fixture" || m.ShortcutTargetKind != "Google Doc" {
		t.Errorf("shortcut details lost: %+v", m)
	}
}

func TestNewFileContentRestriction(t *testing.T) {
	f := &gdrive.File{ID: "id-x-fixture", ContentRestrictions: []*gdrive.ContentRestriction{{ReadOnly: true, Reason: "under review"}}}
	m := New(f, Options{})
	if !m.ContentLocked || m.ContentLockedReason != "under review" {
		t.Errorf("content restriction lost: %+v", m)
	}
}

func TestSharingSummaries(t *testing.T) {
	cases := []struct {
		name   string
		shared bool
		perms  []*gdrive.Permission
		want   string
	}{
		{"private", false, []*gdrive.Permission{{Type: "user", Role: "owner", EmailAddress: "person@example.com"}}, "private to you"},
		{"three people", true, []*gdrive.Permission{
			{Type: "user", Role: "owner", EmailAddress: "person@example.com"},
			{Type: "user", Role: "writer", EmailAddress: "a@example.com"},
			{Type: "user", Role: "writer", EmailAddress: "b@example.com"},
			{Type: "user", Role: "reader", EmailAddress: "c@example.com"},
		}, "shared with 3 people: 2 can edit, 1 can view"},
		{"one person", true, []*gdrive.Permission{
			{Type: "user", Role: "commenter", EmailAddress: "a@example.com"},
		}, "shared with 1 person: 1 can comment"},
		{"link", true, []*gdrive.Permission{
			{Type: "anyone", Role: "reader"},
		}, "anyone with the link can view"},
		{"discoverable link", true, []*gdrive.Permission{
			{Type: "anyone", Role: "reader", AllowFileDiscovery: true},
		}, "anyone on the internet can view and can find it by search"},
		{"domain", true, []*gdrive.Permission{
			{Type: "domain", Role: "reader", Domain: "example.com"},
		}, "everyone at example.com can view with the link"},
	}
	for _, c := range cases {
		got := NewSharing(c.shared, c.perms, true).Summary()
		if got != c.want {
			t.Errorf("%s: summary = %q, want %q", c.name, got, c.want)
		}
	}
	// Drive saying "shared" while the only visible grant is the owner is
	// reported as such: claiming the file is private would be a lie.
	ownerOnly := NewSharing(true, []*gdrive.Permission{{Type: "user", Role: "owner", EmailAddress: "person@example.com"}}, true)
	if got := ownerOnly.Summary(); !strings.Contains(got, "no grants are visible") {
		t.Errorf("summary = %q", got)
	}
}

func TestSharingUnknownPermissions(t *testing.T) {
	s := NewSharing(true, nil, false)
	if !s.Unknown {
		t.Fatal("a nil permission list means unknown, not private")
	}
	if !strings.Contains(s.Summary(), "cannot read the permission list") {
		t.Errorf("summary = %q", s.Summary())
	}
	if got := NewSharing(false, nil, false).Summary(); !strings.Contains(got, "unknown") {
		t.Errorf("summary = %q", got)
	}
}

func TestSharingPublicAndInherited(t *testing.T) {
	s := NewSharing(true, []*gdrive.Permission{
		{Type: "anyone", Role: "reader"},
		{Type: "user", Role: "writer", EmailAddress: "a@example.com",
			Details: []*gdrive.PermissionDetails{{Inherited: true, InheritedFrom: "id-drive-marketing"}}},
	}, true)
	if !s.Public() {
		t.Error("an anyone grant is public exposure")
	}
	if s.Inherited != 1 {
		t.Errorf("inherited = %d", s.Inherited)
	}
	if !strings.Contains(s.Summary(), "inherited from the shared drive") {
		t.Errorf("summary = %q", s.Summary())
	}
	for _, g := range s.Grants {
		if g.Role == "writer" && !g.Inherited() {
			t.Error("the inherited grant lost its source")
		}
	}
	if NewSharing(true, []*gdrive.Permission{}, true).Public() {
		t.Error("no grants is not public")
	}
}

func TestSharingPendingOwner(t *testing.T) {
	s := NewSharing(true, []*gdrive.Permission{
		{Type: "user", Role: "writer", EmailAddress: "new@example.com", PendingOwner: true},
	}, true)
	if s.PendingOwner == "" {
		t.Fatal("a pending owner should be recorded")
	}
	if !strings.Contains(s.Summary(), "waiting to be accepted") {
		t.Errorf("summary = %q", s.Summary())
	}
}

func TestGrantLabels(t *testing.T) {
	cases := []struct {
		g    Grant
		want string
	}{
		{Grant{Type: "anyone", Who: "anyone"}, "anyone with the link"},
		{Grant{Type: "domain", Who: "example.com"}, "everyone at example.com"},
		{Grant{Type: "group", Who: "team@example.com", Name: "The Team"}, "The Team (group team@example.com)"},
		{Grant{Type: "group", Who: "team@example.com"}, "group team@example.com"},
		{Grant{Type: "user", Who: "a@example.com", Name: "A Person"}, "A Person <a@example.com>"},
		{Grant{Type: "user", Who: "a@example.com"}, "a@example.com"},
		{Grant{Type: "user", Name: "Nameless"}, "Nameless"},
	}
	for _, c := range cases {
		if got := c.g.Label(); got != c.want {
			t.Errorf("Label() = %q, want %q", got, c.want)
		}
	}
}

func TestRoleWords(t *testing.T) {
	cases := map[string]string{
		RoleOwner: "owns it", RoleWriter: "can edit", RoleCommenter: "can comment",
		RoleReader: "can view", RoleOrganizer: "manages the drive",
		RoleFileOrganizer: "organises content", "": "has no role", "custom": "has role custom",
	}
	for role, want := range cases {
		if got := RoleWords(role); got != want {
			t.Errorf("RoleWords(%q) = %q, want %q", role, got, want)
		}
	}
}

func TestGrantsAreOrderedByAccess(t *testing.T) {
	s := NewSharing(true, []*gdrive.Permission{
		{Type: "user", Role: "reader", EmailAddress: "z@example.com"},
		{Type: "user", Role: "owner", EmailAddress: "a@example.com"},
		{Type: "user", Role: "writer", EmailAddress: "m@example.com"},
	}, true)
	got := []string{s.Grants[0].Role, s.Grants[1].Role, s.Grants[2].Role}
	if strings.Join(got, ",") != "owner,writer,reader" {
		t.Errorf("order = %v", got)
	}
}

func TestCanIsNilForUnknownCapabilities(t *testing.T) {
	if Can(nil) != nil {
		t.Error("Can(nil) should be nil, not an empty claim")
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		0: "0 B", 512: "512 B", 1024: "1.0 KiB", 1536: "1.5 KiB",
		4096: "4.0 KiB", 1 << 20: "1.0 MiB", 1 << 30: "1.0 GiB",
		1 << 40: "1.0 TiB", 150 * 1024: "150 KiB",
	}
	for n, want := range cases {
		if got := HumanSize(n); got != want {
			t.Errorf("HumanSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestHumanTime(t *testing.T) {
	now := time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC)
	ts := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	if got := HumanTime(ts, now); got != "2026-03-04 09:00Z (2 days ago)" {
		t.Errorf("HumanTime = %q", got)
	}
	if got := HumanTime(time.Time{}, now); got != "unknown" {
		t.Errorf("HumanTime(zero) = %q", got)
	}
	if got := HumanTime(ts, time.Time{}); got != "2026-03-04 09:00Z" {
		t.Errorf("HumanTime without a now = %q", got)
	}
}

func TestAgo(t *testing.T) {
	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	cases := map[time.Duration]string{
		30 * time.Second:     "just now",
		5 * time.Minute:      "5 minutes ago",
		time.Hour:            "1 hour ago",
		50 * time.Hour:       "2 days ago",
		60 * 24 * time.Hour:  "2 months ago",
		800 * 24 * time.Hour: "2 years ago",
	}
	for d, want := range cases {
		if got := Ago(now.Add(-d), now); got != want {
			t.Errorf("Ago(-%s) = %q, want %q", d, got, want)
		}
	}
	if got := Ago(now.Add(48*time.Hour), now); got != "in 2 days" {
		t.Errorf("a future time = %q", got)
	}
}

func containsString(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func TestArticle(t *testing.T) {
	cases := map[string]string{
		"Excel spreadsheet": "an", "OpenDocument text": "an", "Google Doc": "a",
		"PDF": "a", "PNG image": "a", "SVG image": "an", "XML file": "an",
		"HTML file": "an", "MATROSKA video": "a", "folder": "a", "": "a",
	}
	for phrase, want := range cases {
		if got := Article(phrase); got != want {
			t.Errorf("Article(%q) = %q, want %q", phrase, got, want)
		}
	}
	f := &gdrive.File{MimeType: gdrive.MimeShortcut, ShortcutDetails: &gdrive.ShortcutDetails{
		TargetMimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"}}
	if got := Kind(f); got != "shortcut to an Excel spreadsheet" {
		t.Errorf("Kind = %q", got)
	}
}

func TestSharedDriveItemIsNeverCalledPrivate(t *testing.T) {
	f := &gdrive.File{ID: "id-x-fixture", Name: "Plan", MimeType: gdrive.MimeDocument,
		DriveID: "id-drive-marketing", Permissions: []*gdrive.Permission{}}
	m := New(f, Options{Location: Location{Drive: "Marketing", SharedDrive: true}})
	got := m.Sharing.Summary()
	if strings.Contains(got, "private") {
		t.Errorf("a shared-drive item was called private: %q", got)
	}
	if !strings.Contains(got, "Marketing") {
		t.Errorf("summary should name the drive: %q", got)
	}

	withGrant := &gdrive.File{ID: "id-y-fixture", Name: "Plan", DriveID: "id-drive-marketing", Shared: true,
		Permissions: []*gdrive.Permission{{Type: "user", Role: "reader", EmailAddress: "a@example.com"}}}
	m2 := New(withGrant, Options{Location: Location{Drive: "Marketing", SharedDrive: true}})
	if !strings.Contains(m2.Sharing.Summary(), "plus everyone with access to the shared drive Marketing") {
		t.Errorf("summary = %q", m2.Sharing.Summary())
	}

	// A My Drive file keeps the plain summary.
	my := &gdrive.File{ID: "id-z-fixture", Name: "Notes", Permissions: []*gdrive.Permission{}}
	if got := New(my, Options{}).Sharing.Summary(); got != "private to you" {
		t.Errorf("My Drive summary = %q", got)
	}
}

func TestAGoogleDocumentReportsNoSize(t *testing.T) {
	// Drive reports one byte for a new, empty Doc and for a long one:
	// the number is the metadata it keeps, not the size of anything a
	// person can get. Showing it invites a reading it cannot support.
	doc := New(&gdrive.File{
		ID: "id-notes-fixture", Name: "Notes", MimeType: gdrive.MimeDocument, Size: "1",
	}, Options{})
	if doc.HasSize {
		t.Errorf("a Google Doc reported a size of %d bytes", doc.Size)
	}

	blob := New(&gdrive.File{
		ID: "id-log-fixture", Name: "server.log", MimeType: "text/plain", Size: "4096",
	}, Options{})
	if !blob.HasSize || blob.Size != 4096 {
		t.Errorf("a file with bytes reported size %d (known: %v)", blob.Size, blob.HasSize)
	}

	// A folder and a shortcut are Google types but not documents, and
	// neither claims a size of its own anyway.
	for _, mime := range []string{gdrive.MimeFolder, gdrive.MimeShortcut} {
		if got := New(&gdrive.File{MimeType: mime}, Options{}); got.HasSize {
			t.Errorf("%s reported a size", mime)
		}
	}
}

func TestKindWithArticle(t *testing.T) {
	cases := map[string]string{
		gdrive.MimeDocument: "a Google Doc",
		gdrive.MimeFolder:   "a folder",
		"application/pdf":   "a PDF",
		"image/svg+xml":     "an SVG image",
	}
	for mime, want := range cases {
		if got := KindWithArticle(&gdrive.File{MimeType: mime}); got != want {
			t.Errorf("KindWithArticle(%s) = %q, want %q", mime, got, want)
		}
	}
}
