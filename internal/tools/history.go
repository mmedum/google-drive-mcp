package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/render"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// RevisionsInput selects a file's history.
type RevisionsInput struct {
	File string `json:"file" jsonschema:"the file whose versions to list: an id, any Drive URL, a path from My Drive, or a shared-drive path. A name or path matching more than one item is refused with the candidates listed."`
}

// ManageRevisionInput pins or unpins one revision.
type ManageRevisionInput struct {
	File     string `json:"file" jsonschema:"the file the revision belongs to: an id, any Drive URL, a path from My Drive, or a shared-drive path"`
	Revision string `json:"revision" jsonschema:"the revision id, from list_revisions"`
	Action   string `json:"action" jsonschema:"keep to pin it so Drive never discards it, or unkeep to let Drive discard it again"`
}

// ChangesInput asks for a start token or for the changes since one.
type ChangesInput struct {
	PageToken   string `json:"page_token,omitempty" jsonschema:"a token from an earlier call to this tool. Without one you get a starting point and nothing else, because the feed has no beginning: it only has a point to start from."`
	Drive       string `json:"drive,omitempty" jsonschema:"follow one shared drive instead of this account's own Drive, by name or id. A shared drive's feed has its own tokens, and a token from one feed means nothing in another."`
	MyDriveOnly bool   `json:"my_drive_only,omitempty" jsonschema:"only changes inside My Drive, leaving out shared drives and files shared with you"`
	Limit       int    `json:"limit,omitempty" jsonschema:"how many changes to return, default 100, maximum 1000"`
}

// registerHistory adds the version-history and changes-feed tools. The
// two listings are reads and are always registered; manage_revision is
// not, in read-only mode.
func registerHistory(s *mcp.Server, d Deps) []string {
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_revisions",
		Description: "A file's version history, newest first: when each version was made, by whom, how big it " +
			"was, and whether Drive is keeping it. download_file with revision fetches one version's content. " +
			"Drive discards a version 30 days after it stops being current unless it is pinned, so a history is " +
			"shorter than it looks; manage_revision pins one. " +
			"For a Google Doc, Sheet or Slides deck Drive says its own list can be incomplete — the editor " +
			"keeps more history than this API reports — and the result repeats that rather than hiding it.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in RevisionsInput) (*mcp.CallToolResult, any, error) {
		out, err := d.Service.ListRevisions(ctx, service.ListRevisionsInput{File: in.File})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(out), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_changes",
		Description: "What has changed in this Drive since a point in time. Call it with no page_token to get " +
			"that starting point, then call it again later with the token it gave you: each answer carries the " +
			"token for the next call. A file that was trashed and one that is gone for good are reported " +
			"differently, because only one of them can be undone. " +
			"The tokens are opaque and belong to one feed, so keep them for the session; search_files with " +
			"modified_after answers a similar question without any state at all.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ChangesInput) (*mcp.CallToolResult, any, error) {
		out, err := d.Service.ListChanges(ctx, service.ListChangesInput{
			PageToken: in.PageToken, Drive: in.Drive, MyDriveOnly: in.MyDriveOnly, Limit: in.Limit,
		})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(out), nil, nil
	})
	names := make([]string, 0, 3)
	names = append(names, "list_revisions", "list_changes")
	if d.Config.ReadOnly {
		return names
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "manage_revision",
		Description: "Pin a version of a file so Drive keeps it, or unpin one so Drive may discard it again. " +
			"Without a pin Drive discards a version 30 days after it stops being current, so this is what to " +
			"call before replacing content you may want back. Actions: " +
			strings.Join(service.RevisionActions(), ", ") + ".",
		Annotations: idempotentWrite,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ManageRevisionInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.ManageRevision(ctx, service.ManageRevisionInput{
			File: in.File, Revision: in.Revision, Action: in.Action,
		}))
	})
	return append(names, "manage_revision")
}
