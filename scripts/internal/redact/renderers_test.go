package redact_test

import (
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
// same functions the server uses, and fails if a name survives. A
// renderer that starts printing a person somewhere new fails here
// without anybody remembering to come and add a case.

// The names are invented and unmistakable: an incidental match would
// make this test pass for the wrong reason, and a name copied from a
// real response would itself be the leak.
const (
	me        = "Wendell Ashgrove"
	other     = "Perpetua Blackwood"
	their     = "someone@corp.example.net"
	fixtureID = "1SyntheticFixtureFileIdAAAAAAAAAAAA"
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
	if len(all) < 7 {
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
		// A file id counts too: the tree prints no people and no
		// addresses, and an id is what it does carry for the redactor to
		// take.
		hasID := strings.Contains(text, fixtureID)
		if hasName {
			names++
		}
		if hasAddress {
			addresses++
		}
		if !hasName && !hasAddress && !hasID {
			t.Errorf("the %s fixture carries nothing the redactor hides, so redacting it proves "+
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
