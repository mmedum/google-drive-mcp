package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/model"
	"github.com/mmedum/google-drive-mcp/internal/render"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// DrivesInput tunes a shared-drive listing.
type DrivesInput struct {
	Name          string `json:"name,omitempty" jsonschema:"only drives whose name contains this"`
	IncludeHidden bool   `json:"include_hidden,omitempty" jsonschema:"also show drives hidden from the sidebar. They are left out by default, and hiding is a display choice: a hidden drive is as reachable as any other."`
}

// ManageDriveInput is one change to a shared drive.
type ManageDriveInput struct {
	Action       string          `json:"action" jsonschema:"what to do: create, rename, hide, unhide or restrict"`
	Drive        string          `json:"drive,omitempty" jsonschema:"the shared drive to change, by name or id. Not used by create; list_drives shows the ids."`
	Name         string          `json:"name,omitempty" jsonschema:"the name for a new drive, or the new name for a rename"`
	Restrictions map[string]bool `json:"restrictions,omitempty" jsonschema:"for restrict (and optionally create): the switches to set, as name to true or false. Names: members_only, domain_users_only, copy_requires_writer_permission, admin_managed, folder_sharing_requires_organizer."`
	DryRun       bool            `json:"dry_run,omitempty" jsonschema:"report what would happen and change nothing"`
}

// registerDrives adds the shared-drive tools. list_drives is a read and
// is always registered; manage_drive is not, in read-only mode.
func registerDrives(s *mcp.Server, d Deps) []string {
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_drives",
		Description: "The shared drives this account can see, with the id every other tool takes, what this " +
			"account may do in each, and which restrictions are in force. Shared drives are a Google Workspace " +
			"feature; a consumer account has none, and get_account says which this is. " +
			"A shared drive's id is also its root folder's id, so it can be passed anywhere a folder can, and " +
			"drive:Name works as a path everywhere in this server.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DrivesInput) (*mcp.CallToolResult, any, error) {
		out, err := d.Service.ListDrives(ctx, service.ListDrivesInput{Name: in.Name, IncludeHidden: in.IncludeHidden})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(out), nil, nil
	})
	if d.Config.ReadOnly {
		return []string{"list_drives"}
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "manage_drive",
		Description: "Make a shared drive, rename one, hide or unhide it, or change its restrictions. " +
			"Everything in a shared drive belongs to the organisation rather than to a person, which is the " +
			"difference from a folder in My Drive and does not come undone. " +
			"Members are not managed here: a member is a permission on the drive, so share_file and " +
			"unshare_file add and remove them with the drive as the target. " +
			"Restrictions: " + strings.Join(model.DriveRestrictionNames, ", ") + ". " +
			"Deleting a shared drive is a separate, gated tool.",
		Annotations: idempotentWrite,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ManageDriveInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.ManageDrive(ctx, service.ManageDriveInput{
			Action: in.Action, Drive: in.Drive, Name: in.Name,
			Restrictions: in.Restrictions, DryRun: in.DryRun,
		}))
	})
	return []string{"list_drives", "manage_drive"}
}
