// Package server wires the MCP SDK to the tools and offers a schema dump
// through an in-memory client session.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/config"
	"github.com/mmedum/google-drive-mcp/internal/service"
	"github.com/mmedum/google-drive-mcp/internal/tools"
)

// Name is the MCP server name.
const Name = "google-drive-mcp"

// SDKVersion is recorded in schema dumps so a diff caused by an SDK
// upgrade can be told apart from a tool-surface change.
const SDKVersion = "v1.7.0"

const instructions = "Google Drive tools, at the file boundary: finding files, where they live, who can see them, " +
	"folders, transfers, sharing and history. What is inside a Google Doc, Sheet or Slides deck is out of scope; " +
	"those have their own APIs. " +
	"Start with get_file on any id, URL or path: it is cheap and it shows the folder a file sits in and who can " +
	"reach it, both of which change what an action means. Use list_folder when you know where something is and " +
	"search_files when you do not; a listing costs twenty times a read, so do not walk a tree to find one file. " +
	"Drive has no substring search: name and text match the beginnings of words and whole words. " +
	"Ids are the contract. A name or path that matches more than one item comes back as [ambiguous] with the " +
	"candidates; pick one and pass its id rather than retrying the name."

// Deps are what the server needs.
type Deps struct {
	Service *service.Service
	Config  config.Config
	Logger  *slog.Logger
	Version string
}

// New builds the MCP server with every tool the configuration allows.
func New(d Deps) *mcp.Server {
	opts := &mcp.ServerOptions{Instructions: instructions}
	if d.Logger != nil && d.Logger.Enabled(context.Background(), slog.LevelDebug) {
		opts.Logger = d.Logger
	}
	s := mcp.NewServer(&mcp.Implementation{Name: Name, Version: d.Version}, opts)
	registered := tools.Register(s, tools.Deps{Service: d.Service, Config: d.Config, Logger: d.Logger})
	if d.Service != nil {
		d.Service.SetRegisteredTools(registered)
	}
	return s
}

// DumpSchemas writes the tool list and the resource templates as the wire
// would carry them: the SDK has no public enumerator, so an in-memory
// client asks the server.
func DumpSchemas(ctx context.Context, s *mcp.Server, w io.Writer, version string) error {
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		return fmt.Errorf("connect server: %w", err)
	}
	defer func() { _ = ss.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "schema-dump", Version: version}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		return fmt.Errorf("connect client: %w", err)
	}
	defer func() { _ = cs.Close() }()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("list tools: %w", err)
	}
	sort.Slice(res.Tools, func(i, j int) bool { return res.Tools[i].Name < res.Tools[j].Name })
	tmpls, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil {
		return fmt.Errorf("list resource templates: %w", err)
	}
	sort.Slice(tmpls.ResourceTemplates, func(i, j int) bool {
		return tmpls.ResourceTemplates[i].URITemplate < tmpls.ResourceTemplates[j].URITemplate
	})
	out := struct {
		Server            string                  `json:"server"`
		Version           string                  `json:"version"`
		SDK               string                  `json:"sdk"`
		Tools             []*mcp.Tool             `json:"tools"`
		ResourceTemplates []*mcp.ResourceTemplate `json:"resourceTemplates"`
	}{Name, version, SDKVersion, res.Tools, tmpls.ResourceTemplates}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
