package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/service"
)

// ReadFileInput selects a window of a file's text.
type ReadFileInput struct {
	File     string `json:"file" jsonschema:"a file id, any Drive or Docs URL, the word root for My Drive, a path from My Drive like /Projects/2026/notes.txt, or a shared-drive path like drive:Marketing/Campaigns. A name or path matching more than one item is refused with the candidates listed, so pass an id when you have one. A shortcut is followed to what it points at."`
	Format   string `json:"format,omitempty" jsonschema:"for a spreadsheet only: csv (the default) or tsv"`
	Offset   int64  `json:"offset,omitempty" jsonschema:"where to start reading. Pass 0 for the beginning, or the continue_from value a previous read of the same file reported."`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"how much text to return, default 20000, maximum 400000"`
}

// DownloadFileInput selects a file to write to the local directory.
type DownloadFileInput struct {
	File             string `json:"file" jsonschema:"a file id, any Drive or Docs URL, a path from My Drive like /Projects/2026/Budget.xlsx, or a shared-drive path like drive:Marketing/Campaigns. A name or path matching more than one item is refused with the candidates listed, so pass an id when you have one. A shortcut is followed to what it points at."`
	Format           string `json:"format,omitempty" jsonschema:"for a Google Doc, Sheet, Slides deck or Drawing only, which Drive converts as it exports them: docx, xlsx, pptx, pdf, odt, ods, odp, rtf, txt, html, md, csv, tsv, epub, png, jpg or svg. The default is the Office format for the kind, or png for a drawing."`
	Revision         string `json:"revision,omitempty" jsonschema:"a revision id, to fetch an older version instead of the current one"`
	AcknowledgeAbuse bool   `json:"acknowledge_abuse,omitempty" jsonschema:"download a file Google has flagged as malware or spam. Only pass this after a refusal that named the flag, and only when you know what the file is."`
}

// registerContent adds the two tools that move a file's content out of
// Drive. Both are read-only as far as Drive is concerned, so they stay
// registered in read-only mode; download_file still needs the local
// directory, and says so when it is not set.
func registerContent(s *mcp.Server, d Deps) []string {
	mcp.AddTool(s, &mcp.Tool{
		Name: "read_file",
		Description: "The text of one file, straight back to you: a Google Doc as markdown, a Sheet as csv, " +
			"Slides as plain text, and a text file, log, CSV, JSON or source file as itself. Only the part you " +
			"ask for is fetched, so the head of a 200 MB log costs one small request; the header says which part " +
			"you got and how to ask for the next. " +
			"PDFs, Office files and images are refused with the two ways forward, because they are not text. " +
			"Nothing is written to disk: download_file does that.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReadFileInput) (*mcp.CallToolResult, any, error) {
		out, err := d.Service.ReadFile(ctx, service.ReadFileInput{
			File: in.File, Format: in.Format, Offset: in.Offset, MaxChars: in.MaxChars,
		})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(out), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "download_file",
		Description: "Write a file to the server's local directory, streamed and checked against Drive's own " +
			"checksum. Use it for what read_file cannot return — a PDF, an image, a video, an Office file, a " +
			"whole spreadsheet — and for keeping a copy. A Google Doc, Sheet or Slides deck is converted on the " +
			"way out, docx, xlsx and pptx by default. The result says where the file landed. " +
			"This needs the server to have been started with GDRIVE_LOCAL_DIR; get_account says whether it was.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in DownloadFileInput) (*mcp.CallToolResult, any, error) {
		out, err := d.Service.DownloadFile(ctx, service.DownloadFileInput{
			File: in.File, Format: in.Format, Revision: in.Revision, AcknowledgeAbuse: in.AcknowledgeAbuse,
		})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(out), nil, nil
	})

	return []string{"read_file", "download_file"}
}
