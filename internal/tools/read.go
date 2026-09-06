package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/service"
)

// Every file argument spells out the accepted forms in its own schema
// description: a model reads one tool's schema at a time, and a struct
// tag cannot be composed from a constant.

// AccountInput takes nothing; get_account describes the signed-in account.
type AccountInput struct{}

// FileInput identifies one file.
type FileInput struct {
	File string `json:"file" jsonschema:"a file id, any Drive or Docs URL, the word root for My Drive, a path from My Drive like /Projects/2026/Budget.xlsx, or a shared-drive path like drive:Marketing/Campaigns. A name or path matching more than one item is refused with the candidates listed, so pass an id when you have one."`
	// IncludeLabels only does anything when the deployer enabled labels.
	IncludeLabels bool `json:"include_labels,omitempty" jsonschema:"also read the Workspace labels applied to the file; only available when the server was started with GDRIVE_LABELS=true"`
}

// SearchInput is the typed search surface.
type SearchInput struct {
	Name           string `json:"name,omitempty" jsonschema:"match the file name. Drive matches the BEGINNINGS OF WORDS, not any substring: \"Bud\" finds \"Budget 2026\" but \"udget\" finds nothing. Several words must appear next to each other in that order."`
	Text           string `json:"text,omitempty" jsonschema:"match whole words in the file's content and name. Wrap in double quotes for an exact phrase. Not a substring match."`
	Kind           string `json:"kind,omitempty" jsonschema:"limit to one kind: folder, doc, sheet, slides, form, drawing, pdf, image, video, audio, shortcut, office, or any (the default)"`
	MimeType       string `json:"mime_type,omitempty" jsonschema:"limit to one exact MIME type, for kinds the kind field does not name"`
	InFolder       string `json:"in_folder,omitempty" jsonschema:"only items DIRECTLY inside this folder; Drive cannot search a folder recursively, so this does not reach subfolders. Takes the same forms as file."`
	Drive          string `json:"drive,omitempty" jsonschema:"search one shared drive, by name or id. Without it the search covers My Drive, files shared with you, and every shared drive."`
	Scope          string `json:"scope,omitempty" jsonschema:"all (the default), my_drive, or shared_with_me"`
	Owner          string `json:"owner,omitempty" jsonschema:"me, or an email address"`
	Starred        bool   `json:"starred,omitempty" jsonschema:"only starred items"`
	Trashed        bool   `json:"trashed,omitempty" jsonschema:"search the trash instead of live files"`
	ModifiedAfter  string `json:"modified_after,omitempty" jsonschema:"only items modified after this date, as 2026-03-04 or 2026-03-04T09:00:00Z"`
	ModifiedBefore string `json:"modified_before,omitempty" jsonschema:"only items modified before this date"`
	CreatedAfter   string `json:"created_after,omitempty" jsonschema:"only items created after this date"`
	OrderBy        string `json:"order_by,omitempty" jsonschema:"modified (the default), created, name, recency, viewed or size"`
	Limit          int    `json:"limit,omitempty" jsonschema:"how many hits to return, default 25, maximum 200"`
	PageToken      string `json:"page_token,omitempty" jsonschema:"the page_token from a previous result, to see the next page"`
	Property       string `json:"property,omitempty" jsonschema:"match a custom file property, as \"key\" to find every file carrying that key whatever its value, or \"key=value\" to match the value too. These are the pairs update_file sets: one app tags files this way for another to find."`
	RawQuery       string `json:"raw_query,omitempty" jsonschema:"a Drive API v3 query expression, ANDed with the other fields, for syntax these fields do not cover"`
}

// FolderInput selects a folder to list.
type FolderInput struct {
	Folder         string `json:"folder,omitempty" jsonschema:"the folder to list. Give a file id, any Drive or Docs URL, the word root for My Drive, a path from My Drive like /Projects/2026/Budget.xlsx, or a shared-drive path like drive:Marketing/Campaigns. A name or path matching more than one item is refused with the candidates listed, so pass an id when you have one. Defaults to the root of My Drive."`
	Kind           string `json:"kind,omitempty" jsonschema:"only show one kind: folder, doc, sheet, slides, form, drawing, pdf, image, video, audio, shortcut, office, or any (the default)"`
	PageSize       int    `json:"page_size,omitempty" jsonschema:"how many items per page, default 100, maximum 200"`
	PageToken      string `json:"page_token,omitempty" jsonschema:"the page_token from a previous result, to see the next page"`
	Recursive      bool   `json:"recursive,omitempty" jsonschema:"walk subfolders and return a tree instead of one page. Costs one listing per folder, so it is bounded by max_depth and max_items and says where it stopped."`
	MaxDepth       int    `json:"max_depth,omitempty" jsonschema:"how many folder levels a recursive walk enters, default 3, maximum 10"`
	MaxItems       int    `json:"max_items,omitempty" jsonschema:"how many items a recursive walk returns, default 200, maximum 2000"`
	IncludeTrashed bool   `json:"include_trashed,omitempty" jsonschema:"also show items in the trash; they are left out by default"`
}

func registerRead(s *mcp.Server, d Deps) []string {
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_account",
		Description: "Who is signed in to Google, how much storage their Drive uses, whether this account can create " +
			"shared drives and which ones it can see, and what this server will let you do on their behalf: whether it " +
			"is read-only, whether the sharing and destructive tools are registered, and whether files can be " +
			"downloaded or uploaded at all. Call it once when something is refused and you want to know whether it is " +
			"the account, the organisation or this server's configuration saying no. " +
			"It describes the account, not any file: get_file does that.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ AccountInput) (*mcp.CallToolResult, any, error) {
		out, err := d.Service.GetAccount(ctx)
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(out), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_file",
		Description: "Everything about one file, folder or shortcut: what kind of thing it is, WHERE IT LIVES, its " +
			"link, size, when it changed and who changed it, who owns it, WHO CAN SEE IT, and what this account may " +
			"do with it. Cheap: one or two calls. Call it first when handed an id, a URL or a path, because names in " +
			"Drive are not unique and the folder a file sits in is part of what it means. " +
			"On a shortcut it describes the shortcut itself and names its target. " +
			"For one item; list_folder lists what is inside a folder, and search_files finds items you cannot name.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in FileInput) (*mcp.CallToolResult, any, error) {
		out, err := d.Service.GetFile(ctx, service.GetFileInput{File: in.File, IncludeLabels: in.IncludeLabels})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(out), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "search_files",
		Description: "Find files across My Drive, files shared with you, and every shared drive, by name, content, " +
			"kind, folder, owner, star or date. Each hit shows its kind, name, id, folder and last change. " +
			"IMPORTANT: Drive does not do substring search. `name` matches the beginnings of words and `text` matches " +
			"whole words, so \"udget\" will never find \"Budget\". Use list_folder when you know where something is: " +
			"a search costs twenty times what a read does. `in_folder` reaches direct children only.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, any, error) {
		out, err := d.Service.Search(ctx, service.SearchInput{
			Name: in.Name, Text: in.Text, Kind: in.Kind, MimeType: in.MimeType,
			InFolder: in.InFolder, Drive: in.Drive, Scope: in.Scope, Owner: in.Owner,
			Starred: in.Starred, Trashed: in.Trashed,
			ModifiedAfter: in.ModifiedAfter, ModifiedBefore: in.ModifiedBefore, CreatedAfter: in.CreatedAfter,
			OrderBy: in.OrderBy, Property: in.Property, Limit: in.Limit, PageToken: in.PageToken,
			RawQuery: in.RawQuery,
		})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(out), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_folder",
		Description: "List what is in a folder: one page, folders first and then names in natural order, or with " +
			"recursive: true a tree of the folders below it. Prefer this over search_files when you know where to " +
			"look. Items in the trash are left out unless you ask for them. A recursive walk is bounded by max_depth " +
			"and max_items and names the folders it did not enter, so a tree that stops early says so rather than " +
			"looking complete.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in FolderInput) (*mcp.CallToolResult, any, error) {
		out, err := d.Service.ListFolder(ctx, service.ListFolderInput{
			Folder: in.Folder, Kind: in.Kind, PageSize: in.PageSize, PageToken: in.PageToken,
			Recursive: in.Recursive, MaxDepth: in.MaxDepth, MaxItems: in.MaxItems,
			IncludeTrashed: in.IncludeTrashed,
		})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(out), nil, nil
	})

	return []string{"get_account", "get_file", "search_files", "list_folder"}
}
