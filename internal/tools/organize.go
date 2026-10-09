package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/v2/internal/render"
	"github.com/mmedum/google-drive-mcp/v2/internal/service"
)

// The file argument's accepted forms are spelled out in each schema: a
// model reads one tool's schema at a time, and a Go struct tag cannot be
// composed from a constant.

// CreateFileInput describes a file to create.
type CreateFileInput struct {
	Name           string `json:"name" jsonschema:"the name for the new file"`
	Parent         string `json:"parent,omitempty" jsonschema:"the folder to create it in, as an id, a Drive URL, a path from My Drive like /Projects/2026, or a shared-drive path like drive:Marketing/Campaigns. Defaults to the root of My Drive."`
	Kind           string `json:"kind,omitempty" jsonschema:"make an empty Google file of this kind: doc, sheet, slides, drawing or form. Pass this or content, not both."`
	Content        string `json:"content,omitempty" jsonschema:"the text of a new file, written inline. Up to 5 MB; larger files go through upload_file. Pass this or kind, not both."`
	MimeType       string `json:"mime_type,omitempty" jsonschema:"what the inline content is, default text/plain"`
	ConvertTo      string `json:"convert_to,omitempty" jsonschema:"ask Google to import the content as one of its own kinds: doc, sheet, slides or drawing. Markdown becomes a formatted Google Doc this way."`
	Description    string `json:"description,omitempty" jsonschema:"a description stored on the file"`
	Starred        bool   `json:"starred,omitempty" jsonschema:"star it"`
	AllowDuplicate bool   `json:"allow_duplicate,omitempty" jsonschema:"create it even though the folder already holds something of that name. Drive allows duplicates; this server refuses them unless you say so, because a retried call is the usual way they appear."`
}

// UploadFileInput describes a local file to send to Drive.
type UploadFileInput struct {
	LocalPath                 string `json:"local_path" jsonschema:"the file to send, inside the server's local directory. Either a bare name in that directory or an absolute path inside it; anything outside is refused."`
	Name                      string `json:"name,omitempty" jsonschema:"the name it gets in Drive, default the local file's own name"`
	Parent                    string `json:"parent,omitempty" jsonschema:"the folder to put it in, as an id, a Drive URL, a path from My Drive, or a shared-drive path like drive:Marketing/Campaigns. Defaults to the root of My Drive."`
	MimeType                  string `json:"mime_type,omitempty" jsonschema:"what the file is; by default this is worked out from the extension and then from the bytes"`
	ConvertTo                 string `json:"convert_to,omitempty" jsonschema:"ask Google to import it as one of its own kinds: doc, sheet, slides or drawing. Importing a PDF or a photograph as a doc reads the text out of it."`
	OCRLanguage               string `json:"ocr_language,omitempty" jsonschema:"an ISO 639-1 language code hinting what language the text in a scan or photograph is, for convert_to"`
	UseContentAsIndexableText bool   `json:"use_content_as_indexable_text,omitempty" jsonschema:"index the uploaded bytes as the file's searchable text, so Drive search can find it by its words. For a type Drive does not read on its own; it does nothing for a type it already indexes."`
	Description               string `json:"description,omitempty" jsonschema:"a description stored on the file"`
	AllowDuplicate            bool   `json:"allow_duplicate,omitempty" jsonschema:"upload it even though the folder already holds something of that name"`
}

// UpdateContentInput replaces a file's bytes.
type UpdateContentInput struct {
	File                 string `json:"file" jsonschema:"the file whose content to replace: an id, a Drive URL, a path from My Drive, or a shared-drive path. A name or path matching more than one item is refused with the candidates listed."`
	Content              string `json:"content,omitempty" jsonschema:"the new text, written inline. Pass this or local_path."`
	LocalPath            string `json:"local_path,omitempty" jsonschema:"a file inside the server's local directory to take the new content from. Pass this or content."`
	MimeType             string `json:"mime_type,omitempty" jsonschema:"what the new content is; by default the file keeps the type it had"`
	KeepPreviousRevision bool   `json:"keep_previous_revision,omitempty" jsonschema:"pin the version being replaced so Drive keeps it. Without this Drive may discard it after 30 days, or sooner once the file has 100 revisions."`
	ExpectHeadRevision   string `json:"expect_head_revision,omitempty" jsonschema:"the head revision id you last saw. The write is refused if the file has changed since. Drive has no true preconditions, so this is a check, not a lock."`
}

// CreateFolderInput describes a folder to create.
type CreateFolderInput struct {
	Name           string `json:"name" jsonschema:"the name for the new folder"`
	Parent         string `json:"parent,omitempty" jsonschema:"the folder to create it in, as an id, a Drive URL, a path from My Drive, or a shared-drive path like drive:Marketing/Campaigns. Defaults to the root of My Drive."`
	Description    string `json:"description,omitempty" jsonschema:"a description stored on the folder"`
	Color          string `json:"color,omitempty" jsonschema:"an RGB hex color like #4986e7. Drive keeps a palette and uses the nearest color in it."`
	AllowDuplicate bool   `json:"allow_duplicate,omitempty" jsonschema:"create it even though a folder of that name is already there"`
}

// UpdateFileMetaInput is a metadata patch; only the fields passed change.
type UpdateFileMetaInput struct {
	File                         string            `json:"file" jsonschema:"the item to change: an id, a Drive URL, a path from My Drive, or a shared-drive path. A name or path matching more than one item is refused with the candidates listed. A shortcut is changed itself, not what it points at."`
	Name                         string            `json:"name,omitempty" jsonschema:"a new name"`
	Description                  *string           `json:"description,omitempty" jsonschema:"a new description; an empty string clears it"`
	Starred                      *bool             `json:"starred,omitempty" jsonschema:"star or unstar it"`
	Color                        string            `json:"color,omitempty" jsonschema:"for a folder: an RGB hex color like #4986e7"`
	Properties                   map[string]string `json:"properties,omitempty" jsonschema:"custom key-value pairs stored on the file and visible to every app. An empty value deletes that key."`
	CopyRequiresWriterPermission *bool             `json:"copy_requires_writer_permission,omitempty" jsonschema:"legacy: Drive's old download switch, kept for compatibility. true stops viewers and commenters copying, printing or downloading it; false lifts every download restriction set on the file, editors' included. Use restrict_download instead; the two are refused together."`
	WritersCanShare              *bool             `json:"writers_can_share,omitempty" jsonschema:"false stops editors changing who else can see it"`
	RestrictDownload             string            `json:"restrict_download,omitempty" jsonschema:"who cannot download, print or copy it: none, viewers (viewers and commenters), or editors (editors as well). Only its owner, or an organizer of its shared drive, can change it. A shared drive or an organization rule can restrict more than this sets; the result shows what is in effect."`
	LimitedAccess                *bool             `json:"limited_access,omitempty" jsonschema:"for a folder: true lets only the people added to it directly open it, and those who reach the folder above see it without opening it. false lets everyone who reaches the folder above open it and everything inside it, which is put to the person first when the client can ask."`
	Viewed                       bool              `json:"viewed,omitempty" jsonschema:"mark the file as opened by you just now, which is what puts it at the top of Drive's Recent view. Only true does anything: Drive stores a timestamp and offers no way to say a file was never opened."`
}

// MoveFileInput moves one item, or several, somewhere else.
type MoveFileInput struct {
	File   string   `json:"file,omitempty" jsonschema:"the item to move: an id, a Drive URL, a path from My Drive, or a shared-drive path. A shortcut is moved itself, not what it points at. Pass this or files."`
	Files  []string `json:"files,omitempty" jsonschema:"several items to move to the one destination, at most 50, each as file takes it. Pass this or file, not both. Each item gets its own outcome: moved, unchanged, refused with the reason, or failed with Drive's error. A failure does not undo the moves before it."`
	To     string   `json:"to" jsonschema:"where it goes: a folder id, a Drive URL, the word root for My Drive, a path from My Drive like /Projects/2026, or a shared drive as drive:Marketing"`
	DryRun bool     `json:"dry_run,omitempty" jsonschema:"report what would happen to each item, who could reach it before and after included, and change nothing"`
}

// CopyFileInput describes a copy.
type CopyFileInput struct {
	File                string `json:"file" jsonschema:"the file or folder to copy: an id, a Drive URL, a path from My Drive, or a shared-drive path. A folder needs recursive: true."`
	Name                string `json:"name,omitempty" jsonschema:"the copy's name, default \"Copy of <the original>\" as Drive itself does"`
	To                  string `json:"to,omitempty" jsonschema:"the folder for the copy, default the folder the original is in"`
	ConvertTo           string `json:"convert_to,omitempty" jsonschema:"ask Google to import the copy as one of its own kinds: doc, sheet, slides or drawing. This is how a PDF or a scanned image becomes a document with readable text, which read_file can then return."`
	OCRLanguage         string `json:"ocr_language,omitempty" jsonschema:"an ISO 639-1 language code hinting what language the text in a scan is, for convert_to"`
	KeepRevisionForever bool   `json:"keep_revision_forever,omitempty" jsonschema:"pin the copy's first revision so Drive keeps it"`
	CopyComments        bool   `json:"copy_comments,omitempty" jsonschema:"ask Drive to bring the comment threads along, and check the copy afterwards. Drive's answer says nothing either way, and it does not always do it: a live run found a Google Doc's threads came across and an uploaded CSV's did not, on the same run with the same argument. list_comments on the copy is what tells you. Off by default: the threads are other people's words, and the copy may end up somewhere they cannot see."`
	AllowDuplicate      bool   `json:"allow_duplicate,omitempty" jsonschema:"copy it even though the destination folder already holds something of that name"`
	Recursive           bool   `json:"recursive,omitempty" jsonschema:"required to copy a folder: Drive has no call for it, so it is one listing per folder and one write per item inside"`
	MaxItems            int    `json:"max_items,omitempty" jsonschema:"how many items a recursive copy may write, default 200, ceiling 2000. A tree larger than this is refused before anything is copied, rather than copied halfway."`
	DryRun              bool   `json:"dry_run,omitempty" jsonschema:"report what would be copied, and for a folder how big the tree is, and copy nothing"`
}

// CreateShortcutInput describes a shortcut to create.
type CreateShortcutInput struct {
	Target         string `json:"target" jsonschema:"what the shortcut points at: an id, a Drive URL, a path from My Drive, or a shared-drive path"`
	Parent         string `json:"parent,omitempty" jsonschema:"the folder to put the shortcut in. Defaults to the root of My Drive."`
	Name           string `json:"name,omitempty" jsonschema:"the shortcut's name, default the target's own name"`
	AllowDuplicate bool   `json:"allow_duplicate,omitempty" jsonschema:"create it even though the folder already holds something of that name"`
}

// TrashInput selects an item to trash or restore.
type TrashInput struct {
	File   string `json:"file" jsonschema:"the item: an id, a Drive URL, a path from My Drive, or a shared-drive path. A shortcut is trashed itself, not what it points at."`
	DryRun bool   `json:"dry_run,omitempty" jsonschema:"report what would happen and change nothing"`
}

// registerWrite adds the tools that change Drive. None of them is
// registered in read-only mode.
func registerWrite(s *mcp.Server, d Deps) []string {
	mcp.AddTool(s, &mcp.Tool{
		Name: "create_file",
		Description: "Make a new file: an empty Google Doc, Sheet, Slides deck, Drawing or Form with kind, or a " +
			"file written from text you have here with content. With convert_to, markdown or csv you have " +
			"written becomes a formatted Google Doc or Sheet. Up to 5 MB; a file already on disk goes through " +
			"upload_file. Refuses a second file of the same name in one folder unless allow_duplicate is set, " +
			"because a retried call is how a folder ends up with two.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CreateFileInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.CreateFile(ctx, service.CreateFileInput{
			Name: in.Name, Parent: in.Parent, Kind: in.Kind, Content: in.Content,
			MimeType: in.MimeType, ConvertTo: in.ConvertTo, Description: in.Description,
			Starred: in.Starred, AllowDuplicate: in.AllowDuplicate,
		}))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "upload_file",
		Description: "Send a file from the server's local directory to Drive. A small file goes in one request; " +
			"a large one goes in chunks and survives a dropped connection, so uploading gigabytes is a normal " +
			"thing to do. With convert_to, Google imports it as a Doc, Sheet or Slides deck, which for a PDF or " +
			"a photograph also reads the text out of it. " +
			"Needs the server to have been started with GDRIVE_LOCAL_DIR; get_account says whether it was.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in UploadFileInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.UploadFile(ctx, service.UploadFileInput{
			LocalPath: in.LocalPath, Name: in.Name, Parent: in.Parent, MimeType: in.MimeType,
			ConvertTo: in.ConvertTo, OCRLanguage: in.OCRLanguage,
			UseContentAsIndexableText: in.UseContentAsIndexableText, Description: in.Description,
			AllowDuplicate: in.AllowDuplicate,
		}))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "update_content",
		Description: "Replace what is inside a file, keeping its id, its place, its sharing and every link and " +
			"shortcut that points at it. Drive keeps the old version as a revision for about 30 days, and sooner " +
			"drops it once a file has 100 revisions, unless keep_previous_revision pins it. " +
			"A Google Doc, Sheet or Slides deck is refused: their content belongs to the Docs, Sheets and Slides " +
			"APIs, which this server does not offer. To edit a text file, read_file it, change the text, and " +
			"pass the whole new text back here.",
		Annotations: replacing,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in UpdateContentInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.UpdateContent(ctx, service.UpdateContentInput{
			File: in.File, Content: in.Content, LocalPath: in.LocalPath, MimeType: in.MimeType,
			KeepPreviousRevision: in.KeepPreviousRevision, ExpectHeadRevision: in.ExpectHeadRevision,
		}))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "create_folder",
		Description: "Make a folder. Refuses a second folder of the same name beside the first unless " +
			"allow_duplicate is set: Drive allows two, and nothing afterwards can tell you which one you meant.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CreateFolderInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.CreateFolder(ctx, service.CreateFolderInput{
			Name: in.Name, Parent: in.Parent, Description: in.Description,
			Color: in.Color, AllowDuplicate: in.AllowDuplicate,
		}))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "update_file",
		Description: "Change a file's details without touching its content: rename it, describe it, star it, " +
			"color a folder, set custom properties, mark it as opened, stop downloads and re-sharing, or give a " +
			"folder limited access. Only the fields you pass change, and the result shows each one before and " +
			"after. Turning a folder's limited access off is also put to the person when the client can ask; a " +
			"call they do not confirm is [blocked] and is not made again unless they ask. A server started with " +
			"GDRIVE_SHARING=off refuses anything here that lets more people reach or pass on a file. " +
			"update_content replaces what is inside a file; move_file changes where it is.",
		Annotations: idempotentWrite,
	}, asked(d, "update_file", func(ctx context.Context, in UpdateFileMetaInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.UpdateFile(ctx, service.UpdateFileInput{
			File: in.File, Name: in.Name, Description: in.Description, Starred: in.Starred,
			Color: in.Color, Properties: in.Properties,
			CopyRequiresWriterPermission: in.CopyRequiresWriterPermission,
			WritersCanShare:              in.WritersCanShare,
			RestrictDownload:             in.RestrictDownload,
			LimitedAccess:                in.LimitedAccess,
			Viewed:                       in.Viewed,
		}))
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name: "move_file",
		Description: "Move an item into another folder or into a shared drive, or up to 50 items with files. A " +
			"file in Drive has exactly one parent, so this takes it out of where it was. A move also changes who " +
			"can reach it: it keeps the access granted on it directly, loses what it had through the old " +
			"folder, and gains everyone who can reach the new folder or shared drive. The result shows who " +
			"could reach it before and who can after. A folder in My Drive cannot move into a shared drive at " +
			"all: make one there with create_folder and move the files into it. dry_run reports the old and new " +
			"locations and who would reach each item, and changes nothing. A move that lets more people reach " +
			"an item, or gives them more access, is also put to the person when the client can ask, once for " +
			"the whole call; a call they do not confirm is [blocked], nothing in it moves, and it is not made " +
			"again unless they ask. A server started with GDRIVE_SHARING=off refuses such a move instead.",
		Annotations: idempotentWrite,
	}, asked(d, "move_file", func(ctx context.Context, in MoveFileInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.MoveFile(ctx, service.MoveFileInput{
			File: in.File, Files: in.Files, To: in.To, DryRun: in.DryRun,
		}))
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name: "copy_file",
		Description: "Copy one file, leaving the original alone. With convert_to, Google imports the copy as one " +
			"of its own kinds; a PDF or a scanned image copied with convert_to: doc becomes a Google Doc with its " +
			"text read out, which read_file then reads. extract_text does the same and deletes the copy again. " +
			"A folder needs recursive: true, because Drive has no call that copies one — it is a listing per " +
			"folder and a write per item, so a large tree takes a while. A tree over max_items is refused " +
			"before anything is written rather than copied halfway; dry_run says how big it is first. " +
			"Shortcuts inside a tree are made again pointing where they point now, not at the copies.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CopyFileInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.CopyFile(ctx, service.CopyFileInput{
			File: in.File, Name: in.Name, To: in.To, ConvertTo: in.ConvertTo,
			OCRLanguage: in.OCRLanguage, KeepRevisionForever: in.KeepRevisionForever,
			CopyComments:   in.CopyComments,
			AllowDuplicate: in.AllowDuplicate, Recursive: in.Recursive, MaxItems: in.MaxItems,
			DryRun: in.DryRun,
		}))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "create_shortcut",
		Description: "Put a pointer to one item in another folder. A file has one parent in Drive, so this is " +
			"what to use when something has to appear in two places. Organizing, sharing and trashing a shortcut " +
			"act on the shortcut, never on what it points at.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CreateShortcutInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.CreateShortcut(ctx, service.CreateShortcutInput{
			Target: in.Target, Parent: in.Parent, Name: in.Name, AllowDuplicate: in.AllowDuplicate,
		}))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "trash_file",
		Description: "Move an item to the trash, which is reversible: restore_file brings it back, and Drive " +
			"empties the trash 30 days after an item goes in. Trashing a folder takes everything inside it. " +
			"This is the way to remove something; permanent deletion is a separate tool that most deployments " +
			"do not register at all.",
		Annotations: idempotentWrite,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in TrashInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.TrashFile(ctx, service.TrashInput{File: in.File, DryRun: in.DryRun}))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "restore_file",
		Description: "Take an item out of the trash. It goes back to the folder it came from, which the result " +
			"names, because that may not be where you were looking.",
		Annotations: idempotentWrite,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in TrashInput) (*mcp.CallToolResult, *render.WriteJSON, error) {
		return result(d.Service.RestoreFile(ctx, service.TrashInput{File: in.File, DryRun: in.DryRun}))
	})

	return []string{
		"create_file", "upload_file", "update_content", "create_folder", "update_file",
		"move_file", "copy_file", "create_shortcut", "trash_file", "restore_file",
	}
}
