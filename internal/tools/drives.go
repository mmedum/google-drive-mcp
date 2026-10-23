package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
	"github.com/mmedum/google-drive-mcp/v3/internal/model"
	"github.com/mmedum/google-drive-mcp/v3/internal/render"
	"github.com/mmedum/google-drive-mcp/v3/internal/service"
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

// LiftDriveRestrictionInput turns off one restriction that keeps a
// shared drive's content inside the organization.
type LiftDriveRestrictionInput struct {
	Drive       string `json:"drive" jsonschema:"the shared drive, by name or id"`
	DriveName   string `json:"drive_name" jsonschema:"the shared drive's name exactly as list_drives shows it. Required: it says in the call itself which drive is opened up, and a name that is not the drive's is refused with [stale]."`
	Restriction string `json:"restriction" jsonschema:"the restriction to turn off: domain_users_only"`
	Confirm     bool   `json:"confirm,omitempty" jsonschema:"required to actually turn it off. Without it the call is refused."`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"report what would change, and change nothing"`
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
			"Everything in a shared drive belongs to the organization rather than to a person, which is the " +
			"difference from a folder in My Drive and does not come undone. " +
			"Members are not managed here: a member is a permission on the drive, so share_file and " +
			"unshare_file add and remove them with the drive as the target. " +
			"Restrictions: " + strings.Join(model.DriveRestrictionNames, ", ") + ". " +
			"With GDRIVE_SHARING=off a restriction may be turned on but not off. Turning domain_users_only off " +
			"lets the drive's content reach people outside the organization, so it is refused here with " +
			"[blocked]: lift_drive_restriction does it, on a server started with GDRIVE_ENABLE_DESTRUCTIVE=true. " +
			"Deleting a shared drive is a separate, gated tool.",
		Annotations: idempotentWrite,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ManageDriveInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.ManageDrive(ctx, service.ManageDriveInput{
			Action: in.Action, Drive: in.Drive, Name: in.Name,
			Restrictions: in.Restrictions, DryRun: in.DryRun,
		}))
	})
	if d.Config.Sharing == config.SharingOff || !d.Config.EnableDestructive {
		return []string{"list_drives", "manage_drive"}
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "lift_drive_restriction",
		Description: "Turn off a shared drive restriction that keeps what is in the drive inside the " +
			"organization. Restrictions: " + strings.Join(service.LiftableRestrictions(), ", ") + ". With " +
			"domain_users_only off, the drive and what is in it can be shared with people outside the " +
			"organization. manage_drive with action restrict turns it back on. Needs confirm: true, and " +
			"drive_name: the drive's name as list_drives shows it.",
		Annotations: widening,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in LiftDriveRestrictionInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.LiftDriveRestriction(ctx, service.LiftDriveRestrictionInput{
			Drive: in.Drive, DriveName: in.DriveName, Restriction: in.Restriction, Confirm: in.Confirm, DryRun: in.DryRun,
		}))
	})
	return []string{"list_drives", "manage_drive", "lift_drive_restriction"}
}
