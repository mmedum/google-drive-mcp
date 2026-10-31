package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/v3/internal/render"
	"github.com/mmedum/google-drive-mcp/v3/internal/service"
)

// Every file argument spells out the accepted forms in its own schema
// description: a model reads one tool's schema at a time, and a struct
// tag cannot be composed from a constant.

// PermissionsInput selects what to describe.
type PermissionsInput struct {
	File string `json:"file" jsonschema:"the item to describe: a file id, any Drive URL, the word root for My Drive, a path from My Drive like /Projects/2026, or a shared drive as drive:Marketing, which lists that drive's members. A name or path matching more than one item is refused with the candidates listed."`
}

// ShareInput is one grant to make or change, inside the organization.
type ShareInput struct {
	File      string `json:"file" jsonschema:"what to share: a file id, any Drive URL, a path from My Drive, or a shared drive as drive:Marketing to add a member to it. A shortcut is shared itself, not what it points at."`
	Principal string `json:"principal" jsonschema:"who gets access: an address like someone@example.com, or group:team@example.com for a Google group, inside this account's organization"`
	Role      string `json:"role" jsonschema:"what they may do: reader, commenter, writer, or file_organizer and organizer inside a shared drive"`
	Notify    bool   `json:"notify,omitempty" jsonschema:"send Google's notification email. Off by default: a tool call is not a reason to put mail in somebody's inbox."`
	Message   string `json:"message,omitempty" jsonschema:"a line to include in that email; only used when notify is true"`
	Expires   string `json:"expires,omitempty" jsonschema:"when the grant should end, as a date like 2026-12-01 or a duration like 30d, or never to remove an expiry a grant already has. Drive's limit is a year. Leave it out to keep whatever expiry is already there."`
	DryRun    bool   `json:"dry_run,omitempty" jsonschema:"report who can see it now and what this would change, and change nothing"`
}

// ShareOutsideInput is one grant to make or change, outside the
// organization.
type ShareOutsideInput struct {
	File      string `json:"file" jsonschema:"what to share: a file id, any Drive URL, a path from My Drive, or a shared drive as drive:Marketing to add a member to it. A shortcut is shared itself, not what it points at."`
	FileName  string `json:"file_name" jsonschema:"the name of the file, folder or shared drive exactly as get_file shows it. Required: it says in the call itself what is opened up, and a name that is not the item's is refused with [stale]."`
	Principal string `json:"principal" jsonschema:"who gets access: an address like someone@example.org, or group:team@example.org for a Google group, outside this account's organization"`
	Role      string `json:"role" jsonschema:"what they may do: reader, commenter, writer, or file_organizer and organizer inside a shared drive"`
	Notify    bool   `json:"notify,omitempty" jsonschema:"send Google's notification email. Off by default: a tool call is not a reason to put mail in somebody's inbox."`
	Message   string `json:"message,omitempty" jsonschema:"a line to include in that email; only used when notify is true"`
	Expires   string `json:"expires,omitempty" jsonschema:"when the grant should end, as a date like 2026-12-01 or a duration like 30d, or never to remove an expiry a grant already has. Drive's limit is a year. Leave it out to keep whatever expiry is already there."`
	Confirm   bool   `json:"confirm,omitempty" jsonschema:"required to actually share. Without it the call is refused."`
	DryRun    bool   `json:"dry_run,omitempty" jsonschema:"report who can see it now and what this would change, and change nothing"`
}

// ShareByLinkInput opens a file to anyone with the link, or to a domain.
type ShareByLinkInput struct {
	File         string `json:"file" jsonschema:"what to open: a file id, any Drive URL, or a path from My Drive. A shortcut is shared itself, not what it points at."`
	FileName     string `json:"file_name" jsonschema:"the name of the file or folder exactly as get_file shows it. Required: it says in the call itself what is opened up, and a name that is not the item's is refused with [stale]."`
	Principal    string `json:"principal" jsonschema:"anyone, for a link that needs no sign-in, or domain:example.com for everyone in an organization"`
	Role         string `json:"role" jsonschema:"what they may do: reader, commenter or writer"`
	Discoverable *bool  `json:"discoverable,omitempty" jsonschema:"whether the file also turns up in their search results rather than only opening by link. Leave it out to keep what the grant already has; a new grant is by link only."`
	Confirm      bool   `json:"confirm,omitempty" jsonschema:"required to actually share. Without it the call is refused."`
	DryRun       bool   `json:"dry_run,omitempty" jsonschema:"report who can see it now and what this would change, and change nothing"`
}

// TransferOwnershipInput hands a file to a new owner.
type TransferOwnershipInput struct {
	File     string `json:"file" jsonschema:"the My Drive file or folder to hand over: a file id, any Drive URL, or a path from My Drive"`
	FileName string `json:"file_name" jsonschema:"the name of the file or folder exactly as get_file shows it. Required: it says in the call itself what is handed over, and a name that is not the item's is refused with [stale]."`
	To       string `json:"to" jsonschema:"the address of the person who becomes the owner"`
	Message  string `json:"message,omitempty" jsonschema:"a line to include in the email Google always sends for a transfer"`
	Confirm  bool   `json:"confirm,omitempty" jsonschema:"required to actually transfer. Without it the call is refused."`
	DryRun   bool   `json:"dry_run,omitempty" jsonschema:"report what would change, and change nothing"`
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
// it. GDRIVE_SHARING=off removes the rest.
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
	names := make([]string, 0, 6)
	names = append(names, "list_permissions")
	if d.Config.ReadOnly || d.Config.Sharing == "off" {
		return names
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "share_file",
		Description: "Give a named person or group inside this account's organization access to a file, or " +
			"change the access they have. The result shows who could see it before and who can see it after, " +
			"because that is the part worth checking. Granting to somebody who already has access changes " +
			"their role rather than adding a second grant. Accepted roles: " + strings.Join(service.GrantRoles(), ", ") +
			". No email is sent unless notify is set. A grant to a group reaches " + service.GroupReach + ". " +
			"A link for anyone, a whole domain, someone outside the " +
			"organization and a new owner are refused here with [blocked]: share_by_link, " +
			"share_outside_organization and transfer_ownership do those, and exist only on a server started with " +
			"GDRIVE_ENABLE_DESTRUCTIVE=true. Outside means an address whose domain is not this account's; for a " +
			"personal Google account that is every address. What may actually be shared is decided by the " +
			"organization's own policy, which Google enforces: a refusal comes back as [blocked] with Google's " +
			"own words.",
		Annotations: changing,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ShareInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.ShareFile(ctx, service.ShareFileInput{
			File: in.File, Principal: in.Principal, Role: in.Role, Notify: in.Notify,
			Message: in.Message, Expires: in.Expires, DryRun: in.DryRun,
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

	names = append(names, "share_file", "unshare_file")
	if !d.Config.EnableDestructive {
		return names
	}
	return append(names, registerWideShares(s, d)...)
}

// registerWideShares adds the shares that reach past the people inside
// the organization, or hand a file over (§4a). Each is its own tool
// because a client remembers an approval per tool, never per argument,
// and each exists only on a server started with
// GDRIVE_ENABLE_DESTRUCTIVE=true, with confirm: true on the call.
func registerWideShares(s *mcp.Server, d Deps) []string {
	mcp.AddTool(s, &mcp.Tool{
		Name: "share_by_link",
		Description: "Open a file to anyone with the link, or to everyone in a domain, or change the access such " +
			"a grant gives. ANYONE WITH THE LINK means everybody who has or guesses it, with no sign-in. The result " +
			"shows who could see the file before and who can after. discoverable also makes it turn up in their " +
			"search. unshare_file with remove_link takes it back, but not what was read or copied meanwhile. " +
			"Needs confirm: true, and file_name: the item's name as get_file shows it.",
		Annotations: widening,
		Meta:        requiresUserInteraction(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ShareByLinkInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.ShareByLink(ctx, service.ShareByLinkInput{
			File: in.File, FileName: in.FileName, Principal: in.Principal, Role: in.Role, Discoverable: in.Discoverable,
			Confirm: in.Confirm, DryRun: in.DryRun,
		}))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "share_outside_organization",
		Description: "Give a person or group outside this account's organization access to a file, or change " +
			"the access they have. Outside means an address whose domain is not this account's; for a personal " +
			"Google account that is every address. The result shows who could see the file before and who can " +
			"after. Accepted roles: " + strings.Join(service.GrantRoles(), ", ") + ". No email is sent unless " +
			"notify is set. Needs confirm: true, and file_name: the item's name as get_file shows it. What may " +
			"actually be shared is decided by the organization's own policy, which Google enforces.",
		Annotations: widening,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ShareOutsideInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.ShareOutside(ctx, service.ShareOutsideInput{
			File: in.File, FileName: in.FileName, Principal: in.Principal, Role: in.Role, Notify: in.Notify,
			Message: in.Message, Expires: in.Expires, Confirm: in.Confirm, DryRun: in.DryRun,
		}))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "transfer_ownership",
		Description: "Make another person the owner of a file or folder in My Drive. This account becomes a " +
			"writer on it and CANNOT TAKE IT BACK: only the new owner can hand it on. Google always emails the new " +
			"owner. A shared drive owns everything in it, so a file there has no owner to transfer. Needs " +
			"confirm: true, and file_name: the item's name as get_file shows it.",
		Annotations: widening,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in TransferOwnershipInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.TransferOwnership(ctx, service.TransferOwnershipInput{
			File: in.File, FileName: in.FileName, To: in.To, Message: in.Message, Confirm: in.Confirm, DryRun: in.DryRun,
		}))
	})
	return []string{"share_by_link", "share_outside_organization", "transfer_ownership"}
}
