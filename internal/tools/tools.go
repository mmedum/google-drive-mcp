// Package tools registers the MCP tools. Handlers validate input, call
// the service, and shape the result; every rule worth testing lives in
// the service.
package tools

import (
	"errors"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
	"github.com/mmedum/google-drive-mcp/v3/internal/render"
	"github.com/mmedum/google-drive-mcp/v3/internal/service"
)

// Deps are what the tools need.
type Deps struct {
	Service *service.Service
	Config  config.Config
	Logger  *slog.Logger
}

// FullSurface is the configuration under which every tool registers.
//
// It lives beside Register rather than in the command that dumps the
// schemas or in the gate that reads the dump, because it has to name
// every gate Register consults and neither of those can know when a new
// one appears. The gate kept its own list, and that list could only ever
// hold booleans — it builds GDRIVE_<NAME>=true — so Sharing, which is
// all or off, could not be in it. A config value can say what an
// environment variable cannot.
//
// --dump-schemas uses it so the schema diff compares the whole
// registrable surface on both sides. Without that the current build was
// dumped with the gated tools and the baseline without them, so every
// one read as newly added on every run and a removed one could not be
// reported at all.
//
// TestFullSurfaceRegistersEverything holds the claim rather than this
// comment doing it: it enumerates the gate flags and requires no
// combination to register a tool this one does not. Dropping any of the
// first three lines fails it. The Sharing line is the exception and is
// inert: the checks read `!= SharingOff`, so the zero value already
// registers those tools and removing the line changes nothing. It is
// written anyway, because a reader should not have to know which gates
// happen to default open.
func FullSurface(cfg config.Config) config.Config {
	cfg.ReadOnly = false
	cfg.EnableDestructive = true
	cfg.Labels = true
	cfg.Activity = true
	cfg.Sharing = config.SharingAll
	return cfg
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
		names = append(names, registerExtract(s, d)...)
	}
	names = append(names, registerAccess(s, d)...)
	names = append(names, registerDrives(s, d)...)
	names = append(names, registerHistory(s, d)...)
	names = append(names, registerComments(s, d)...)
	names = append(names, registerApprovals(s, d)...)
	names = append(names, registerActivity(s, d)...)
	names = append(names, registerLabels(s, d)...)
	names = append(names, registerDestructive(s, d)...)
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

// interactionKey is Claude Code's mark for a tool whose permission
// prompt is itself the point: it prompts on every call, whatever the
// operator's allow rules say, and refuses the call where no prompt can
// be drawn. No other client reads it.
const interactionKey = "anthropic/requiresUserInteraction"

// requiresUserInteraction marks one of the few tools whose prompt is the
// point (§4a): a permanent delete that skips the trash, an unrecoverable
// delete of a whole container, and a share to anyone with the link or a
// whole domain. It is a hint and nothing more. The gate that holds is
// that these tools are not registered at all unless the deployer asked
// for them, so a client that ignores the mark changes nothing about what
// can run. It is on for every client: the server asks nobody itself.
// Nowhere else: a mark on a delete done many times in a row (one
// revision, one comment) makes a person answer a prompt per item, and
// people stop reading prompts they see that often.
func requiresUserInteraction() mcp.Meta {
	return mcp.Meta{interactionKey: true}
}

var (
	readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)}
	// write adds something without removing anything.
	write = &mcp.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(false)}
	// idempotentWrite sets a state rather than adding to one, so calling
	// it twice with the same arguments leaves the same result. Each one
	// can be set back, so none is destructive: the specification's
	// default for an unset destructiveHint is true, which is why it is
	// written out.
	idempotentWrite = &mcp.ToolAnnotations{DestructiveHint: new(false), IdempotentHint: true, OpenWorldHint: new(false)}
	// replacing overwrites what is there. The old content survives only
	// as a revision Drive may purge, or, for a comment's text or a
	// label's field, not at all, so it is destructive.
	replacing = &mcp.ToolAnnotations{DestructiveHint: new(true), OpenWorldHint: new(false)}
	// localWrite reads Drive and writes a new local file. It is not
	// read-only, and it never overwrites a local file, so it is not
	// destructive either.
	localWrite = &mcp.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(false)}
	// widening reaches past the people inside the organization, or hands
	// a file over. A grant can be taken back, but not what was read
	// meanwhile, and only a new owner can hand a file back, so a client
	// that prompts by destructiveHint prompts for it.
	widening = &mcp.ToolAnnotations{DestructiveHint: new(true), OpenWorldHint: new(false)}
	// final records something that cannot be taken back: an approval,
	// which Drive has no call to withdraw and which can lock a file for
	// good.
	final = &mcp.ToolAnnotations{DestructiveHint: new(true), OpenWorldHint: new(false)}
	// destructive removes something with no way back. Calling it twice is
	// harmless only because the second call finds nothing left, which is
	// not what idempotent is for, so it is not marked as such.
	destructive = &mcp.ToolAnnotations{DestructiveHint: new(true), OpenWorldHint: new(false)}
)
