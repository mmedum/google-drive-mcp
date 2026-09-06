package redact_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/render"
	"github.com/mmedum/google-drive-mcp/scripts/internal/redact"
)

// A redactor tested against lines somebody typed out is a redactor that
// covers the surface as it was on the day they typed them. Phase 3 added
// comments, whose author Drive gives no address for, and the redactor
// went on reporting a clean transcript because nothing tied it to the
// renderers.
//
// So this test does not describe the output. It RENDERS it, through the
// same functions the server uses, and fails if a name survives.
//
// That was once claimed to catch a renderer which starts printing a
// person somewhere new "without anybody remembering to add a case", and
// it did not: rendered() below is a map somebody types, and phase 4
// added two renderers, added neither, and watched one leak a name into a
// live transcript while this test passed. TestEveryRendererIsCovered at
// the bottom of this file is what makes the claim true now — it reads
// internal/render's syntax tree and fails on a renderer nothing here
// produces. The fixtures are still written by hand, because each
// renderer takes its own arguments; what is no longer possible is
// forgetting one in silence.

// The names are invented and unmistakable: an incidental match would
// make this test pass for the wrong reason, and a name copied from a
// real response would itself be the leak.
const (
	me        = "Wendell Ashgrove"
	other     = "Perpetua Blackwood"
	their     = "someone@corp.example.net"
	fixtureID = "1SyntheticFixtureFileIdAAAAAAAAAAAA"
	// A shared drive's id has a shape of its own, and the redactor has to
	// see it as an id: a fixture with a made-up "id-drive-1" would prove
	// only that the redactor ignores things that do not look like ids.
	fixtureDriveID = "0ASyntheticFixtureDriveIdAAAAAAA"
)

var now = time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)

func meUser() *gdrive.User    { return &gdrive.User{DisplayName: me, Me: true} }
func otherUser() *gdrive.User { return &gdrive.User{DisplayName: other, EmailAddress: their} }

// bareUser is the shape with nothing beside it: Drive populates no
// address on a comment's author, so this is all a comment listing has.
func bareUser() *gdrive.User { return &gdrive.User{DisplayName: other} }

func fixtureGrants() []*gdrive.Permission {
	return []*gdrive.Permission{
		{ID: "id-permission-1", Type: "user", Role: "owner", EmailAddress: "person@example.com", DisplayName: me},
		{ID: "id-permission-2", Type: "user", Role: "writer", EmailAddress: their, DisplayName: other},
	}
}

func fixtureFile() *model.File {
	return model.New(&gdrive.File{
		ID: fixtureID, Name: "Budget.xlsx",
		MimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		Size:     "4096", CreatedTime: "2026-01-02T10:00:00Z", ModifiedTime: "2026-03-04T09:00:00Z",
		Owners: []*gdrive.User{meUser()}, LastModifyingUser: otherUser(),
		SharingUser: otherUser(), OwnedByMe: true, Shared: true,
		Capabilities: &gdrive.Capabilities{CanEdit: true, CanShare: true},
		Permissions:  fixtureGrants(),
	}, model.Options{Permissions: fixtureGrants(), PermissionsKnown: true})
}

// rendered is every output that can carry a person, produced the way the
// server produces it.
func rendered() map[string]string {
	f := fixtureFile()
	out := map[string]string{
		"file card": render.FileCard(f, render.FileCardOptions{Now: now}),
		"listing":   render.Listing([]*model.File{f}, render.ListingOptions{Now: now, Title: "a folder", ShowLocation: true}),
		"tree": render.Tree(&render.TreeNode{File: f, Items: 1,
			Children: []*render.TreeNode{{File: f}}}, render.TreeOptions{Title: "a tree"}),
		"permissions": render.Permissions(f.Sharing.Grants, render.PermissionsOptions{
			Subject: "Budget.xlsx", Sharing: f.Sharing, CanShare: true}),
		"revisions": render.Revisions([]*model.Revision{
			model.NewRevision(&gdrive.Revision{
				ID: "id-revision-1", ModifiedTime: "2026-03-04T09:00:00Z",
				LastModifyingUser: otherUser(), Size: "1024",
			}, "id-revision-1"),
			model.NewRevision(&gdrive.Revision{
				ID: "id-revision-2", ModifiedTime: "2026-03-03T09:00:00Z",
				LastModifyingUser: meUser(),
			}, "id-revision-1"),
		}, render.RevisionsOptions{Subject: "Budget.xlsx", Now: now}),
		"comments": render.Comments([]*model.Comment{
			model.NewComment(&gdrive.Comment{
				ID: "id-comment-1", Content: "is this right?", CreatedTime: "2026-03-04T09:00:00Z",
				Author: bareUser(),
				Replies: []*gdrive.Reply{{
					ID: "id-reply-1", Content: "yes", CreatedTime: "2026-03-05T09:00:00Z",
					Author: meUser(),
				}},
			}),
		}, render.CommentsOptions{Subject: "Budget.xlsx", Now: now, CanComment: true}),
		"approvals": render.Approvals([]*model.Approval{
			model.NewApproval(&gdrive.Approval{
				ApprovalID: "id-approval-1", Status: model.ApprovalInProgress,
				CreateTime: "2026-03-04T09:00:00Z", Initiator: otherUser(),
				ReviewerResponses: []*gdrive.ReviewerResponse{
					{Reviewer: meUser(), Response: model.ResponseNone},
					{Reviewer: bareUser(), Response: model.ResponseApproved},
				},
			}),
		}, render.ApprovalsOptions{Subject: "Budget.xlsx", Now: now}),
		"account": render.Account(&gdrive.About{
			// With an address, as about.get really answers — and the test
			// below covers the shape without one, which is what showed
			// that the name was protected only by the address beside it.
			User:         &gdrive.User{DisplayName: me, EmailAddress: their, Me: true},
			StorageQuota: &gdrive.StorageQuota{Limit: "16106127360", Usage: "4294967296"},
		}, render.AccountOptions{}),
		"drives": render.Drives([]*model.Drive{
			model.NewDrive(&gdrive.Drive{ID: fixtureDriveID, Name: "Marketing"}),
		}, render.DrivesOptions{}),
		"drive card": render.DriveCard(
			model.NewDrive(&gdrive.Drive{ID: fixtureDriveID, Name: "Marketing"}),
			render.DriveCardOptions{}),
		"changes": render.Changes([]*model.Change{
			model.NewChange(&gdrive.Change{
				FileID: fixtureID, Time: "2026-03-04T09:00:00Z", File: &gdrive.File{
					ID: fixtureID, Name: "Budget.xlsx", MimeType: "text/csv",
					LastModifyingUser: otherUser(),
				},
			}, "My Drive"),
		}, render.ChangesOptions{Now: now}),
		"file text": render.FileText(fixtureFile(), render.FileTextOptions{
			Text: "a line of the file", Now: now,
		}),
		"download": render.Download(fixtureFile(), render.DownloadOptions{
			Path: "/tmp/Budget.xlsx", Bytes: 4096, Now: now,
		}),
		"access requests": render.AccessRequests([]*model.AccessRequest{
			model.NewAccessRequest(&gdrive.AccessProposal{
				ProposalID: "id-request-1", RequesterEmailAddress: their,
				RecipientEmailAddress: their, CreateTime: "2026-03-05T09:00:00Z",
				RolesAndViews: []*gdrive.AccessProposalRoleAndView{{Role: "writer"}},
			}),
		}, render.AccessRequestsOptions{Subject: "Budget.xlsx", Now: now, CanShare: true}),
	}
	return out
}

func TestNoRendererLeaksAPersonThroughTheRedactor(t *testing.T) {
	all := rendered()
	if len(all) < 8 {
		t.Fatalf("only %d renderers are covered; this test is not looking at the surface", len(all))
	}
	for what, text := range all {
		t.Run(what, func(t *testing.T) {
			// A fresh redactor per renderer: a transcript is redacted by
			// one, but a leak that only shows up on the first sighting of
			// a name would hide behind a shared memory.
			got := redact.NewRedactor(false).Do(text)
			for _, secret := range []string{"Wendell", "Ashgrove", "Perpetua", "Blackwood", their} {
				if strings.Contains(got, secret) {
					t.Errorf("%q survived redaction:\n%s", secret, got)
				}
			}
		})
	}
}

// noPersonToday are the two renderers that print no display name, with
// the reason. They stay in the set above so that a change which starts
// printing one is caught without anybody remembering to add it here;
// they are named here so the floor below can tell "prints no person"
// from "the fixture forgot to supply one".
var noPersonToday = map[string]string{
	"tree":            "a tree prints names, ids and kinds, and no people at all",
	"access requests": "an access proposal carries addresses only — Drive attaches no display name to one",
	"drives":          "a shared drive has a name, a role and restrictions; the people in it are permissions, and list_permissions renders those",
	"drive card":      "as the drives listing",
	"changes":         "a change says which file changed and when, not who changed it: the feed's own File carries a last modifier, and this server does not print it",
	"file text":       "the text of a file, headed by its name and size — the people who touched it are on the file card, not here",
	"download":        "where the bytes went and whether the checksum matched; no people",
}

// TestTheFixturesReallyCarrySomethingToHide is the floor. Three of these
// fixtures proved nothing on the first run: one because the renderer has
// no person in it, one because a proposal has only addresses, and one
// because the grants were passed on the wire type when model.New reads
// them from its options — so the permissions listing rendered "no
// grants" and the leak test passed over an empty page.
func TestTheFixturesReallyCarrySomethingToHide(t *testing.T) {
	names, addresses := 0, 0
	for what, text := range rendered() {
		hasName := strings.Contains(text, me) || strings.Contains(text, other)
		hasAddress := strings.Contains(text, their)
		if hasName {
			names++
		}
		if hasAddress {
			addresses++
		}
		// The real question is whether redacting this fixture proves
		// anything, and the direct way to ask it is to redact it and see
		// whether anything moved. Naming the file-id constant instead —
		// which is what this did — passes a fixture that happens to carry
		// that one id and fails one carrying a drive id, an address or a
		// link, none of which is less of a secret.
		if redact.NewRedactor(false).Do(text) == text {
			t.Errorf("redacting the %s fixture changes nothing, so redacting it proves "+
				"nothing:\n%s", what, text)
			continue
		}
		if !hasName && noPersonToday[what] == "" {
			t.Errorf("the %s fixture carries no display name and is not one of the renderers that "+
				"never print one; either the fixture is wrong or noPersonToday needs a new entry with "+
				"a reason:\n%s", what, text)
		}
	}
	if names < 4 || addresses < 2 {
		t.Errorf("%d fixtures carry a name and %d carry an address; the set is not exercising the "+
			"redactor", names, addresses)
	}
}

// render.Activity is not in the set above, and the reason is worth
// stating rather than left as an absence.
//
// It prints no person AT ALL — not a redacted one, none. The Drive
// Activity API identifies an actor by a People API resource name and an
// is-it-you flag, and gives no display name or address for anybody, so
// the renderer says "you" or "somebody else". Putting it in the leak set
// would mean redacting a fixture with nothing in it, which is what that
// set's floor exists to refuse.
//
// So its assertion is the stronger one: given an actor, no trace of them
// reaches the output — including the resource name itself, which is an
// identifier of a person even though it is not a name. If this renderer
// starts printing people, this fails, and the fix is to move it into
// rendered() above where the redactor has to learn its positions.
//
// render.Labels is not here either, and needs no test: a label
// DEFINITION as this server models it has no person in it to print. The
// wire type's creator and publisher are deliberately outside the subset
// in internal/gdrive, so there is nothing for a fixture to supply.
func TestActivityNamesNobody(t *testing.T) {
	const personID = "people/1234567890"
	event, _ := model.NewActivity(&gdrive.DriveActivity{
		Timestamp:           "2026-03-04T09:00:00Z",
		PrimaryActionDetail: &gdrive.ActionDetail{Edit: &struct{}{}},
		Actors: []*gdrive.ActivityActor{{User: &gdrive.ActivityUser{
			KnownUser: &gdrive.ActivityKnownUser{PersonName: personID},
		}}},
		Targets: []*gdrive.ActivityTarget{{DriveItem: &gdrive.ActivityDriveItem{
			Name: "items/" + fixtureID, Title: "Budget.xlsx",
		}}},
	})
	text := render.Activity([]*model.Activity{event}, render.ActivityOptions{
		Subject: "Budget.xlsx", Now: now,
	})

	for _, secret := range []string{me, other, their, personID} {
		if strings.Contains(text, secret) {
			t.Errorf("the activity renderer printed %q, so it names people after all and belongs "+
				"in rendered() where the redactor learns its positions:\n%s", secret, text)
		}
	}
	if !strings.Contains(text, "somebody else") {
		t.Errorf("the renderer did not say who in the only terms it can:\n%s", text)
	}
}

// TestEveryRendererIsCovered is what makes the claim at the top of this
// file true rather than aspirational.
//
// rendered() is a map somebody types, and until phase 4 the comment above
// it said a new renderer "fails here without anybody remembering to come
// and add a case". It does not: two renderers were added, neither was
// added here, the test passed, and one of them leaked a name into a live
// transcript. A guard described as automatic because describing it is
// free is worse than one honestly described as remembered — people stop
// checking the thing they have been told is checked.
//
// So this reads internal/render's own syntax tree. Every exported
// function returning a string is a renderer; each must appear in
// rendered() above, or in notRenderers below with the reason it is not
// one. Forgetting is now a failure with the name of what was forgotten.
func TestEveryRendererIsCovered(t *testing.T) {
	const dir = "../../../internal/render"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	found := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() || !returnsOnlyString(fn) {
				continue
			}
			found[fn.Name.Name] = true
		}
	}
	if len(found) < 15 {
		t.Fatalf("found only %d renderers in %s; this test is not reading the package", len(found), dir)
	}

	covered := rendered()
	for name := range found {
		if reason := notRenderers[name]; reason != "" {
			continue
		}
		key := renderedKey[name]
		if key == "" || covered[key] == "" {
			t.Errorf("render.%s is exported and returns a string, and nothing in rendered() produces "+
				"it. Add a fixture there, or add it to notRenderers with the reason it cannot carry "+
				"a person", name)
		}
	}
}

// returnsOnlyString reports whether a function's single result is a
// string, which is what every renderer in this package is.
func returnsOnlyString(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return false
	}
	ident, ok := fn.Type.Results.List[0].Type.(*ast.Ident)
	return ok && ident.Name == "string"
}

// renderedKey maps a renderer's Go name to the key it appears under in
// rendered(). The keys are prose because a failure names them to a
// person; this is the join.
var renderedKey = map[string]string{
	"FileCard":       "file card",
	"Listing":        "listing",
	"Tree":           "tree",
	"Permissions":    "permissions",
	"Revisions":      "revisions",
	"Comments":       "comments",
	"AccessRequests": "access requests",
	"Approvals":      "approvals",
	"Account":        "account",
	"Drives":         "drives",
	"DriveCard":      "drive card",
	"Changes":        "changes",
	"FileText":       "file text",
	"Download":       "download",
}

// notRenderers are the exported string functions in internal/render that
// do not render a result, with the reason. Each is here on purpose: a
// name added without thought belongs in rendered() instead.
var notRenderers = map[string]string{
	"StripDataURIs": "a text filter over content, not a result: it takes a string and gives one back",
	"Checksum":      "one verdict word about a download's md5, with nothing in it but the verdict",
	"Activity":      "asserted by TestActivityNamesNobody, which is stronger: it prints no person at all",
	"Labels":        "a label definition as this server models it has no person in it to print",
}
