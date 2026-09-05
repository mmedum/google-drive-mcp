package render

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// golden compares got with testdata/golden/<name>, or rewrites it with
// -update. Goldens are compared byte for byte, which is why
// .gitattributes pins LF on every platform.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run `go test ./internal/render -update`)", path, err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// now is the fixed clock every golden is rendered against.
var now = time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)

func sheet() *model.File {
	f := &gdrive.File{
		ID: "id-budget-fixture", Name: "Budget.xlsx",
		MimeType:          "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		Description:       "the quarterly numbers",
		Size:              "4096",
		MD5Checksum:       "d41d8cd98f00b204e9800998ecf8427e",
		CreatedTime:       "2026-01-02T10:00:00Z",
		ModifiedTime:      "2026-03-04T09:00:00Z",
		Owners:            []*gdrive.User{{DisplayName: "Test Person", EmailAddress: "person@example.com", Me: true}},
		LastModifyingUser: &gdrive.User{DisplayName: "Other Person", EmailAddress: "other@example.com"},
		WebViewLink:       "https://drive.google.com/file/d/id-budget-fixture/view",
		Starred:           true,
		Shared:            true,
		OwnedByMe:         true,
		Capabilities:      &gdrive.Capabilities{CanEdit: true, CanComment: true, CanShare: true, CanDownload: true, CanTrash: true, CanRename: true},
		Permissions: []*gdrive.Permission{
			{ID: "id-permission-1", Type: "user", Role: "owner", EmailAddress: "person@example.com", DisplayName: "Test Person"},
			{ID: "id-permission-2", Type: "user", Role: "writer", EmailAddress: "a@example.com", DisplayName: "A Person"},
			{ID: "id-permission-3", Type: "user", Role: "reader", EmailAddress: "b@example.com", DisplayName: "B Person"},
			{ID: "id-permission-4", Type: "anyone", Role: "reader"},
		},
		Properties: map[string]string{"team": "finance", "review": "2026-Q1"},
	}
	return model.New(f, model.Options{Location: model.Location{Drive: "My Drive", Folders: []string{"Projects", "2026"}}})
}

func TestFileCardGolden(t *testing.T) {
	golden(t, "file_card.txt", FileCard(sheet(), FileCardOptions{Now: now}))
}

func TestFileCardOfAGoogleDocGolden(t *testing.T) {
	f := &gdrive.File{
		ID: "id-notes-fixture", Name: "Meeting notes", MimeType: gdrive.MimeDocument,
		CreatedTime: "2026-02-01T08:30:00Z", ModifiedTime: "2026-03-05T16:45:00Z",
		Owners:            []*gdrive.User{{DisplayName: "Test Person", EmailAddress: "person@example.com", Me: true}},
		LastModifyingUser: &gdrive.User{DisplayName: "Test Person", EmailAddress: "person@example.com", Me: true},
		WebViewLink:       "https://docs.google.com/document/d/id-notes-fixture/edit",
		HeadRevisionID:    "rev-42",
		DriveID:           "id-drive-marketing",
		Capabilities:      &gdrive.Capabilities{CanEdit: true, CanComment: true, CanShare: true, CanDownload: true, CanTrash: true},
		Permissions:       []*gdrive.Permission{{Type: "user", Role: "owner", EmailAddress: "person@example.com"}},
	}
	m := model.New(f, model.Options{
		Location:      model.Location{Drive: "Marketing", SharedDrive: true, Folders: []string{"Q3"}},
		ExportFormats: []string{"docx", "epub", "html", "md", "odt", "pdf", "rtf", "txt", "zip"},
	})
	golden(t, "file_card_doc.txt", FileCard(m, FileCardOptions{Now: now}))
}

func TestFileCardOfATrashedShortcutGolden(t *testing.T) {
	f := &gdrive.File{
		ID: "id-shortcut-fixture", Name: "Budget shortcut", MimeType: gdrive.MimeShortcut,
		ShortcutDetails: &gdrive.ShortcutDetails{TargetID: "id-budget-fixture", TargetMimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		DriveID:         "id-drive-marketing",
		CreatedTime:     "2026-02-10T12:00:00Z", ModifiedTime: "2026-02-10T12:00:00Z",
		Trashed: true, TrashedTime: "2026-03-05T10:00:00Z",
		TrashingUser: &gdrive.User{DisplayName: "Test Person", EmailAddress: "person@example.com", Me: true},
		Capabilities: &gdrive.Capabilities{CanUntrash: true, CanDelete: true},
		Permissions:  []*gdrive.Permission{},
	}
	m := model.New(f, model.Options{Location: model.Location{Drive: "Marketing", SharedDrive: true}})
	golden(t, "file_card_shortcut.txt", FileCard(m, FileCardOptions{Now: now, FollowedShortcut: ""}))
}

func TestFileCardNil(t *testing.T) {
	if got := FileCard(nil, FileCardOptions{}); got != "(no file)\n" {
		t.Errorf("FileCard(nil) = %q", got)
	}
}

func TestFileCardNotesAFollowedShortcut(t *testing.T) {
	got := FileCard(sheet(), FileCardOptions{Now: now, FollowedShortcut: "id-shortcut-fixture"})
	if !strings.Contains(got, "followed the shortcut id-shortcut-fixture") {
		t.Errorf("output does not say a shortcut was followed:\n%s", got)
	}
}

func TestFileCardWarnsAboutACopyRestriction(t *testing.T) {
	f := &gdrive.File{ID: "id-x-fixture", Name: "Locked.pdf", MimeType: "application/pdf", CopyRequiresWriterPermission: true}
	got := FileCard(model.New(f, model.Options{}), FileCardOptions{Now: now})
	if !strings.Contains(got, "cannot copy, print or download") {
		t.Errorf("output does not mention the copy restriction:\n%s", got)
	}
}

func TestFileCardWarnsAboutAContentLock(t *testing.T) {
	f := &gdrive.File{ID: "id-x-fixture", Name: "Frozen.docx", MimeType: "application/pdf",
		ContentRestrictions: []*gdrive.ContentRestriction{{ReadOnly: true, Reason: "under review"}}}
	got := FileCard(model.New(f, model.Options{}), FileCardOptions{Now: now})
	if !strings.Contains(got, "under review") || !strings.Contains(got, "edits will be refused") {
		t.Errorf("output does not explain the content lock:\n%s", got)
	}
	bare := &gdrive.File{ID: "id-y-fixture", Name: "Frozen2.docx", ContentRestrictions: []*gdrive.ContentRestriction{{ReadOnly: true}}}
	if got := FileCard(model.New(bare, model.Options{}), FileCardOptions{}); !strings.Contains(got, "no reason given") {
		t.Errorf("a lock without a reason should say so:\n%s", got)
	}
}

func listingFixture() []*model.File {
	folder := &gdrive.File{ID: "id-2026-fixture", Name: "2026", MimeType: gdrive.MimeFolder,
		ModifiedTime:      "2026-03-01T11:00:00Z",
		LastModifyingUser: &gdrive.User{DisplayName: "Test Person", EmailAddress: "person@example.com", Me: true}}
	doc := &gdrive.File{ID: "id-notes-fixture", Name: "Meeting notes", MimeType: gdrive.MimeDocument,
		ModifiedTime: "2026-03-05T16:45:00Z", Shared: true,
		Permissions:       []*gdrive.Permission{{Type: "user", Role: "writer", EmailAddress: "a@example.com"}},
		LastModifyingUser: &gdrive.User{DisplayName: "Other Person", EmailAddress: "other@example.com"}}
	pdf := &gdrive.File{ID: "id-report-fixture", Name: "Report.pdf", MimeType: "application/pdf",
		Size: "2097152", ModifiedTime: "2026-02-20T09:15:00Z", Starred: true,
		Permissions:       []*gdrive.Permission{{Type: "anyone", Role: "reader"}},
		LastModifyingUser: &gdrive.User{DisplayName: "Test Person", EmailAddress: "person@example.com", Me: true}}
	loc := model.Location{Drive: "My Drive", Folders: []string{"Projects"}}
	return []*model.File{
		model.New(folder, model.Options{Location: loc}),
		model.New(doc, model.Options{Location: loc}),
		model.New(pdf, model.Options{Location: loc}),
	}
}

func TestListingGolden(t *testing.T) {
	out := Listing(listingFixture(), ListingOptions{
		Title: "My Drive/Projects — 3 items, folders first",
		Now:   now,
	})
	golden(t, "listing.txt", out)
}

func TestSearchListingGolden(t *testing.T) {
	out := Listing(listingFixture(), ListingOptions{
		Title:            `search: name contains "report" — 3 hits`,
		Now:              now,
		ShowLocation:     true,
		NextPageToken:    "offset-3",
		IncompleteSearch: true,
	})
	golden(t, "listing_search.txt", out)
}

func TestListingEmpty(t *testing.T) {
	out := Listing(nil, ListingOptions{Title: "My Drive/Empty — 0 items"})
	if !strings.Contains(out, "(nothing here)") {
		t.Errorf("output = %q", out)
	}
	out = Listing(nil, ListingOptions{Empty: "no file matched; `contains` matches word starts, not any substring"})
	if !strings.Contains(out, "word starts") {
		t.Errorf("a caller's own empty message should be used: %q", out)
	}
}

func TestTreeGolden(t *testing.T) {
	loc := model.Location{Drive: "My Drive"}
	mk := func(id, name, mime string, size string) *model.File {
		return model.New(&gdrive.File{ID: id, Name: name, MimeType: mime, Size: size}, model.Options{Location: loc})
	}
	root := &TreeNode{
		File: mk("id-projects-fixture", "Projects", gdrive.MimeFolder, ""), Items: 2,
		Children: []*TreeNode{
			{File: mk("id-2026-fixture", "2026", gdrive.MimeFolder, ""), Items: 2, Children: []*TreeNode{
				{File: mk("id-budget-fixture", "Budget.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "4096")},
				{File: mk("id-notes-fixture", "Meeting notes", gdrive.MimeDocument, "")},
			}},
			{File: mk("id-archive-fixture", "Archive", gdrive.MimeFolder, ""), Items: 812, NotEntered: "depth limit 2"},
		},
	}
	out := Tree(root, TreeOptions{
		Title: "My Drive/Projects — tree, depth 2, 4 items shown",
		Note:  "stopped at the depth limit; list_folder on Archive with recursive: true goes deeper",
	})
	golden(t, "tree.txt", out)
}

func TestTreeNil(t *testing.T) {
	if got := Tree(nil, TreeOptions{Title: "x"}); !strings.Contains(got, "(nothing here)") {
		t.Errorf("Tree(nil) = %q", got)
	}
}

func TestAccountGolden(t *testing.T) {
	about := &gdrive.About{
		User: &gdrive.User{DisplayName: "Test Person", EmailAddress: "person@example.com", Me: true},
		StorageQuota: &gdrive.StorageQuota{
			Limit: "16106127360", Usage: "4294967296", UsageInDrive: "3221225472", UsageInDriveTrash: "1073741824",
		},
		CanCreateDrives: true,
	}
	out := Account(about, AccountOptions{
		Sharing: "all", LocalDir: "/home/person/drive-files", MaxDownload: 1 << 30,
		Drives: []string{"Marketing", "Engineering"}, DrivesKnown: true,
		Tools: []string{"get_account", "get_file", "list_folder", "search_files"},
	})
	golden(t, "account.txt", out)
}

func TestAccountLockedDown(t *testing.T) {
	about := &gdrive.About{
		User:         &gdrive.User{EmailAddress: "person@example.com"},
		StorageQuota: &gdrive.StorageQuota{Usage: "1073741824", UsageInDrive: "1073741824"},
	}
	out := Account(about, AccountOptions{ReadOnly: true, Sharing: "off", Labels: true})
	for _, want := range []string{
		"read-only", "GDRIVE_SHARING=off", "destructive tools: disabled",
		"file transfer: off", "cannot create them", "no limit on this account", "labels: enabled",
		"(no display name)", "tools registered: none",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("account output missing %q:\n%s", want, out)
		}
	}
}

func TestStoragePercentageNeverReadsAsZeroForARealAmount(t *testing.T) {
	// 670 GiB of a 150 TiB Workspace allowance rounds to 0%, which looks
	// like a broken number rather than like plenty of room.
	cases := map[string]struct{ usage, limit int64 }{
		"0.5%":       {719 << 30, 150 << 40},
		"under 0.1%": {1 << 30, 150 << 40},
		"27%":        {4 << 30, 15 << 30},
	}
	for want, c := range cases {
		if got := percentUsed(c.usage, c.limit); got != want {
			t.Errorf("percentUsed(%d, %d) = %q, want %q", c.usage, c.limit, got, want)
		}
	}
	if got := percentUsed(1, 0); got != "unknown" {
		t.Errorf("percentUsed with no limit = %q", got)
	}
}

func TestAccountWithoutASignIn(t *testing.T) {
	if got := Account(nil, AccountOptions{}); !strings.Contains(got, "not signed in") {
		t.Errorf("Account(nil) = %q", got)
	}
	if got := Account(&gdrive.About{}, AccountOptions{}); !strings.Contains(got, "not signed in") {
		t.Errorf("Account without a user = %q", got)
	}
}

func TestBudget(t *testing.T) {
	cases := map[int]int{0: DefaultMaxChars, -5: DefaultMaxChars, 100: 100, MaxMaxChars + 1: MaxMaxChars}
	for in, want := range cases {
		if got := Budget(in); got != want {
			t.Errorf("Budget(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestJoinOr(t *testing.T) {
	if got := joinOr(nil, "none"); got != "none" {
		t.Errorf("joinOr = %q", got)
	}
	if got := joinOr([]string{"a", "b"}, "none"); got != "a, b" {
		t.Errorf("joinOr = %q", got)
	}
}

func TestFieldSkipsEmptyValues(t *testing.T) {
	var b buf
	b.field("a", "")
	b.field("b", "   ")
	b.field("c", "value")
	if got := b.String(); got != "c: value\n" {
		t.Errorf("buf = %q", got)
	}
}

func TestEmptyListingStillCarriesTheWarningAndTheContinuation(t *testing.T) {
	// Drive can return an empty page with a continuation token, and a
	// search it could not complete is most misleading when it matched
	// nothing: "no file matched" would be the wrong conclusion.
	out := Listing(nil, ListingOptions{
		Title: "search: name starting with \"widget\" — 0 hits",
		Empty: "no file matched.", IncompleteSearch: true, NextPageToken: "offset-25",
		Note: "note from the caller",
	})
	for _, want := range []string{"no file matched.", "search was incomplete", "offset-25", "note from the caller"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty listing missing %q:\n%s", want, out)
		}
	}
}

func TestTreeSaysWhenAFoldersOwnListingWasCutShort(t *testing.T) {
	loc := model.Location{Drive: "My Drive"}
	mk := func(id, name, mime string) *model.File {
		return model.New(&gdrive.File{ID: id, Name: name, MimeType: mime}, model.Options{Location: loc})
	}
	root := &TreeNode{
		File: mk("id-flat-fixture", "Flat", gdrive.MimeFolder), Items: 2,
		Truncated: "item limit 2 reached; more items here",
		Children: []*TreeNode{
			{File: mk("id-one-fixture", "One.txt", "text/plain")},
			{File: mk("id-two-fixture", "Two.txt", "text/plain")},
		},
	}
	out := Tree(root, TreeOptions{Title: "t"})
	if !strings.Contains(out, "listing cut short") {
		t.Errorf("a truncated listing must say so:\n%s", out)
	}
	if !strings.Contains(out, "2 items shown") {
		t.Errorf("the count is what was shown, not what the folder holds:\n%s", out)
	}
}

func TestTreeRootKeepsEveryLevelOfItsPath(t *testing.T) {
	// A folder named like the last component of its own path used to lose
	// a level to a string-suffix test.
	f := model.New(&gdrive.File{ID: "id-inner-fixture", Name: "Projects", MimeType: gdrive.MimeFolder},
		model.Options{Location: model.Location{Drive: "My Drive", Folders: []string{"Projects"}}})
	out := Tree(&TreeNode{File: f}, TreeOptions{})
	if !strings.Contains(out, "My Drive/Projects/Projects/") {
		t.Errorf("a level of the path was dropped:\n%s", out)
	}

	// A drive's own root is already named by its location, and must not
	// be doubled.
	rootFile := model.New(&gdrive.File{ID: "id-drive-marketing", Name: "Marketing", MimeType: gdrive.MimeFolder},
		model.Options{Location: model.Location{Drive: "Marketing", SharedDrive: true}, IsDriveRoot: true})
	out = Tree(&TreeNode{File: rootFile}, TreeOptions{})
	if strings.Contains(out, "Marketing (shared drive)/Marketing") {
		t.Errorf("the drive root's name was doubled:\n%s", out)
	}
}

func TestWriteCardGolden(t *testing.T) {
	golden(t, "write_card.txt", FileCard(sheet(), FileCardOptions{
		Now: now, Action: ActionUpdated,
		Changes: []Change{
			{Field: "name", From: "Budget.xlsx", To: "Budget 2026.xlsx"},
			{Field: "description", From: "", To: "the quarterly numbers"},
		},
		Note: "the content is unchanged; update_content replaces that.",
	}))
}

func TestFileTextGolden(t *testing.T) {
	golden(t, "file_text.txt", FileText(sheet(), FileTextOptions{
		Now: now, Format: "csv", Offset: 0, Bytes: 24, Total: 96, More: true,
		Note: "this is the FIRST SHEET only.",
		Text: "name,amount\nfirst,1\nsecond,2\n",
	}))
}

func TestFileTextOfAWholeFile(t *testing.T) {
	got := FileText(sheet(), FileTextOptions{Now: now, Format: "text", Bytes: 5, Total: 5, Text: "hello"})
	if !strings.Contains(got, "the whole file") {
		t.Errorf("output:\n%s", got)
	}
	if !strings.HasSuffix(got, "hello\n") {
		t.Errorf("the text should end with a newline:\n%q", got)
	}
}

func TestFileTextOfAnEmptyFile(t *testing.T) {
	got := FileText(sheet(), FileTextOptions{Now: now, Format: "text"})
	if !strings.Contains(got, "the file is empty") {
		t.Errorf("output:\n%s", got)
	}
}

func TestFileTextPastTheEnd(t *testing.T) {
	got := FileText(sheet(), FileTextOptions{Now: now, Format: "text", Offset: 500})
	if !strings.Contains(got, "the file ends before it") {
		t.Errorf("output:\n%s", got)
	}
}

func TestFileTextNil(t *testing.T) {
	if got := FileText(nil, FileTextOptions{}); got != "(no file)\n" {
		t.Errorf("FileText(nil) = %q", got)
	}
}

func TestStripDataURIs(t *testing.T) {
	in := "before ![a chart](data:image/png;base64,AAAA) between ![](data:image/gif;base64,BBBB) after"
	got := StripDataURIs(in)
	if strings.Contains(got, "base64") {
		t.Errorf("an inlined image survived: %s", got)
	}
	if strings.Count(got, "[image removed]") != 2 {
		t.Errorf("both images should be marked: %s", got)
	}
	// A markdown link that is not a data URI is left alone.
	kept := "![a chart](chart.png)"
	if StripDataURIs(kept) != kept {
		t.Errorf("a plain image link was rewritten: %s", StripDataURIs(kept))
	}
}

func TestDownloadGolden(t *testing.T) {
	golden(t, "download.txt", Download(sheet(), DownloadOptions{
		Now: now, Path: "/home/person/drive/Budget-id-bud.xlsx", Bytes: 4096, Format: "xlsx",
		Checksum: model.Checksum{State: model.ChecksumMatch},
	}))
}

func TestDownloadNil(t *testing.T) {
	if got := Download(nil, DownloadOptions{}); got != "(no file)\n" {
		t.Errorf("Download(nil) = %q", got)
	}
}

func TestNewWriteJSONCarriesTheText(t *testing.T) {
	m := sheet()
	text := FileCard(m, FileCardOptions{Now: now, Action: ActionCreated})
	got := NewWriteJSON(m, ActionCreated, "a note", text, []Change{{Field: "name", From: "a", To: "b"}})
	if got.Summary != text {
		t.Error("the structured result does not carry the prose a client may show instead")
	}
	if got.File == nil || got.File.ID != m.ID || got.File.Size != 4096 {
		t.Errorf("file = %+v", got.File)
	}
	if got.Action != "created" || got.Note != "a note" || len(got.Changes) != 1 {
		t.Errorf("result = %+v", got)
	}
	if NewWriteJSON(nil, ActionCreated, "", "", nil).File != nil {
		t.Error("a result with no file should carry none")
	}
}

func TestChecksumSaysWhichOfTheFourItIs(t *testing.T) {
	// "Nothing to compare against" and "it matched" are the two that
	// must never read alike: one is a guarantee and the other is its
	// absence.
	cases := map[model.ChecksumState]string{
		model.ChecksumMatch:         "verified",
		model.ChecksumMismatch:      "MISMATCH",
		model.ChecksumNotComparable: "not compared",
		model.ChecksumNotPublished:  "no checksum",
	}
	for state, want := range cases {
		got := Checksum(model.Checksum{State: state, Expected: "aaa", Actual: "bbb", Why: "it is an export"})
		if !strings.Contains(got, want) {
			t.Errorf("state %d rendered %q, want it to contain %q", state, got, want)
		}
	}
	if strings.Contains(Checksum(model.Checksum{State: model.ChecksumNotPublished}), "verified") {
		t.Error("a file with no checksum was described as verified")
	}
}

func TestDryRunIsSaidOnceByTheCard(t *testing.T) {
	got := FileCard(sheet(), FileCardOptions{Now: now, Action: ActionMoved, DryRun: true})
	if !strings.Contains(got, "NOTHING WAS CHANGED") {
		t.Errorf("a dry run did not say so:\n%s", got)
	}
}

// TestRenderersSurviveANilRow covers the layer the service guard sits in
// front of. A renderer here is pure and is the last thing between a
// malformed response and the process: a nil in a slice must cost one
// missing row, not a SIGSEGV that takes the stdio server down.
func TestRenderersSurviveANilRow(t *testing.T) {
	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)

	changes := Changes([]*model.Change{
		nil,
		{Kind: "file", ID: "id-fixture", Name: "Budget.xlsx", At: now},
	}, ChangesOptions{Now: now})
	if !strings.Contains(changes, "Budget.xlsx") {
		t.Errorf("the nil swallowed the row beside it:\n%s", changes)
	}

	revisions := Revisions([]*model.Revision{
		nil,
		{ID: "id-revision-fixture", Modified: now},
	}, RevisionsOptions{Now: now})
	if !strings.Contains(revisions, "id-revision-fixture") {
		t.Errorf("the nil swallowed the revision beside it:\n%s", revisions)
	}

	drives := Drives([]*model.Drive{
		nil,
		{ID: "id-drive-fixture", Name: "Marketing"},
	}, DrivesOptions{})
	if !strings.Contains(drives, "Marketing") {
		t.Errorf("the nil swallowed the drive beside it:\n%s", drives)
	}
}
