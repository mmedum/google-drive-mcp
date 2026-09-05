package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/render"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// DeleteFileInput names one item to remove for good.
type DeleteFileInput struct {
	File    string `json:"file" jsonschema:"the item to destroy: an id, any Drive URL, a path from My Drive, or a shared-drive path. A shortcut is deleted itself, not what it points at."`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"required to actually delete. Without it the call is refused, because this cannot be undone by anyone, including a Workspace administrator."`
	DryRun  bool   `json:"dry_run,omitempty" jsonschema:"report what would be destroyed and destroy nothing"`
}

// EmptyTrashInput names whose trash to empty.
type EmptyTrashInput struct {
	Drive   string `json:"drive,omitempty" jsonschema:"empty one shared drive's trash instead of this account's own, by name or id"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"required to actually empty it. Without it the call is refused."`
	DryRun  bool   `json:"dry_run,omitempty" jsonschema:"report how much is in there and destroy nothing"`
}

// DeleteDriveInput names a shared drive to remove.
type DeleteDriveInput struct {
	Drive   string `json:"drive" jsonschema:"the shared drive to destroy, by name or id"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"required to actually delete it. Without it the call is refused."`
	DryRun  bool   `json:"dry_run,omitempty" jsonschema:"report what would happen and destroy nothing"`
}

// DeleteRevisionInput names one revision to remove.
type DeleteRevisionInput struct {
	File     string `json:"file" jsonschema:"the file the revision belongs to: an id, any Drive URL, a path from My Drive, or a shared-drive path"`
	Revision string `json:"revision" jsonschema:"the revision id, from list_revisions"`
	Confirm  bool   `json:"confirm,omitempty" jsonschema:"required to actually delete it. Without it the call is refused."`
	DryRun   bool   `json:"dry_run,omitempty" jsonschema:"report what would be destroyed and destroy nothing"`
}

// registerDestructive adds the tools that destroy with no way back. They
// exist only when the deployer set GDRIVE_ENABLE_DESTRUCTIVE=true: the
// specification treats annotations as untrusted hints, so the gate is
// the registration itself and not a flag on the tool. Each of them also
// needs confirm: true on the call, because a registered tool is one a
// model will reach for eventually.
func registerDestructive(s *mcp.Server, d Deps) []string {
	if d.Config.ReadOnly || !d.Config.EnableDestructive {
		return nil
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "delete_file",
		Description: "Destroy a file or folder permanently, skipping the trash. THERE IS NO UNDO: not in Drive, " +
			"not for an administrator, not through support. Deleting a folder takes everything inside it that " +
			"this account owns. Use trash_file instead unless permanence is the point; a trashed item can be " +
			"restored, and Drive clears the trash after 30 days anyway. Needs confirm: true.",
		Annotations: destructive,
		Meta:        requiresUserInteraction(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteFileInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.DeleteFile(ctx, service.DeleteFileInput{
			File: in.File, Confirm: in.Confirm, DryRun: in.DryRun,
		}))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "empty_trash",
		Description: "Destroy everything in the trash permanently. THERE IS NO UNDO, and this is the one " +
			"destructive call that does not name what it removes, so run it with dry_run first: that says how " +
			"much is in there. Anything in the trash can still be brought back with restore_file until this " +
			"runs. Needs confirm: true.",
		Annotations: destructive,
		Meta:        requiresUserInteraction(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EmptyTrashInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.EmptyTrash(ctx, service.EmptyTrashInput{
			Drive: in.Drive, Confirm: in.Confirm, DryRun: in.DryRun,
		}))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "delete_drive",
		Description: "Destroy a shared drive permanently. THERE IS NO UNDO. Drive refuses it while the drive " +
			"still holds anything untrashed, and this server never asks for the override that would take the " +
			"contents with it: trash what is in there first, then empty_trash. Needs confirm: true.",
		Annotations: destructive,
		Meta:        requiresUserInteraction(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteDriveInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.DeleteDrive(ctx, service.DeleteDriveInput{
			Drive: in.Drive, Confirm: in.Confirm, DryRun: in.DryRun,
		}))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "delete_revision",
		Description: "Destroy one old version of a file's content permanently. THERE IS NO UNDO. The file's " +
			"current content is untouched. Drive allows this only for a file with bytes of its own, never for a " +
			"Google Doc, Sheet or Slides deck, and never for the version the file is at now. Needs confirm: true.",
		Annotations: destructive,
		Meta:        requiresUserInteraction(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DeleteRevisionInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.DeleteRevision(ctx, service.DeleteRevisionInput{
			File: in.File, Revision: in.Revision, Confirm: in.Confirm, DryRun: in.DryRun,
		}))
	})

	return []string{"delete_file", "empty_trash", "delete_drive", "delete_revision"}
}

// requiresUserInteraction marks a tool that a client should not run
// without a person seeing it. It is a hint and nothing more — the gate
// that actually holds is that these tools are not registered at all
// unless the deployer asked for them — but a client that honours it
// gives the person the look at the call that the hint is for.
func requiresUserInteraction() mcp.Meta {
	return mcp.Meta{"anthropic/requiresUserInteraction": true}
}
