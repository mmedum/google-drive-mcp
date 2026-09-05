package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/render"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// Every file argument spells out the accepted forms in its own schema
// description: a model reads one tool's schema at a time, and a struct
// tag cannot be composed from a constant.

// PermissionsInput selects what to describe.
type PermissionsInput struct {
	File string `json:"file" jsonschema:"the item to describe: a file id, any Drive URL, the word root for My Drive, a path from My Drive like /Projects/2026, or a shared drive as drive:Marketing, which lists that drive's members. A name or path matching more than one item is refused with the candidates listed."`
}

// ShareInput is one grant to make or change.
type ShareInput struct {
	File              string `json:"file" jsonschema:"what to share: a file id, any Drive URL, a path from My Drive, or a shared drive as drive:Marketing to add a member to it. A shortcut is shared itself, not what it points at."`
	Principal         string `json:"principal" jsonschema:"who gets access: an address like someone@example.com, group:team@example.com for a Google group, domain:example.com for everyone in an organisation, or anyone for a link that needs no sign-in"`
	Role              string `json:"role" jsonschema:"what they may do: reader, commenter, writer, or file_organizer and organizer inside a shared drive. owner hands the file over and needs transfer_ownership."`
	Notify            bool   `json:"notify,omitempty" jsonschema:"send Google's notification email. Off by default: a tool call is not a reason to put mail in somebody's inbox. An ownership transfer always mails, whatever this says."`
	Message           string `json:"message,omitempty" jsonschema:"a line to include in that email; only used when notify is true"`
	Expires           string `json:"expires,omitempty" jsonschema:"when the grant should end, as a date like 2026-12-01 or a duration like 30d, or never to remove an expiry a grant already has. People and groups only, and Drive's limit is a year. Leave it out to keep whatever expiry is already there."`
	Discoverable      *bool  `json:"discoverable,omitempty" jsonschema:"for a domain or anyone grant: whether the file also turns up in their search results rather than only opening by link. Leave it out to keep what the grant already has; a new grant is by link only."`
	AllowAnyone       bool   `json:"allow_anyone,omitempty" jsonschema:"required to grant access to anyone with the link. Without it that grant is refused, because it puts the file within reach of everybody who has or guesses the link."`
	TransferOwnership bool   `json:"transfer_ownership,omitempty" jsonschema:"required for role: owner. It makes them the owner and demotes this account to a writer, and only the new owner can hand it back."`
	DryRun            bool   `json:"dry_run,omitempty" jsonschema:"report who can see it now and what this would change, and change nothing"`
}

// UnshareInput revokes one grant.
type UnshareInput struct {
	File         string `json:"file" jsonschema:"what to revoke access to: a file id, any Drive URL, a path from My Drive, or a shared drive as drive:Marketing to remove a member from it"`
	Principal    string `json:"principal,omitempty" jsonschema:"whose access to remove: an address, group:team@example.com, domain:example.com, or anyone. Pass this, permission_id or remove_link."`
	PermissionID string `json:"permission_id,omitempty" jsonschema:"the grant to remove, by the id list_permissions shows. Use this when the grant has no address to name it by."`
	RemoveLink   bool   `json:"remove_link,omitempty" jsonschema:"remove the anyone-with-the-link or domain-wide grant, which is what people mean by making a file private again"`
	DryRun       bool   `json:"dry_run,omitempty" jsonschema:"report what would be removed and change nothing"`
}

// registerAccess adds the access tools. list_permissions is a read and
// is always registered: seeing who can reach a file is not widening it,
// and a server that hides exposure is worse than one that cannot change
// it. share_file and unshare_file are the ones GDRIVE_SHARING=off
// removes.
func registerAccess(s *mcp.Server, d Deps) []string {
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_permissions",
		Description: "WHO CAN SEE this file, and how: every grant with the role it carries, when it expires, " +
			"whether it reaches people by link or by search, and where it came from. On a shared drive it lists " +
			"the members. Each row carries the permission id, which is what unshare_file takes when a grant has " +
			"no address to name it by. Call this before sharing anything: the exposure a file already has decides " +
			"whether a change matters.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in PermissionsInput) (*mcp.CallToolResult, any, error) {
		out, err := d.Service.ListPermissions(ctx, service.ListPermissionsInput{File: in.File})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(out), nil, nil
	})
	names := make([]string, 0, 3)
	names = append(names, "list_permissions")
	if d.Config.ReadOnly || d.Config.Sharing == "off" {
		return names
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "share_file",
		Description: "Give someone access to a file, or change the access they have. The result shows who could " +
			"see it before and who can see it after, because that is the part worth checking. " +
			"Granting to somebody who already has access changes their role rather than adding a second grant. " +
			"Accepted roles: " + strings.Join(service.Roles(), ", ") + ". " +
			"A link anyone can open needs allow_anyone: true, and handing over ownership needs " +
			"transfer_ownership: true; without those the call is refused. No email is sent unless notify is set. " +
			"What may actually be shared is decided by the organisation's own policy, which Google enforces: a " +
			"refusal comes back as [blocked] with Google's own words.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ShareInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.ShareFile(ctx, service.ShareFileInput{
			File: in.File, Principal: in.Principal, Role: in.Role, Notify: in.Notify,
			Message: in.Message, Expires: in.Expires, Discoverable: in.Discoverable,
			AllowAnyone: in.AllowAnyone, TransferOwnership: in.TransferOwnership, DryRun: in.DryRun,
		}))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "unshare_file",
		Description: "Take someone's access away, or kill the link that let anybody open it. The result shows who " +
			"can still see the file afterwards. A grant that comes from a shared drive or a folder above is " +
			"refused here with the place it came from named: it can only be removed there. An owner's access is " +
			"not revoked but transferred.",
		Annotations: idempotentWrite,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in UnshareInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.UnshareFile(ctx, service.UnshareFileInput{
			File: in.File, Principal: in.Principal, PermissionID: in.PermissionID,
			RemoveLink: in.RemoveLink, DryRun: in.DryRun,
		}))
	})

	return append(names, "share_file", "unshare_file")
}
