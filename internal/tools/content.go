package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/v2/internal/service"
)

// ReadFileInput selects a window of a file's text.
type ReadFileInput struct {
	File     string `json:"file" jsonschema:"a file id, any Drive or Docs URL, the word root for My Drive, a path from My Drive like /Projects/2026/notes.txt, or a shared-drive path like drive:Marketing/Campaigns. A name or path matching more than one item is refused with the candidates listed, so pass an id when you have one. A shortcut is followed to what it points at."`
	Format   string `json:"format,omitempty" jsonschema:"for a spreadsheet only, a Google Sheet or an Excel or OpenDocument workbook: csv (the default) or tsv"`
	Offset   int64  `json:"offset,omitempty" jsonschema:"where to start reading. Pass 0 for the beginning, or the continue_from value a previous read of the same file reported."`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"how much text to return, default 20000, maximum 400000"`
}

// DownloadFileInput selects a file to write to the local directory.
type DownloadFileInput struct {
	File             string `json:"file" jsonschema:"a file id, any Drive or Docs URL, a path from My Drive like /Projects/2026/Budget.xlsx, or a shared-drive path like drive:Marketing/Campaigns. A name or path matching more than one item is refused with the candidates listed, so pass an id when you have one. A shortcut is followed to what it points at."`
	Format           string `json:"format,omitempty" jsonschema:"for a Google Doc, Sheet, Slides deck or Drawing only, which Drive converts as it exports them: docx, xlsx, pptx, pdf, odt, ods, odp, rtf, txt, html, md, csv, tsv, epub, zip, json, png, jpg or svg. Not every kind offers every one; get_file lists what a file offers. The default is the Office format for the kind, png for a drawing, and json for an Apps Script project."`
	Revision         string `json:"revision,omitempty" jsonschema:"a revision id, to fetch an older version instead of the current one"`
	AcknowledgeAbuse bool   `json:"acknowledge_abuse,omitempty" jsonschema:"download a file Google has flagged as malware or spam. Only pass this after a refusal that named the flag, and only when you know what the file is."`
}

// ExtractTextInput selects a PDF or an image whose text to read.
type ExtractTextInput struct {
	File        string `json:"file" jsonschema:"the PDF or image: a file id, any Drive URL, a path from My Drive like /Scans/receipt.pdf, or a shared-drive path like drive:Finance/Receipts/receipt.pdf. A name or path matching more than one item is refused with the candidates listed, so pass an id when you have one. A shortcut is followed to what it points at."`
	OCRLanguage string `json:"ocr_language,omitempty" jsonschema:"an ISO 639-1 language code hinting what language the text is in, such as en or de. Google detects the language without it."`
	Offset      int64  `json:"offset,omitempty" jsonschema:"where to start reading the text. Pass 0 for the beginning, or the offset a previous call of this tool on the same file said to continue from."`
	MaxChars    int    `json:"max_chars,omitempty" jsonschema:"how much text to return, default 20000, maximum 400000"`
	KeepCopy    bool   `json:"keep_copy,omitempty" jsonschema:"keep the Google Doc the OCR made, in the root of My Drive, and say its id and link, instead of deleting it"`
}

// registerContent adds the two tools that move a file's content out of
// Drive. Both are read-only as far as Drive is concerned, so they stay
// registered in read-only mode; download_file still needs the local
// directory, and says so when it is not set.
func registerContent(s *mcp.Server, d Deps) []string {
	mcp.AddTool(s, &mcp.Tool{
		Name: "read_file",
		Description: "The text of one file, straight back to you: a Google Doc as markdown, a Sheet as csv, " +
			"Slides as plain text, and a text file, log, CSV, JSON or source file as itself. A Word document, " +
			"an Excel workbook or a PowerPoint deck, or its OpenDocument counterpart, is read here as text: a " +
			"document in order, a workbook's first sheet as csv, a deck slide by slide. The old .doc, .xls and " +
			".ppt formats are not. Only the part you ask for is fetched, so the head of a 200 MB log costs one " +
			"small request; the header says which part you got and how to ask for the next. " +
			"PDFs and images are not text: extract_text reads them with Google's OCR, where it is registered. " +
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
		Annotations: localWrite,
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

// registerExtract adds extract_text. It writes, if only for the length
// of the call, so it is not registered in read-only mode.
//
// Its annotations are a write's: it adds a file and takes away only that
// same file, so it destroys nothing that was there before it ran, which
// is what destructiveHint asks about. Two calls make two copies, so it
// is not idempotent. It asks the person nothing: the only thing it
// removes for good is the copy it made a moment earlier, which nobody
// else has seen, and §4a asks before a write that cannot be undone on
// something that was already there.
//
// Like read_file it returns text only. Its result is a window of up to
// 400 000 characters, and a structured copy of the same text would
// double it in every client that shows both, while Claude Code shows the
// model only the structured half (§4, §18). What a write's record would
// carry, the kept copy's id and link or a leftover copy's, is in the
// text's header.
func registerExtract(s *mcp.Server, d Deps) []string {
	mcp.AddTool(s, &mcp.Tool{
		Name: "extract_text",
		Description: "Read the text in a PDF or an image (JPEG, PNG, GIF or BMP) with Google's OCR. Drive reads " +
			"such text only while converting a file into a Google Doc, so this makes a temporary Google Doc copy " +
			"in the root of My Drive, exports its text and deletes the copy for good; keep_copy leaves it " +
			"instead. The original is not changed. Calling again with offset pages through the same text for a " +
			"few minutes without another copy. If Google does not confirm the copy was made, the result is " +
			"[ambiguous_outcome] and says how to find it with search_files; do not call again to find out. " +
			"read_file reads a Google Doc, an Office file or a text file directly, with no copy. Google asks " +
			"for files of 2 MB or less; a file over 50 MB is refused.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ExtractTextInput) (*mcp.CallToolResult, any, error) {
		out, err := d.Service.ExtractText(ctx, service.ExtractTextInput{
			File: in.File, OCRLanguage: in.OCRLanguage, Offset: in.Offset, MaxChars: in.MaxChars,
			KeepCopy: in.KeepCopy,
		})
		if err != nil {
			return nil, nil, fail(err)
		}
		return text(out), nil, nil
	})
	return []string{"extract_text"}
}
