package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/config"
	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/server"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// session connects an in-memory client to a server over a fake Drive,
// which is how a real client sees the tool surface.
func session(t *testing.T, cfg config.Config, withCredentials bool) *mcp.ClientSession {
	t.Helper()
	fake := drivetest.New()
	t.Cleanup(fake.Close)
	fake.SetNow(func() time.Time { return time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC) })
	fake.AddFolder("id-projects-fixture", "Projects", fake.RootID)
	fake.AddFile("id-notes-fixture", "Meeting notes", gdrive.MimeDocument, "id-projects-fixture")

	api := drivetest.Client(t, fake)
	if !withCredentials {
		api = drivetest.NoCredentials(t, fake)
	}
	svc := service.New(api, service.Options{
		ReadOnly: cfg.ReadOnly, Destructive: cfg.EnableDestructive, Sharing: cfg.Sharing,
		LocalDir: cfg.LocalDir, MaxDownload: cfg.MaxDownload,
		Now: func() time.Time { return time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC) },
	})
	srv := server.New(server.Deps{Service: svc, Config: cfg, Version: "test"})

	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func defaultConfig() config.Config {
	return config.Config{Profile: "default", Sharing: config.SharingAll, MaxDownload: 1 << 30, HTTPTimeout: time.Minute}
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestToolsListed(t *testing.T) {
	cs := session(t, defaultConfig(), true)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := map[string]bool{"get_account": false, "get_file": false, "search_files": false, "list_folder": false}
	for _, tool := range res.Tools {
		if _, ok := want[tool.Name]; !ok {
			t.Errorf("unexpected tool %q registered", tool.Name)
			continue
		}
		want[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s should be marked read-only", tool.Name)
		}
		if strings.Contains(tool.Name, ".") {
			t.Errorf("tool names carry no dots: %q", tool.Name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %q is missing", name)
		}
	}
}

func TestSchemasAreFlat(t *testing.T) {
	cs := session(t, defaultConfig(), true)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range res.Tools {
		// Inspect the schema as a client sees it, on the wire.
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Errorf("%s: schema does not marshal: %v", tool.Name, err)
			continue
		}
		var schema struct {
			Type       string `json:"type"`
			Properties map[string]struct {
				Type        string `json:"type"`
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Errorf("%s: schema does not decode: %v", tool.Name, err)
			continue
		}
		if schema.Type != "object" {
			t.Errorf("%s: schema type = %q", tool.Name, schema.Type)
		}
		for name, prop := range schema.Properties {
			switch prop.Type {
			case "string", "boolean", "integer", "number":
			default:
				t.Errorf("%s.%s has type %q; schemas stay flat", tool.Name, name, prop.Type)
			}
			if prop.Description == "" {
				t.Errorf("%s.%s has no description", tool.Name, name)
			}
		}
	}
}

func TestGetFileThroughTheProtocol(t *testing.T) {
	cs := session(t, defaultConfig(), true)
	res := call(t, cs, "get_file", map[string]any{"file": "id-notes-fixture"})
	if res.IsError {
		t.Fatalf("get_file failed: %s", resultText(t, res))
	}
	out := resultText(t, res)
	if !strings.Contains(out, "Meeting notes — Google Doc") {
		t.Errorf("output:\n%s", out)
	}
	// A read tool returns text only: Claude Code shows the model the
	// structured form alone when both are present.
	if res.StructuredContent != nil {
		t.Errorf("a read tool should return no structured content, got %v", res.StructuredContent)
	}
}

func TestListFolderThroughTheProtocol(t *testing.T) {
	cs := session(t, defaultConfig(), true)
	res := call(t, cs, "list_folder", map[string]any{"folder": "/Projects"})
	if res.IsError {
		t.Fatalf("list_folder failed: %s", resultText(t, res))
	}
	if !strings.Contains(resultText(t, res), "Meeting notes") {
		t.Errorf("output:\n%s", resultText(t, res))
	}
}

func TestSearchFilesThroughTheProtocol(t *testing.T) {
	cs := session(t, defaultConfig(), true)
	res := call(t, cs, "search_files", map[string]any{"name": "Meeting"})
	if res.IsError {
		t.Fatalf("search_files failed: %s", resultText(t, res))
	}
	if !strings.Contains(resultText(t, res), "Meeting notes") {
		t.Errorf("output:\n%s", resultText(t, res))
	}
}

func TestGetAccountThroughTheProtocol(t *testing.T) {
	cs := session(t, defaultConfig(), true)
	res := call(t, cs, "get_account", map[string]any{})
	if res.IsError {
		t.Fatalf("get_account failed: %s", resultText(t, res))
	}
	if !strings.Contains(resultText(t, res), drivetest.AccountEmail) {
		t.Errorf("output:\n%s", resultText(t, res))
	}
}

func TestToolErrorsCarryAClass(t *testing.T) {
	cs := session(t, defaultConfig(), true)
	cases := []struct {
		tool  string
		args  map[string]any
		class string
	}{
		{"get_file", map[string]any{"file": "1NoSuchFileIdAAAAAAAAAAAAAAAAAAAAAA"}, "[not_found]"},
		{"get_file", map[string]any{"file": "https://example.com/x"}, "[invalid]"},
		{"search_files", map[string]any{}, "[invalid]"},
		{"list_folder", map[string]any{"folder": "id-notes-fixture"}, "[invalid]"},
	}
	for _, c := range cases {
		res := call(t, cs, c.tool, c.args)
		if !res.IsError {
			t.Errorf("%s %v should be an error", c.tool, c.args)
			continue
		}
		out := resultText(t, res)
		if !strings.Contains(out, c.class) {
			t.Errorf("%s %v: want class %s, got %q", c.tool, c.args, c.class, out)
		}
	}
}

func TestWithoutCredentialsEveryToolAnswersAuth(t *testing.T) {
	cs := session(t, defaultConfig(), false)
	for _, name := range []string{"get_account", "get_file", "search_files", "list_folder"} {
		args := map[string]any{}
		switch name {
		case "get_file":
			args["file"] = "1SyntheticFixtureFileIdAAAAAAAAAAAA"
		case "search_files":
			args["name"] = "anything"
		}
		res := call(t, cs, name, args)
		if !res.IsError {
			t.Errorf("%s should fail without credentials", name)
			continue
		}
		if out := resultText(t, res); !strings.Contains(out, "[auth]") {
			t.Errorf("%s: want an [auth] class, got %q", name, out)
		}
	}
}

func TestInstructionsNameTheGroundRules(t *testing.T) {
	cs := session(t, defaultConfig(), true)
	got := cs.InitializeResult().Instructions
	for _, want := range []string{"get_file", "list_folder", "search_files", "ambiguous", "substring"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions do not mention %q:\n%s", want, got)
		}
	}
}

func TestDumpSchemas(t *testing.T) {
	srv := server.New(server.Deps{Config: defaultConfig(), Version: "test"})
	var buf bytes.Buffer
	if err := server.DumpSchemas(context.Background(), srv, &buf, "test"); err != nil {
		t.Fatalf("DumpSchemas: %v", err)
	}
	var out struct {
		Server string `json:"server"`
		SDK    string `json:"sdk"`
		Tools  []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Properties map[string]any `json:"properties"`
				Required   []string       `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
		ResourceTemplates []any `json:"resourceTemplates"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("dump is not valid JSON: %v", err)
	}
	if out.Server != server.Name || out.SDK != server.SDKVersion {
		t.Errorf("dump header = %+v", out)
	}
	if len(out.Tools) != 4 {
		t.Fatalf("dumped %d tools, want 4", len(out.Tools))
	}
	// Sorted, so a diff between releases is a diff in the surface.
	for i := 1; i < len(out.Tools); i++ {
		if out.Tools[i-1].Name > out.Tools[i].Name {
			t.Errorf("tools are not sorted: %s before %s", out.Tools[i-1].Name, out.Tools[i].Name)
		}
	}
	for _, tool := range out.Tools {
		if tool.Name == "get_file" && len(tool.InputSchema.Required) != 1 {
			t.Errorf("get_file should require exactly the file argument, got %v", tool.InputSchema.Required)
		}
	}
}

func TestServerRunsWithoutAService(t *testing.T) {
	// --dump-schemas builds a server with no service at all; registering
	// the tools must not need one.
	srv := server.New(server.Deps{Config: defaultConfig(), Version: "test"})
	if srv == nil {
		t.Fatal("New returned nil")
	}
}

func TestSchemaDescriptionsCarryNoUnexpandedPlaceholders(t *testing.T) {
	// A struct tag cannot be composed from a constant, and a templated
	// one that nothing expands ships the template to the model.
	cs := session(t, defaultConfig(), true)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range res.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		for _, marker := range []string{"${", "%s", "%!", "TODO", "FIXME"} {
			if strings.Contains(string(raw), marker) {
				t.Errorf("%s schema contains %q: %s", tool.Name, marker, raw)
			}
		}
		if strings.Contains(tool.Description, "${") {
			t.Errorf("%s description contains an unexpanded placeholder", tool.Name)
		}
	}
}

func TestSchemasNameTheKindsAndOrdersTheServiceAccepts(t *testing.T) {
	// The kind and order lists are prose in a struct tag, so nothing but
	// a test ties them to the values the service will actually take.
	cs := session(t, defaultConfig(), true)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	schemas := map[string]string{}
	for _, tool := range res.Tools {
		raw, _ := json.Marshal(tool.InputSchema)
		schemas[tool.Name] = string(raw)
	}
	for _, tool := range []string{"search_files", "list_folder"} {
		for _, kind := range service.Kinds() {
			if !strings.Contains(schemas[tool], kind) {
				t.Errorf("%s does not offer the kind %q that the service accepts", tool, kind)
			}
		}
	}
	for _, order := range service.OrderBys() {
		if !strings.Contains(schemas["search_files"], order) {
			t.Errorf("search_files does not offer the order %q that the service accepts", order)
		}
	}
}

func TestGetAccountReportsTheToolsThatExist(t *testing.T) {
	cs := session(t, defaultConfig(), true)
	res := call(t, cs, "get_account", map[string]any{})
	out := resultText(t, res)
	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range listed.Tools {
		if !strings.Contains(out, tool.Name) {
			t.Errorf("get_account does not mention the registered tool %q:\n%s", tool.Name, out)
		}
	}
	// Naming a tool a later phase will add would be a claim the server
	// cannot honour today.
	for _, absent := range []string{"trash_file", "restore_file", "list_permissions", "delete_file"} {
		if strings.Contains(out, absent) {
			t.Errorf("get_account names %q, which is not registered:\n%s", absent, out)
		}
	}
}

// notYetRegistered are tools the design has planned but this build does
// not register. Nothing a model reads may name one: following the advice
// would get "no such tool". As each phase lands its tools, its names come
// off this list and may be used in output again.
var notYetRegistered = []string{
	"read_file", "download_file", "create_file", "upload_file", "update_content",
	"create_folder", "update_file", "move_file", "copy_file", "create_shortcut",
	"trash_file", "restore_file", "list_permissions", "share_file", "unshare_file",
	"list_drives", "manage_drive", "list_revisions", "manage_revision", "list_changes",
	"list_comments", "add_comment", "reply_comment", "delete_file", "empty_trash",
	"delete_drive", "delete_revision", "delete_comment", "list_labels", "manage_labels",
}

func TestNothingAModelReadsNamesAToolThatDoesNotExist(t *testing.T) {
	cs := session(t, defaultConfig(), true)

	var texts []string
	texts = append(texts, cs.InitializeResult().Instructions)
	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	registered := map[string]bool{}
	for _, tool := range listed.Tools {
		registered[tool.Name] = true
		raw, _ := json.Marshal(tool.InputSchema)
		texts = append(texts, tool.Description, string(raw))
	}

	// Every result the four tools can produce, including the refusals,
	// which are where advice is densest.
	calls := []struct {
		name string
		args map[string]any
	}{
		{"get_account", map[string]any{}},
		{"get_file", map[string]any{"file": "id-notes-fixture"}},
		{"get_file", map[string]any{"file": "id-projects-fixture"}},
		{"get_file", map[string]any{"file": "1NoSuchFileIdAAAAAAAAAAAAAAAAAAAAAA"}},
		{"get_file", map[string]any{"file": "https://drive.google.com/drive/shared-drives"}},
		{"list_folder", map[string]any{"folder": "/Projects"}},
		{"list_folder", map[string]any{"folder": "id-notes-fixture"}},
		{"list_folder", map[string]any{"folder": "root", "recursive": true}},
		{"search_files", map[string]any{"name": "Meeting"}},
		{"search_files", map[string]any{"name": "nothingmatchesthis"}},
		{"search_files", map[string]any{}},
		{"search_files", map[string]any{"kind": "not-a-kind", "name": "x"}},
	}
	for _, c := range calls {
		texts = append(texts, resultText(t, call(t, cs, c.name, c.args)))
	}

	for _, absent := range notYetRegistered {
		if registered[absent] {
			t.Errorf("%q is registered; take it off notYetRegistered", absent)
			continue
		}
		for _, text := range texts {
			if strings.Contains(text, absent) {
				t.Errorf("output names %q, which this build does not register:\n%s", absent, text)
				break
			}
		}
	}
}
