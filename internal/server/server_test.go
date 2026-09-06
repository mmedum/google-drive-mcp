package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/internal/config"
	"github.com/mmedum/google-drive-mcp/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/internal/gdrive"
	"github.com/mmedum/google-drive-mcp/internal/mediatype"
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
	fake.AddComment("id-notes-fixture", "id-comment-fixture", "does this cover the second case?",
		drivetest.WithReply("id-reply-fixture", "Someone Else", "not yet", ""))
	fake.AddProposal("id-notes-fixture", "id-request-fixture", "alice@example.com", "writer")

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
	// The read tools, then the two that move content out, then the
	// listings phases 2 and 3 add, then the tools that change Drive. The
	// destructive five are not here: a default build does not register
	// them, and TestDestructiveToolsExistOnlyWhenAskedFor covers those.
	readTools := map[string]bool{
		"get_account": false, "get_file": false, "search_files": false, "list_folder": false,
		"read_file": false, "download_file": false,
		"list_permissions": false, "list_drives": false, "list_revisions": false, "list_changes": false,
		"list_comments": false, "list_access_requests": false,
	}
	want := map[string]bool{
		"create_file": false, "upload_file": false, "update_content": false, "create_folder": false,
		"update_file": false, "move_file": false, "copy_file": false, "create_shortcut": false,
		"trash_file": false, "restore_file": false,
		"share_file": false, "unshare_file": false, "manage_drive": false, "manage_revision": false,
		"add_comment": false, "reply_comment": false, "resolve_access_request": false,
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
	const registeredTools = 29
	if len(out.Tools) != registeredTools {
		t.Fatalf("dumped %d tools, want %d", len(out.Tools), registeredTools)
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
	// The download formats are the same shape of promise: a list typed
	// into a struct tag, and a registry that decides what the code will
	// take. A format in the schema that the registry does not know is
	// the worse half — a model would ask for it and be refused.
	formats := regexp.MustCompile(`\b[a-z0-9]{2,5}\b`)
	offered := map[string]bool{}
	for _, tool := range res.Tools {
		if tool.Name != "download_file" {
			continue
		}
		raw, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("download_file schema does not decode: %v", err)
		}
		described := schema.Properties["format"].Description
		if described == "" {
			t.Fatal("download_file's format argument carries no description")
		}
		// The comma-separated run is the list; the sentences around it
		// are prose, so only the words that are export names count.
		for _, word := range formats.FindAllString(described, -1) {
			if mediatype.ExportMime(word) != "" {
				offered[word] = true
			}
		}
	}
	if len(offered) == 0 {
		t.Fatal("no export format was read out of download_file's schema, so the check below is looking at nothing")
	}
	for _, name := range mediatype.ExportNames() {
		if !offered[name] {
			t.Errorf("download_file does not offer the export format %q that the registry accepts", name)
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
	for _, absent := range []string{"delete_file", "delete_comment", "list_labels"} {
		if strings.Contains(out, absent) {
			t.Errorf("get_account names %q, which is not registered:\n%s", absent, out)
		}
	}
}

// notYetRegistered are tools the design has planned but this build does
// not register. Nothing a model reads may name one: following the advice
// would get "no such tool". As each phase lands its tools, its names come
// off this list and may be used in output again.
var notYetRegistered = []string{"list_labels", "manage_labels"}

// gatedByDefault are the tools a default build leaves out on purpose:
// they destroy without a way back, and GDRIVE_ENABLE_DESTRUCTIVE has to
// be set for them to exist. A default build's output must not name one
// either — the advice would be as unfollowable as advice naming a tool
// nobody has written — so they are checked the same way, and separately,
// because these do exist in some builds and the two reasons are not the
// same reason.
var gatedByDefault = []string{"delete_file", "empty_trash", "delete_drive", "delete_revision", "delete_comment"}

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

	// Every result every registered tool can produce, including the
	// refusals, which are where advice is densest. The calls come from
	// toolArgs rather than a list typed here, and
	// TestEveryRegisteredToolIsExercised fails when a tool has no entry:
	// a list typed by hand stops covering the surface the moment the
	// surface grows, silently, which is exactly how this test came to be
	// checking four tools out of twenty-four.
	for _, c := range toolCalls() {
		// The gated four are in the table because
		// TestEveryRegisteredToolIsExercised makes them be; this build
		// does not register them, and their absence is what the loop
		// below asserts.
		if !registered[c.name] {
			continue
		}
		texts = append(texts, resultText(t, call(t, cs, c.name, c.args)))
	}

	for _, absent := range append(append([]string(nil), notYetRegistered...), gatedByDefault...) {
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
	const readOnlyTools = 12 // four reads, two content reads, phase 2's four listings, and phase 3's two
	if len(res.Tools) != readOnlyTools {
		t.Errorf("read-only mode registered %d tools, want the %d that only read", len(res.Tools), readOnlyTools)
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

func TestDestructiveToolsExistOnlyWhenAskedFor(t *testing.T) {
	// The specification treats annotations as untrusted hints, so the gate
	// that holds is registration itself: a tool that is not registered
	// cannot be called however a client feels about its annotations.
	cfg := defaultConfig()
	cfg.EnableDestructive = true
	cs, fake := sessionAndFake(t, cfg, true)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	found := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		found[tool.Name] = tool
	}
	for _, name := range gatedByDefault {
		tool := found[name]
		if tool == nil {
			t.Errorf("%s is not registered with GDRIVE_ENABLE_DESTRUCTIVE=true", name)
			continue
		}
		if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
			t.Errorf("%s is not annotated as destructive", name)
		}
		// The hint a client may act on to put the call in front of a
		// person before it runs.
		if tool.Meta["anthropic/requiresUserInteraction"] != true {
			t.Errorf("%s does not ask for user interaction: %v", name, tool.Meta)
		}
		if !strings.Contains(tool.Description, "NO UNDO") {
			t.Errorf("%s does not say it cannot be undone: %s", name, tool.Description)
		}
	}

	// Registered is not the same as callable: each still needs confirm.
	out := resultText(t, call(t, cs, "delete_file", map[string]any{"file": "id-notes-fixture"}))
	if !strings.Contains(out, "confirm: true") {
		t.Errorf("delete_file ran without confirm:\n%s", out)
	}
	if fake.Files["id-notes-fixture"] == nil {
		t.Fatal("the file was deleted without confirm")
	}
	if res := call(t, cs, "delete_file", map[string]any{
		"file": "id-notes-fixture", "confirm": true,
	}); res.IsError {
		t.Fatalf("delete_file with confirm: %s", resultText(t, res))
	}
	if fake.Files["id-notes-fixture"] != nil {
		t.Error("the file survived a confirmed delete")
	}
}

func TestSharingOffRemovesOnlyTheToolsThatWiden(t *testing.T) {
	cfg := defaultConfig()
	cfg.Sharing = config.SharingOff
	cs := session(t, cfg, true)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	registered := map[string]bool{}
	for _, tool := range res.Tools {
		registered[tool.Name] = true
	}
	for _, gone := range []string{"share_file", "unshare_file"} {
		if registered[gone] {
			t.Errorf("%s is registered with GDRIVE_SHARING=off", gone)
		}
	}
	// Seeing who can reach a file is not widening access, so the read
	// stays: a server that hides exposure is worse than one that cannot
	// change it.
	if !registered["list_permissions"] {
		t.Error("list_permissions was removed along with the writes")
	}
	out := resultText(t, call(t, cs, "list_permissions", map[string]any{"file": "id-notes-fixture"}))
	if !strings.Contains(out, "GDRIVE_SHARING=off") {
		t.Errorf("the listing does not say why nothing can be changed:\n%s", out)
	}
}

func TestReadOnlyModeKeepsEveryListingAndNoWrite(t *testing.T) {
	cfg := defaultConfig()
	cfg.ReadOnly = true
	cfg.EnableDestructive = true // read-only wins: the stricter setting is the one meant
	cs := session(t, cfg, true)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	registered := map[string]bool{}
	for _, tool := range res.Tools {
		registered[tool.Name] = true
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("read-only mode registered %q, which changes Drive", tool.Name)
		}
	}
	for _, want := range []string{"list_permissions", "list_drives", "list_revisions", "list_changes"} {
		if !registered[want] {
			t.Errorf("read-only mode dropped the listing %q", want)
		}
	}
	for _, gone := range append([]string{"share_file", "manage_drive", "manage_revision"}, gatedByDefault...) {
		if registered[gone] {
			t.Errorf("read-only mode registered the write %q", gone)
		}
	}
}

// toolArgs is one or more representative calls per registered tool: the
// happy path where there is one, and the refusals, which are where a
// result's advice is densest and therefore where it is most likely to
// name something that does not exist.
//
// It is a table rather than a list of calls so that the inventory can be
// checked against the server's own tool list. A hand-typed list of calls
// decays every time a phase adds a tool, and nothing makes a sound when
// it does — which is how the test that reads these results came to be
// exercising four tools out of twenty-four.
func toolArgs() map[string][]map[string]any {
	return map[string][]map[string]any{
		"get_account": {{}},
		"get_file": {
			{"file": "id-notes-fixture"},
			{"file": "id-projects-fixture"},
			{"file": "1NoSuchFileIdFixtureAAAAAAAAAAAAAAA"},
			{"file": "https://drive.google.com/drive/shared-drives"},
		},
		"list_folder": {
			{"folder": "/Projects"},
			{"folder": "id-notes-fixture"},
			{"folder": "root", "recursive": true},
		},
		"search_files": {
			{"name": "Meeting"},
			{"name": "nothingmatchesthis"},
			{},
			{"kind": "not-a-kind", "name": "x"},
		},
		"read_file":      {{"file": "id-notes-fixture"}, {"file": "id-projects-fixture"}},
		"download_file":  {{"file": "id-notes-fixture"}},
		"upload_file":    {{"local_path": "notes.txt"}},
		"create_file":    {{"name": "Notes", "kind": "doc"}, {"name": "x"}},
		"create_folder":  {{"name": "Reports", "parent": "id-projects-fixture"}},
		"update_content": {{"file": "id-notes-fixture", "content": "no"}},
		"update_file":    {{"file": "id-notes-fixture", "starred": true}},
		"move_file":      {{"file": "id-notes-fixture", "to": "root"}, {"file": "id-notes-fixture"}},
		"copy_file":      {{"file": "id-notes-fixture"}, {"file": "id-projects-fixture"}},
		"create_shortcut": {
			{"target": "id-notes-fixture", "parent": "id-projects-fixture", "name": "shortcut"},
		},
		"trash_file":   {{"file": "id-notes-fixture", "dry_run": true}},
		"restore_file": {{"file": "id-notes-fixture"}},

		"list_permissions": {{"file": "id-notes-fixture"}},
		"share_file": {
			{"file": "id-notes-fixture", "principal": "alice@example.com", "role": "reader"},
			{"file": "id-notes-fixture", "principal": "anyone", "role": "reader"},
			{"file": "id-notes-fixture", "principal": "not-an-address", "role": "reader"},
			{"file": "id-notes-fixture", "principal": "alice@example.com", "role": "owner"},
		},
		"unshare_file": {
			{"file": "id-notes-fixture", "remove_link": true},
			{"file": "id-notes-fixture"},
		},
		"list_drives": {{}},
		"manage_drive": {
			{"action": "create", "name": "Research"},
			{"action": "restrict", "drive": "Research"},
			{"action": "not-an-action"},
		},
		"list_revisions":  {{"file": "id-notes-fixture"}, {"file": "id-projects-fixture"}},
		"manage_revision": {{"file": "id-notes-fixture", "revision": "id-nothing", "action": "keep"}},
		"list_changes":    {{}, {"page_token": "not-a-token"}},

		"list_comments": {
			{"file": "id-notes-fixture"},
			{"file": "id-projects-fixture"},
			{"file": "id-notes-fixture", "include_deleted": true, "since": "not-a-date"},
		},
		"add_comment": {{"file": "id-notes-fixture", "content": "one more case to cover"}},
		"reply_comment": {
			{"file": "id-notes-fixture", "comment": "id-comment-fixture", "content": "covered now"},
			{"file": "id-notes-fixture", "comment": "id-comment-fixture", "action": "resolve"},
			{"file": "id-notes-fixture", "comment": "id-nothing", "action": "reopen"},
			{"file": "id-notes-fixture", "comment": "id-comment-fixture", "action": "not-an-action"},
		},
		"list_access_requests": {{"file": "id-notes-fixture"}, {"file": "id-projects-fixture"}},
		"resolve_access_request": {
			{"file": "id-notes-fixture", "request": "id-request-fixture", "action": "accept", "dry_run": true},
			{"file": "id-notes-fixture", "request": "id-nothing", "action": "deny"},
			{"file": "id-notes-fixture", "request": "id-request-fixture", "action": "maybe"},
		},

		"delete_file":     {{"file": "id-notes-fixture"}},
		"empty_trash":     {{"dry_run": true}},
		"delete_drive":    {{"drive": "nothing-of-that-name"}},
		"delete_revision": {{"file": "id-notes-fixture", "revision": "id-nothing"}},
		"delete_comment":  {{"file": "id-notes-fixture", "comment": "id-comment-fixture"}},
	}
}

// toolCall is one entry of toolArgs, flattened.
type toolCall struct {
	name string
	args map[string]any
}

// toolCalls flattens toolArgs in a stable order, so a failure names the
// same call every run.
func toolCalls() []toolCall {
	table := toolArgs()
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]toolCall, 0, len(table))
	for _, name := range names {
		for _, args := range table[name] {
			out = append(out, toolCall{name: name, args: args})
		}
	}
	return out
}

func TestEveryRegisteredToolIsExercised(t *testing.T) {
	// The forcing function. Without it, toolArgs is a list somebody typed
	// once and every later phase quietly leaves its tools out of every
	// test that reads from it.
	//
	// The destructive four are registered here so that the table has to
	// cover them too: they are the tools whose output most needs reading.
	cfg := defaultConfig()
	cfg.EnableDestructive = true
	cs := session(t, cfg, true)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	table := toolArgs()
	for _, tool := range res.Tools {
		if len(table[tool.Name]) == 0 {
			t.Errorf("%s is registered and toolArgs has no call for it", tool.Name)
		}
		delete(table, tool.Name)
	}
	extra := make([]string, 0, len(table))
	for name := range table {
		extra = append(extra, name)
	}
	sort.Strings(extra)
	for _, name := range extra {
		t.Errorf("toolArgs has a call for %q, which is not registered", name)
	}
}
