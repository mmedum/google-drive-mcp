package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/service"
)

// ActivityInput selects what activity to report.
type ActivityInput struct {
	File      string   `json:"file" jsonschema:"the file or folder to report on: an id, any Drive URL, a path from My Drive, or a shared-drive path. A name or path matching more than one item is refused with the candidates listed."`
	Recursive bool     `json:"recursive,omitempty" jsonschema:"for a folder, report everything that happened inside it as well. Without this, a folder reports only what happened to the folder itself, which is usually nothing."`
	Actions   []string `json:"actions,omitempty" jsonschema:"only these kinds of action: create, edit, rename, move, delete, restore, permission, comment, label. Left out, every kind is reported."`
	Since     string   `json:"since,omitempty" jsonschema:"only activity since this moment, as a date like 2026-01-31 or a full RFC 3339 timestamp"`
	PageSize  int      `json:"page_size,omitempty" jsonschema:"how many events to return, default 25, maximum 100"`
	PageToken string   `json:"page_token,omitempty" jsonschema:"the token a previous call returned, to get the next page"`
}

// registerActivity adds list_activity, and only with GDRIVE_ACTIVITY=true
// — which is also what asks for the Drive Activity scope at login.
// Without it every call is refused for want of a scope, so registering
// the tool would be offering something that cannot work.
func registerActivity(s *mcp.Server, d Deps) []string {
	if !d.Config.Activity {
		return nil
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_activity",
		Description: "What happened to a file, or to everything in a folder: created, edited, renamed, " +
			"moved, shared, commented on, trashed, restored, and when. Filter by kind with actions (" +
			strings.Join(service.ActivityActions(), ", ") + ") and by time with since. " +
			"It says WHO only as 'you' or 'somebody else': Drive's activity feed identifies people by an " +
			"internal id and gives no name or address for them, so nothing here can. " +
			"list_changes is the other history tool and answers a different question — what changed across " +
			"the whole account since a token, for keeping in step rather than for looking back at one file.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ActivityInput) (*mcp.CallToolResult, any, error) {
		out, err := d.Service.ListActivity(ctx, service.ListActivityInput{
			File: in.File, Recursive: in.Recursive, Actions: in.Actions, Since: in.Since,
			PageSize: in.PageSize, PageToken: in.PageToken,
		})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(out), nil, nil
	})
	return []string{"list_activity"}
}
