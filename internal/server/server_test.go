package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/config"
	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/render"
	"github.com/mmedum/google-drive-mcp/internal/server"
	"github.com/mmedum/google-drive-mcp/internal/service"
)

// session connects an in-memory client to a server over a fake Drive,
// which is how a real client sees the tool surface.
func session(t *testing.T, cfg config.Config, withCredentials bool) *mcp.ClientSession {
	t.Helper()
	cs, _ := sessionAndFake(t, cfg, withCredentials)
	return cs
}

// sessionAndFake is session, plus the fake Drive behind it, for tests
// that check what a call actually did rather than what it printed.
func sessionAndFake(t *testing.T, cfg config.Config, withCredentials bool) (*mcp.ClientSession, *drivetest.Server) {
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
	return cs, fake
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
	// The read tools, then the two that move content out, then the ten
	// that change Drive.
	readTools := map[string]bool{
		"get_account": false, "get_file": false, "search_files": false, "list_folder": false,
		"read_file": false, "download_file": false,
	}
	want := map[string]bool{
		"create_file": false, "upload_file": false, "update_content": false, "create_folder": false,
		"update_file": false, "move_file": false, "copy_file": false, "create_shortcut": false,
		"trash_file": false, "restore_file": false,
	}
	for name := range readTools {
		want[name] = false
	}
	for _, tool := range res.Tools {
		if _, ok := want[tool.Name]; !ok {
			t.Errorf("unexpected tool %q registered", tool.Name)
			continue
		}
		want[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		if tool.Annotations == nil {
			t.Errorf("%s has no annotations", tool.Name)
			continue
		}
		if _, isRead := readTools[tool.Name]; isRead != tool.Annotations.ReadOnlyHint {
			t.Errorf("%s: readOnlyHint = %v, want %v", tool.Name, tool.Annotations.ReadOnlyHint, isRead)
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
				// A type is a scalar name, or a pair with "null" for an
				// argument whose absence differs from its false or empty
				// value. Both decode through json.RawMessage.
				Type                 json.RawMessage `json:"type"`
				Description          string          `json:"description"`
				AdditionalProperties *struct {
					Type string `json:"type"`
				} `json:"additionalProperties"`
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
			if err := flatType(prop.Type, prop.AdditionalProperties != nil); err != nil {
				t.Errorf("%s.%s: %v", tool.Name, name, err)
			}
			if prop.AdditionalProperties != nil && !isScalar(prop.AdditionalProperties.Type) {
				t.Errorf("%s.%s maps to %q; a map's values stay scalar", tool.Name, name, prop.AdditionalProperties.Type)
			}
			if prop.Description == "" {
				t.Errorf("%s.%s has no description", tool.Name, name)
			}
		}
	}
}

// flatType checks one argument's type. Flat means a model can fill the
// argument in without building a structure: a scalar, a nullable scalar
// (where leaving the argument out and passing its zero value mean
// different things), or a map of scalars for the custom properties Drive
// itself stores as a map.
func flatType(raw json.RawMessage, isMap bool) error {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		if one == "object" && isMap {
			return nil
		}
		if !isScalar(one) {
			return fmt.Errorf("has type %q; schemas stay flat", one)
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return fmt.Errorf("has an unreadable type %s", raw)
	}
	for _, t := range many {
		if t == "null" || isScalar(t) {
			continue
		}
		return fmt.Errorf("has type %v; schemas stay flat", many)
	}
	return nil
}

func isScalar(t string) bool {
	switch t {
	case "string", "boolean", "integer", "number":
		return true
	}
	return false
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
		{"get_file", map[string]any{"file": "1NoSuchFileIdFixtureAAAAAAAAAAAAAAA"}, "[not_found]"},
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
	if len(out.Tools) != 16 {
		t.Fatalf("dumped %d tools, want 16", len(out.Tools))
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
	for _, absent := range []string{"list_permissions", "share_file", "delete_file", "list_changes"} {
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
	"list_permissions", "share_file", "unshare_file",
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
		{"get_file", map[string]any{"file": "1NoSuchFileIdFixtureAAAAAAAAAAAAAAA"}},
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

func TestOverlappingToolsPointAtEachOther(t *testing.T) {
	// A tool description is the model's only map. Where two tools could
	// both plausibly answer a question, each has to name the other and
	// say when to choose it, or the model picks by guesswork.
	cs := session(t, defaultConfig(), true)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	describes := map[string]string{}
	for _, tool := range res.Tools {
		describes[tool.Name] = tool.Description
	}
	overlaps := [][2]string{
		{"search_files", "list_folder"}, // both find items
		{"list_folder", "search_files"},
		{"get_file", "list_folder"},       // a folder is a file too
		{"get_account", "get_file"},       // both are "get something"
		{"read_file", "download_file"},    // both get content out
		{"download_file", "read_file"},    //
		{"create_file", "upload_file"},    // both put a new file in
		{"upload_file", "convert_to"},     // the import path, easy to miss
		{"update_content", "read_file"},   // editing text means reading it first
		{"update_file", "update_content"}, // "update" could mean either
		{"update_file", "move_file"},      // and could mean the third
		{"copy_file", "read_file"},        // the OCR route to text
		{"trash_file", "restore_file"},    // the undo
		{"create_shortcut", "shortcut"},   //
		{"move_file", "create_folder"},    // the way round the folder refusal
	}
	for _, pair := range overlaps {
		if !strings.Contains(describes[pair[0]], pair[1]) {
			t.Errorf("%s overlaps with %s but never names it:\n%s", pair[0], pair[1], describes[pair[0]])
		}
	}
}

func TestWriteToolsThroughTheProtocol(t *testing.T) {
	cs := session(t, defaultConfig(), true)

	res := call(t, cs, "create_folder", map[string]any{"name": "Reports", "parent": "id-projects-fixture"})
	if res.IsError {
		t.Fatalf("create_folder failed: %s", resultText(t, res))
	}
	out := resultText(t, res)
	if !strings.Contains(out, "created: Reports — folder") {
		t.Errorf("text:\n%s", out)
	}
	// A write returns both forms, and the structured one carries the
	// prose, because a client shows one or the other and never both.
	if res.StructuredContent == nil {
		t.Fatal("a write tool returned no structured content")
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("structured content does not marshal: %v", err)
	}
	var got struct {
		Summary string `json:"summary"`
		Action  string `json:"action"`
		File    struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"file"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("structured content does not decode: %v", err)
	}
	if got.Summary != out {
		t.Errorf("the structured summary and the text differ:\n%s\n---\n%s", got.Summary, out)
	}
	if got.Action != "created" || got.File.Name != "Reports" || got.File.Kind != "folder" {
		t.Errorf("structured content = %+v", got)
	}
	if got.File.ID == "" {
		t.Error("the structured result carries no id, which is what the next call needs")
	}
}

func TestReadOnlyModeRegistersNoWriteTools(t *testing.T) {
	cfg := defaultConfig()
	cfg.ReadOnly = true
	cs := session(t, cfg, true)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range res.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("read-only mode registered %q, which changes Drive", tool.Name)
		}
	}
	if len(res.Tools) != 6 {
		t.Errorf("read-only mode registered %d tools, want the six that only read", len(res.Tools))
	}
	// A write that is not registered cannot be called; the service
	// refuses one anyway, so a later tool that forgets to ask still fails
	// closed.
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_folder", Arguments: map[string]any{"name": "Reports"},
	}); err == nil {
		t.Error("create_folder answered in read-only mode")
	}
}

func TestTransferToolsSayWhyTheyCannotRun(t *testing.T) {
	// With no local directory the two transfer tools are still
	// registered: a missing tool tells the model nothing, and this
	// refusal names the setting that turns them on.
	cs := session(t, defaultConfig(), true)
	for _, name := range []string{"download_file", "upload_file"} {
		args := map[string]any{"file": "id-notes-fixture"}
		if name == "upload_file" {
			args = map[string]any{"local_path": "notes.txt"}
		}
		res := call(t, cs, name, args)
		if !res.IsError {
			t.Errorf("%s worked without a local directory", name)
			continue
		}
		if !strings.Contains(resultText(t, res), "GDRIVE_LOCAL_DIR") {
			t.Errorf("%s does not name the setting it needs: %s", name, resultText(t, res))
		}
	}
}

func TestEveryWriteToolThroughTheProtocol(t *testing.T) {
	// One pass over the whole write surface as a client drives it. The
	// service tests check the rules; this checks that each tool's
	// arguments reach the service at all, which a typo in a struct tag
	// would otherwise break silently.
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.LocalDir = dir
	cs, fake := sessionAndFake(t, cfg, true)
	if err := os.WriteFile(filepath.Join(dir, "rows.csv"), []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake.SetContent("id-notes-fixture", "# Notes\n\nthe text\n")

	folder := idOf(t, cs, "create_folder", map[string]any{
		"name": "Reports", "parent": "id-projects-fixture", "color": "#4986e7", "description": "quarterly",
	})
	doc := idOf(t, cs, "create_file", map[string]any{"name": "Plan", "kind": "doc", "parent": folder})
	text := idOf(t, cs, "create_file", map[string]any{
		"name": "inline.csv", "parent": folder, "content": "a,b\n", "mime_type": "text/csv",
	})
	uploaded := idOf(t, cs, "upload_file", map[string]any{
		"local_path": "rows.csv", "parent": folder, "name": "uploaded.csv", "description": "from disk",
	})
	shortcut := idOf(t, cs, "create_shortcut", map[string]any{
		"target": text, "parent": "id-projects-fixture", "name": "inline link",
	})
	copied := idOf(t, cs, "copy_file", map[string]any{
		"file": uploaded, "name": "uploaded copy.csv", "to": "id-projects-fixture",
	})

	for _, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"read_file", map[string]any{"file": text}, "a,b"},
		{"read_file", map[string]any{"file": "id-notes-fixture"}, "the text"},
		{"read_file", map[string]any{"file": text, "offset": 2, "max_chars": 2}, "bytes 2 to 3"},
		{"download_file", map[string]any{"file": uploaded}, "checksum: verified"},
		{"download_file", map[string]any{"file": "id-notes-fixture", "format": "txt"}, "saved:"},
		{"update_content", map[string]any{
			"file": text, "content": "a,b\n3,4\n", "keep_previous_revision": true,
		}, "replaced the content"},
		{"update_content", map[string]any{"file": uploaded, "local_path": "rows.csv"}, "replaced the content"},
		{"update_file", map[string]any{
			"file": text, "name": "renamed.csv", "starred": true,
			"properties": map[string]any{"team": "finance"},
		}, "changed:"},
		{"update_file", map[string]any{"file": doc, "description": "a plan"}, "description"},
		{"move_file", map[string]any{"file": copied, "to": folder, "dry_run": true}, "would have moved"},
		{"move_file", map[string]any{"file": copied, "to": folder}, "moved:"},
		{"trash_file", map[string]any{"file": shortcut, "dry_run": true}, "would have trashed"},
		{"trash_file", map[string]any{"file": shortcut}, "trashed:"},
		{"restore_file", map[string]any{"file": shortcut}, "restored:"},
	} {
		res := call(t, cs, c.tool, c.args)
		if res.IsError {
			t.Errorf("%s %v failed: %s", c.tool, c.args, resultText(t, res))
			continue
		}
		if !strings.Contains(resultText(t, res), c.want) {
			t.Errorf("%s %v did not report %q:\n%s", c.tool, c.args, c.want, resultText(t, res))
		}
	}

	// The shortcut's target survived being trashed and restored, and the
	// upload really carried the bytes off the disk.
	if fake.Files[text].Trashed {
		t.Error("trashing a shortcut trashed what it pointed at")
	}
	if fake.Content[uploaded] != "a,b\n1,2\n" {
		t.Errorf("the uploaded file holds %q", fake.Content[uploaded])
	}
}

// idOf runs a create and returns the id it reported, failing the test if
// the call did not work: everything after it needs that id.
func idOf(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) string {
	t.Helper()
	res := call(t, cs, tool, args)
	if res.IsError {
		t.Fatalf("%s failed: %s", tool, resultText(t, res))
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("%s: structured content does not marshal: %v", tool, err)
	}
	var got struct {
		File struct {
			ID string `json:"id"`
		} `json:"file"`
	}
	if err := json.Unmarshal(raw, &got); err != nil || got.File.ID == "" {
		t.Fatalf("%s returned no id: %s", tool, raw)
	}
	return got.File.ID
}

func TestTheActionSchemaNamesEveryActionTheCodeCanProduce(t *testing.T) {
	// The JSON schema tells the model which words the action field can
	// hold. A free-form string had already let "would move" and "dry run"
	// reach the output while the schema promised seven other words.
	cs := session(t, defaultConfig(), true)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var described string
	for _, tool := range res.Tools {
		if tool.Name != "trash_file" {
			continue
		}
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("output schema does not marshal: %v", err)
		}
		var schema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("output schema does not decode: %v", err)
		}
		described = schema.Properties["action"].Description
	}
	if described == "" {
		t.Fatal("the action field carries no description")
	}
	for _, action := range render.Actions() {
		if !strings.Contains(described, action) {
			t.Errorf("the schema does not name the action %q the code can produce: %s", action, described)
		}
	}
}
