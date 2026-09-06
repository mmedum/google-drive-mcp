package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/render"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// LabelsInput selects a page of label definitions.
type LabelsInput struct {
	Appliable bool   `json:"appliable,omitempty" jsonschema:"only labels you may actually put on a file. Off by default, because a label you can read but not apply still explains a value you can see on a file."`
	PageSize  int    `json:"page_size,omitempty" jsonschema:"how many labels to return, default 50, maximum 200"`
	PageToken string `json:"page_token,omitempty" jsonschema:"the token a previous call returned, to get the next page"`
}

// ManageLabelsInput is one change to one label on one file.
type ManageLabelsInput struct {
	File   string   `json:"file" jsonschema:"the file to change: an id, any Drive URL, a path from My Drive, or a shared-drive path. A name or path matching more than one item is refused with the candidates listed."`
	Label  string   `json:"label" jsonschema:"the label to change, by the id list_labels shows"`
	Action string   `json:"action" jsonschema:"apply to put the label on the file, set_field to give one of its fields a value, unset_field to clear one, or remove to take the label off"`
	Field  string   `json:"field,omitempty" jsonschema:"with set_field and unset_field, the field to change, by the id list_labels shows"`
	Values []string `json:"values,omitempty" jsonschema:"with set_field, the whole new value of the field. It REPLACES what was there rather than adding to it. A selection field takes choice ids, a date field YYYY-MM-DD, a user field email addresses."`
}

// registerLabels adds the two label tools. They are registered only with
// GDRIVE_LABELS=true, which is also what asks for the label scopes at
// login: without them the Drive Labels API refuses every call, so
// registering the tools would be offering a listing that cannot work.
//
// Both go together on purpose. Applying a label needs only the Drive
// scope, but it needs a label id and a field id, and those are generated
// strings that live in the definitions. manage_labels without list_labels
// would be a tool whose arguments nobody can find out.
func registerLabels(s *mcp.Server, d Deps) []string {
	if !d.Config.Labels {
		return nil
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_labels",
		Description: "The Workspace labels this account can use, with the fields on each one and the values " +
			"those fields take. Labels are metadata an organisation defines centrally — a status, a " +
			"retention class, a reviewer — and an administrator publishes them; this server can put them on " +
			"files but cannot create or change a definition. Read this before manage_labels: a label and its " +
			"fields are addressed by generated ids that appear nowhere else.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in LabelsInput) (*mcp.CallToolResult, any, error) {
		out, err := d.Service.ListLabels(ctx, service.ListLabelsInput{
			Appliable: in.Appliable, PageSize: in.PageSize, PageToken: in.PageToken,
		})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(out), nil, nil
	})

	if d.Config.ReadOnly {
		return []string{"list_labels"}
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "manage_labels",
		Description: "Put a Workspace label on a file, set or clear one of its fields, or take it off. " +
			"Actions: " + strings.Join(service.LabelActions(), ", ") + ". One label and one field per call. " +
			"A label is metadata, not access: it changes nothing about who can open the file. But an " +
			"organisation can attach rules to a label — retention, or a data classification — so a label " +
			"may mean the file is kept, or handled, differently. Setting a field REPLACES its value. " +
			"The result is the file as it stands afterwards, with every label on it.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ManageLabelsInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.ManageLabels(ctx, service.ManageLabelsInput{
			File: in.File, Label: in.Label, Action: in.Action, Field: in.Field, Values: in.Values,
		}))
	})
	return []string{"list_labels", "manage_labels"}
}
