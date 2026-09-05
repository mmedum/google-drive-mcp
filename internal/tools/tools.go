// Package tools registers the MCP tools. Handlers validate input, call
// the service, and shape the result; every rule worth testing lives in
// the service.
package tools

import (
	"errors"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/config"
	"github.com/mmedum/google-drive-mcp/internal/render"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// Deps are what the tools need.
type Deps struct {
	Service *service.Service
	Config  config.Config
	Logger  *slog.Logger
}

// Register adds every tool the configuration allows and returns their
// names. A tool the configuration excludes is never registered, because
// the specification treats annotations as untrusted hints: a gate has to
// be in the server. The names come back so get_account can report the
// surface that actually exists rather than a second description of these
// same rules, which would drift as later phases add tools.
func Register(s *mcp.Server, d Deps) []string {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	names := registerRead(s, d)
	names = append(names, registerContent(s, d)...)
	if !d.Config.ReadOnly {
		names = append(names, registerWrite(s, d)...)
	}
	return names
}

// text wraps a string as the tool's unstructured content. Read tools
// return only this: a client may show the model either a result's text
// or its structured form, and Claude Code shows only the structured form
// when both are present, so prose goes in the one form every client shows.
func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// result turns a write's outcome into the two forms a client may show:
// the prose and the structured record. Both carry the same facts,
// because a client shows one or the other and never both.
func result(r *service.Result, err error) (*mcp.CallToolResult, *render.WriteJSON, error) {
	if err != nil {
		return nil, nil, fail(err)
	}
	return text(r.Text), r.JSON, nil
}

// fail returns an error whose text is the LLM-facing "[class] message".
// The SDK turns a returned error into a result with isError: true.
func fail(err error) error {
	var se *service.Error
	if errors.As(err, &se) {
		return errors.New(se.Error())
	}
	return errors.New("[unexpected] " + err.Error())
}

var (
	readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)}
	// write adds something without removing anything.
	write = &mcp.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(false)}
	// idempotentWrite sets a state rather than adding to one, so calling
	// it twice with the same arguments leaves the same result.
	idempotentWrite = &mcp.ToolAnnotations{IdempotentHint: true, OpenWorldHint: new(false)}
)
