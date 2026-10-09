package tools_test

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-drive-mcp/v2/internal/config"
	"github.com/mmedum/google-drive-mcp/v2/internal/gapi/drivetest"
	"github.com/mmedum/google-drive-mcp/v2/internal/server"
	"github.com/mmedum/google-drive-mcp/v2/internal/service"
	"github.com/mmedum/google-drive-mcp/v2/internal/tools"
)

// The protocols a question goes out on: before 2026-07-28 the SDK asks
// with elicitation/create inside the call; from it, the call returns the
// question and comes back with the answer (§4a).
var protocols = []string{"2025-06-18", "2025-11-25", "2026-07-28"}

// everything registers every tool.
func everything() config.Config {
	return tools.FullSurface(config.Config{Profile: "default", MaxDownload: 1 << 30, HTTPTimeout: time.Minute})
}

// person answers the questions a test client is asked, and keeps them.
type person struct {
	mu        sync.Mutex
	questions []*mcp.ElicitParams
	action    string
}

func (p *person) handle(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.questions = append(p.questions, req.Params)
	return &mcp.ElicitResult{Action: p.action}, nil
}

func (p *person) asked() []*mcp.ElicitParams {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.questions)
}

// fixtures is the fake Drive every asking case can reach its write in.
func fixtures(t *testing.T) *drivetest.Server {
	t.Helper()
	fake := drivetest.New()
	t.Cleanup(fake.Close)
	drivetest.SmallTree(fake)
	fake.AddComment("id-notes-fixture", "id-comment-fixture", "does this cover the second case?")
	fake.SetContent("id-budget-fixture", "first")
	fake.SetContent("id-budget-fixture", "second")
	fake.AddDrive("id-drive-empty", "Empty drive")
	fake.Drives["id-drive-marketing"].Restrictions.DomainUsersOnly = true
	fake.AddProposal("id-notes-fixture", "id-request-fixture", "outsider@example.org", "writer")
	return fake
}

// connect connects a client on protocol to a server over a fresh fake.
// A nil p declares no elicitation; opts adjust the client further.
func connect(t *testing.T, cfg config.Config, protocol string, p *person, opts ...func(*mcp.ClientOptions)) (*mcp.ClientSession, *drivetest.Server) {
	t.Helper()
	srv, fake := askingServer(t, cfg)
	return connectTo(t, srv, protocol, p, opts...), fake
}

// askingServer is a server over a fresh fake.
func askingServer(t *testing.T, cfg config.Config) (*mcp.Server, *drivetest.Server) {
	t.Helper()
	fake := fixtures(t)
	svc := service.New(drivetest.Client(t, fake), service.Options{
		ReadOnly: cfg.ReadOnly, Destructive: cfg.EnableDestructive, Sharing: cfg.Sharing,
		Now: func() time.Time { return time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC) },
	})
	return server.New(server.Deps{Service: svc, Config: cfg, Version: "test"}), fake
}

// connectTo connects one more client to srv, as connect does.
func connectTo(t *testing.T, srv *mcp.Server, protocol string, p *person, opts ...func(*mcp.ClientOptions)) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	o := &mcp.ClientOptions{}
	if p != nil {
		o.ElicitationHandler = p.handle
	}
	for _, fn := range opts {
		fn(o)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, o).
		Connect(context.Background(), ct, &mcp.ClientSessionOptions{ProtocolVersion: protocol})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// askCase is a call that clears a tool's own guards and reaches its
// write, the write as the fake records it, and words its question must
// carry.
type askCase struct {
	args   map[string]any
	method string
	path   string
	shows  []string
}

var askCases = map[string]askCase{
	"delete_file": {
		args:   map[string]any{"file": "id-budget-fixture", "confirm": true},
		method: http.MethodDelete, path: "/files/id-budget-fixture",
		shows: []string{"destroy the file `Budget.xlsx` for good, skipping the trash"},
	},
	"empty_trash": {
		args:   map[string]any{"confirm": true},
		method: http.MethodDelete, path: "/files/trash",
		shows: []string{"everything in this account's trash for good", "Drive's listing shows"},
	},
	"delete_drive": {
		args:   map[string]any{"drive": "id-drive-empty", "confirm": true},
		method: http.MethodDelete, path: "/drives/id-drive-empty",
		shows: []string{"the shared drive `Empty drive` for good"},
	},
	"delete_revision": {
		args:   map[string]any{"file": "id-budget-fixture", "revision": "", "confirm": true},
		method: http.MethodDelete, path: "/revisions/",
		shows: []string{"of `Budget.xlsx` for good"},
	},
	"delete_comment": {
		args:   map[string]any{"file": "id-notes-fixture", "comment": "id-comment-fixture", "confirm": true},
		method: http.MethodDelete, path: "/comments/id-comment-fixture",
		shows: []string{"a comment thread on `Meeting notes`", "`does this cover the second case?`"},
	},
	"share_file": {
		args:   map[string]any{"file": "id-budget-fixture", "principal": "anyone", "role": "reader", "allow_anyone": true},
		method: http.MethodPost, path: "/permissions",
		shows: []string{"let anyone with the link open `Budget.xlsx` as reader"},
	},
	"resolve_access_request": {
		args:   map[string]any{"file": "id-notes-fixture", "request": "id-request-fixture", "action": "accept"},
		method: http.MethodPost, path: ":resolve",
		shows: []string{"let `outsider@example.org` open `Meeting notes` as writer", "asked for it themselves"},
	},
	"manage_drive": {
		args: map[string]any{"action": "restrict", "drive": "Marketing",
			"restrictions": map[string]any{"domain_users_only": false}},
		method: http.MethodPatch, path: "/drives/id-drive-marketing",
		shows: []string{"turn off domain_users_only on the shared drive `Marketing`"},
	},
}

// argsFor is a case's arguments against fake, with the revision filled
// in from what the fake holds.
func argsFor(name string, fake *drivetest.Server) map[string]any {
	args := maps.Clone(askCases[name].args)
	if name == "delete_revision" {
		args["revision"] = fake.Revisions["id-budget-fixture"][0].ID
	}
	return args
}

// writes counts the calls that made a case's write.
func writes(fake *drivetest.Server, c askCase) int {
	n := 0
	for _, r := range fake.Requests {
		if r.Method == c.method && strings.Contains(r.Path, c.path) {
			n++
		}
	}
	return n
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func callTool(t *testing.T, cs *mcp.ClientSession, p *mcp.CallToolParams) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), p)
	if err != nil {
		t.Fatalf("calling %s: %v", p.Name, err)
	}
	return res
}

// Declined, nothing is written; accepted, the write is made once. On
// every protocol, for every tool that asks, and the question says what
// the write would do.
func TestEveryAskingWriteWaitsForThePerson(t *testing.T) {
	for name, c := range askCases {
		for _, protocol := range protocols {
			for _, action := range []string{"decline", "cancel", "accept"} {
				p := &person{action: action}
				cs, fake := connect(t, everything(), protocol, p)
				res := callTool(t, cs, &mcp.CallToolParams{Name: name, Arguments: argsFor(name, fake)})
				out := text(res)
				qs := p.asked()
				if len(qs) != 1 {
					t.Fatalf("%s %s %s: asked %d times: %s", name, protocol, action, len(qs), out)
				}
				for _, want := range c.shows {
					if !strings.Contains(qs[0].Message, want) {
						t.Errorf("%s: the question does not say %q:\n%s", name, want, qs[0].Message)
					}
				}
				n := writes(fake, c)
				if action != "accept" {
					if !res.IsError || !strings.HasPrefix(out, "[blocked]") || !strings.Contains(out, "not confirmed by the person") || n != 0 {
						t.Errorf("%s %s %s: %d writes: %s", name, protocol, action, n, out)
					}
					continue
				}
				if res.IsError || n != 1 {
					t.Errorf("%s %s accepted: %d writes: %s", name, protocol, n, out)
				}
			}
		}
	}
}

// Every tool that takes confirm asks, as do the two that widen access;
// the list is read from the published schemas, not typed out.
func TestEveryToolThatTakesConfirmAsks(t *testing.T) {
	cs, _ := connect(t, everything(), "", nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"share_file": true, "manage_drive": true, "resolve_access_request": true}
	registered := map[string]bool{}
	for _, tool := range res.Tools {
		registered[tool.Name] = true
		schema, _ := tool.InputSchema.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["confirm"]; ok {
			want[tool.Name] = true
		}
	}
	if len(want) < 7 {
		t.Fatalf("found %d asking tools; the schemas were not read", len(want))
	}
	for name := range want {
		if _, ok := askCases[name]; !ok {
			t.Errorf("%s takes confirm or widens access and has no asking case", name)
		}
	}
	for name := range askCases {
		if !want[name] || !registered[name] {
			t.Errorf("%s has an asking case and is not an asking tool", name)
		}
	}
}

// A client that cannot ask gets no question, and the arguments are the
// guard; GDRIVE_REQUIRE_PROMPT refuses the write instead.
func TestAClientThatCannotAsk(t *testing.T) {
	for _, require := range []bool{false, true} {
		cfg := everything()
		cfg.RequirePrompt = require
		cs, fake := connect(t, cfg, "", nil)
		c := askCases["delete_file"]
		res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_file", Arguments: argsFor("delete_file", fake)})
		n := writes(fake, c)
		switch {
		case require && (!res.IsError || !strings.Contains(text(res), "GDRIVE_REQUIRE_PROMPT") || n != 0):
			t.Errorf("required: %d writes: %s", n, text(res))
		case !require && (res.IsError || n != 1):
			t.Errorf("not required: %d writes: %s", n, text(res))
		}
	}
}

// A new grant to a person outside the account's organization asks,
// and one inside it does not.
func TestAShareOutsideTheOrganizationAsks(t *testing.T) {
	for _, tc := range []struct {
		who  string
		asks bool
	}{
		{"colleague@example.com", false},
		{"someone@example.org", true},
		{"group:team@example.net", true},
	} {
		p := &person{action: "decline"}
		cs, fake := connect(t, everything(), "2026-07-28", p)
		res := callTool(t, cs, &mcp.CallToolParams{Name: "share_file", Arguments: map[string]any{
			"file": "id-budget-fixture", "principal": tc.who, "role": "writer"}})
		asked := len(p.asked()) == 1
		if asked != tc.asks || res.IsError != tc.asks || (fake.Count(http.MethodPost) > 0) == tc.asks {
			t.Errorf("%s: asked %v, refused %v: %s", tc.who, asked, res.IsError, text(res))
		}
		if tc.asks && asked && !strings.Contains(p.asked()[0].Message, "outside this account's organization") {
			t.Errorf("%s: %s", tc.who, p.asked()[0].Message)
		}
	}
}

// A dry run, and a write that stays among people somebody named, ask
// nothing.
func TestWhatAsksNothing(t *testing.T) {
	p := &person{action: "decline"}
	cs, fake := connect(t, everything(), "2026-07-28", p)
	for _, call := range []*mcp.CallToolParams{
		{Name: "delete_file", Arguments: map[string]any{"file": "id-budget-fixture", "dry_run": true}},
		{Name: "delete_comment", Arguments: map[string]any{"file": "id-notes-fixture", "comment": "id-comment-fixture", "dry_run": true}},
		{Name: "share_file", Arguments: map[string]any{"file": "id-budget-fixture", "principal": "someone@example.com", "role": "reader"}},
		{Name: "resolve_access_request", Arguments: map[string]any{"file": "id-notes-fixture", "request": "id-request-fixture",
			"action": "accept", "dry_run": true}},
		{Name: "manage_drive", Arguments: map[string]any{"action": "restrict", "drive": "Marketing",
			"restrictions": map[string]any{"members_only": true}}},
		{Name: "manage_drive", Arguments: map[string]any{"action": "rename", "drive": "Marketing", "name": "Brand"}},
	} {
		if res := callTool(t, cs, call); res.IsError {
			t.Errorf("%s %v: %s", call.Name, call.Arguments, text(res))
		}
	}
	if qs := p.asked(); len(qs) != 0 {
		t.Errorf("asked %d questions: %s", len(qs), qs[0].Message)
	}
	if fake.Count(http.MethodDelete) != 0 {
		t.Error("a dry run deleted something")
	}
}

// mrtr connects a 2026-07-28 client that hands each question back
// instead of answering it, so a test can answer by hand.
func mrtr(t *testing.T) (*mcp.ClientSession, *drivetest.Server) {
	t.Helper()
	return connect(t, everything(), "2026-07-28", &person{action: "accept"}, func(o *mcp.ClientOptions) {
		o.MultiRoundTrip = &mcp.MultiRoundTripOptions{Disabled: true}
	})
}

var accepted = mcp.InputResponseMap{"confirm": &mcp.ElicitResult{Action: "accept"}}

// The first round only asks. The answer counts once, only with the state
// it was asked with, only for that call, and only while fresh.
func TestTheAnswerIsBoundToItsQuestion(t *testing.T) {
	cs, fake := mrtr(t)
	c := askCases["delete_file"]
	args := argsFor("delete_file", fake)
	first := callTool(t, cs, &mcp.CallToolParams{Name: "delete_file", Arguments: args})
	q, ok := first.InputRequests["confirm"].(*mcp.ElicitParams)
	if !first.NeedsInput() || !ok || q.Mode != "form" || first.RequestState == "" || writes(fake, c) != 0 {
		t.Fatalf("first round %+v; %d writes", first, writes(fake, c))
	}
	state := first.RequestState

	blocked := func(p *mcp.CallToolParams, want string) {
		t.Helper()
		res := callTool(t, cs, p)
		if out := text(res); !res.IsError || !strings.HasPrefix(out, "[blocked]") || !strings.Contains(out, want) {
			t.Errorf("%s", out)
		}
	}
	other := map[string]any{"file": "id-notes-fixture", "confirm": true}
	blocked(&mcp.CallToolParams{Name: "delete_file", Arguments: args, InputResponses: accepted},
		"answers to a question this server has not asked")
	blocked(&mcp.CallToolParams{Name: "delete_file", Arguments: args, InputResponses: accepted, RequestState: state + "x"},
		"did not ask")
	blocked(&mcp.CallToolParams{Name: "delete_file", Arguments: args, InputResponses: accepted,
		RequestState: "e30." + strings.Split(state, ".")[1]}, "did not ask")
	blocked(&mcp.CallToolParams{Name: "delete_file", Arguments: other, InputResponses: accepted, RequestState: state},
		"another call")
	blocked(&mcp.CallToolParams{Name: "empty_trash", Arguments: map[string]any{"confirm": true}, InputResponses: accepted,
		RequestState: state}, "another call")
	blocked(&mcp.CallToolParams{Name: "trash_file", Arguments: map[string]any{"file": "id-budget-fixture"},
		InputResponses: accepted, RequestState: state}, "not a tool here that asks the person")
	if writes(fake, c) != 0 || fake.Count("/trash") != 0 {
		t.Fatalf("%d writes before the answer", writes(fake, c))
	}

	done := callTool(t, cs, &mcp.CallToolParams{Name: "delete_file", Arguments: args, InputResponses: accepted, RequestState: state})
	if done.IsError || writes(fake, c) != 1 {
		t.Fatalf("the verified retry: %s; %d writes", text(done), writes(fake, c))
	}
	blocked(&mcp.CallToolParams{Name: "delete_file", Arguments: args, InputResponses: accepted, RequestState: state}, "already used")
}

// Any answer but an accept is refused before the call reads anything.
func TestARefusalIsRefusedBeforeAnyRead(t *testing.T) {
	for _, action := range []string{"decline", "cancel", "maybe"} {
		cs, fake := mrtr(t)
		args := argsFor("delete_comment", fake)
		first := callTool(t, cs, &mcp.CallToolParams{Name: "delete_comment", Arguments: args})
		before := len(fake.Requested())
		res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_comment", Arguments: args, RequestState: first.RequestState,
			InputResponses: mcp.InputResponseMap{"confirm": &mcp.ElicitResult{Action: action}}})
		if out := text(res); !res.IsError || !strings.HasPrefix(out, "[blocked]") || !strings.Contains(out, "not confirmed by the person") {
			t.Errorf("%s: %s", action, out)
		}
		if n := len(fake.Requested()); before == 0 || n != 0 {
			t.Errorf("%s: %d Drive calls after the answer (%d before)", action, n, before)
		}
	}
}

// What the person saw is what is written: a comment edited between the
// question and the answer is refused, and the next call asks again.
func TestAChangeAfterTheQuestionIsRefused(t *testing.T) {
	cs, fake := mrtr(t)
	c := askCases["delete_comment"]
	args := argsFor("delete_comment", fake)
	first := callTool(t, cs, &mcp.CallToolParams{Name: "delete_comment", Arguments: args})
	fake.Comments["id-notes-fixture"][0].Content = "something else entirely"
	res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_comment", Arguments: args, InputResponses: accepted,
		RequestState: first.RequestState})
	if out := text(res); !res.IsError || !strings.Contains(out, "changed after the person was asked") || writes(fake, c) != 0 {
		t.Errorf("%s; %d writes", out, writes(fake, c))
	}
}

// A state that travels through the client expires; one that stays in
// the process, before 2026-07-28, waits as long as the request does.
func TestALateAnswerIsRefusedOnlyWhenTheStateTravels(t *testing.T) {
	t.Cleanup(tools.SetAskTTL(-time.Minute))
	cs, fake := mrtr(t)
	c := askCases["delete_file"]
	args := argsFor("delete_file", fake)
	first := callTool(t, cs, &mcp.CallToolParams{Name: "delete_file", Arguments: args})
	res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_file", Arguments: args, InputResponses: accepted,
		RequestState: first.RequestState})
	if out := text(res); !res.IsError || !strings.Contains(out, "expired") || writes(fake, c) != 0 {
		t.Fatalf("%s; %d writes", out, writes(fake, c))
	}
	for _, protocol := range []string{"2025-06-18", "2025-11-25"} {
		cs, fake := connect(t, everything(), protocol, &person{action: "accept"})
		res := callTool(t, cs, &mcp.CallToolParams{Name: "delete_file", Arguments: argsFor("delete_file", fake)})
		if res.IsError || writes(fake, c) != 1 {
			t.Errorf("%s: a slow accept in the process was refused: %s", protocol, text(res))
		}
	}
}

// A tool that asks the person before every write carries Claude Code's
// requiresUserInteraction mark only for a client that cannot ask; with
// both, the person would answer twice for one call. The five are every
// tool here that both carries the mark and asks every time: a new name
// needs a look at whether it really asks every time. Each protocol lists
// on one server, the client that can ask first, so a mark dropped from
// the server's own tool rather than from a copy shows for the clients
// after it.
func TestTheMarkIsForAClientThatCannotAsk(t *testing.T) {
	marked := func(cs *mcp.ClientSession) string {
		t.Helper()
		res, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tool := range res.Tools {
			if tool.Meta["anthropic/requiresUserInteraction"] == true {
				out = append(out, tool.Name)
			}
		}
		slices.Sort(out)
		return strings.Join(out, " ")
	}
	urlAlone := func(o *mcp.ClientOptions) {
		o.Capabilities = &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{URL: &mcp.URLElicitationCapabilities{}}}
	}
	const all = "delete_comment delete_drive delete_file delete_revision empty_trash"
	for _, protocol := range protocols {
		srv, _ := askingServer(t, everything())
		for _, c := range []struct {
			name string
			p    *person
			opts []func(*mcp.ClientOptions)
			want string
		}{
			{"form", &person{action: "accept"}, nil, ""},
			{"url alone", &person{action: "accept"}, []func(*mcp.ClientOptions){urlAlone}, all},
			{"no elicitation", nil, nil, all},
		} {
			if got := marked(connectTo(t, srv, protocol, c.p, c.opts...)); got != c.want {
				t.Errorf("%s, %s: marked %q, want %q", protocol, c.name, got, c.want)
			}
		}
	}
}
