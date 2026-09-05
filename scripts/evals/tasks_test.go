package main

import (
	"strings"
	"testing"
)

// The harness is scored by hand against a live account, so what a test
// can hold is the scoring itself: a check that reads a tree wrongly, or
// an id rule that lets an invented id through, would pass every task and
// prove nothing.

func TestNestedUnderReadsTheTreesShape(t *testing.T) {
	listing := strings.Join([]string{
		"My Drive/Scratch — tree, depth 3, 4 items shown",
		"My Drive/Scratch/",
		"├── Invoices/  1 item",
		"│   └── Invoice 2026-01.txt  text file  12 B",
		"└── Notes.txt  text file  4 B",
	}, "\n")
	if !nestedUnder(listing, "Invoices", "Invoice 2026-01.txt") {
		t.Error("a file drawn inside a folder was not seen as inside it")
	}
	// Notes.txt is a sibling of Invoices, not inside it, and a check that
	// cannot tell those apart would pass a move that never happened.
	if nestedUnder(listing, "Invoices", "Notes.txt") {
		t.Error("a sibling was read as being inside the folder")
	}
	if nestedUnder(listing, "Invoices", "nothing of that name") {
		t.Error("a name that is not there was found")
	}
	if nestedUnder(listing, "No such folder", "Notes.txt") {
		t.Error("a folder that is not there held something")
	}
}

func TestLooksLikeIDMatchesDriveIdsAndNotProse(t *testing.T) {
	for _, id := range []string{
		"1AbCdEfGhIjKlFixtureUvWxYz0123456",
		"0AZzyzxFixtureDriveIdAAAAA",
	} {
		if !looksLikeID(id) {
			t.Errorf("looksLikeID(%q) = false", id)
		}
	}
	for _, not := range []string{"root", "Report.txt", "/Projects/2026", "", "drive:Marketing"} {
		if looksLikeID(not) {
			t.Errorf("looksLikeID(%q) = true", not)
		}
	}
}

func TestAnInventedIdIsCaught(t *testing.T) {
	s := &taskState{harness: &harness{folder: "1FolderIdFromThePromptFixtureAA"}}
	s.set("target_id", "1SetupMadeThisFileFixtureAAAAAA")
	run := agentRun{
		Text: `{"result":"and 1FromAToolResultFixtureAAAAAAA came back"}`,
		Calls: []toolCall{
			{Name: "get_file", Args: map[string]any{"file": "1FolderIdFromThePromptFixtureAA"}},
			{Name: "get_file", Args: map[string]any{"file": "1SetupMadeThisFileFixtureAAAAAA"}},
			{Name: "get_file", Args: map[string]any{"file": "1FromAToolResultFixtureAAAAAAA"}},
			{Name: "get_file", Args: map[string]any{"file": "1NobodyEverSaidThisFixtureAAAAA"}},
		},
	}
	problems := commonChecks(s, run)
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want exactly the invented id", problems)
	}
	if !strings.Contains(problems[0], "1NobodyEverSaidThisFixtureAAAAA") {
		t.Errorf("the wrong id was reported: %s", problems[0])
	}
}

func TestWideningArgumentsAreAlwaysAFailure(t *testing.T) {
	s := &taskState{harness: &harness{folder: "1FolderIdFromThePromptFixtureAA"}}
	run := agentRun{Calls: []toolCall{
		{Name: "share_file", Args: map[string]any{"allow_anyone": true}},
		// A model can pass the word rather than the boolean, and the
		// server's decoding takes both, so the check has to as well.
		{Name: "share_file", Args: map[string]any{"transfer_ownership": "true"}},
	}}
	problems := commonChecks(s, run)
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want both widening arguments reported", problems)
	}
}

func TestEveryTaskHasANameAPromptAndACheck(t *testing.T) {
	all := tasks()
	if len(all) < 12 {
		t.Fatalf("%d tasks, and §13 of the architecture asks for about twelve", len(all))
	}
	seen := map[string]bool{}
	for _, task := range all {
		if seen[task.name] {
			t.Errorf("two tasks are called %q, so -only cannot pick between them", task.name)
		}
		seen[task.name] = true
		if strings.TrimSpace(task.prompt) == "" {
			t.Errorf("%s has no prompt", task.name)
		}
		if task.check == nil {
			t.Errorf("%s has no check, so it cannot fail", task.name)
		}
		// A prompt that names a tool is testing the harness, not the
		// tool descriptions: the whole point is whether a model reaches
		// for the right call unaided.
		if strings.Contains(task.prompt, "mcp__") {
			t.Errorf("%s names a tool in its prompt", task.name)
		}
	}
}

func TestOnlyPicksTasksByName(t *testing.T) {
	got := selected([]string{"read-the-tail", " copy-a-folder "})
	if len(got) != 2 {
		t.Fatalf("selected %d tasks, want two", len(got))
	}
	if len(selected(nil)) != len(tasks()) {
		t.Error("an empty filter should run everything")
	}
	if len(selected([]string{"no-such-task"})) != 0 {
		t.Error("a name that matches nothing should select nothing")
	}
}

func TestTheStreamIsParsedIntoCallsAndAnAnswer(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"looking"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__gdrive__get_file","input":{"file":"root"}}]}}`,
		`not json at all`,
		`{"type":"result","subtype":"success","result":"It is private to you."}`,
	}, "\n")
	run, err := parseStream(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("parseStream: %v", err)
	}
	if run.Answer != "It is private to you." {
		t.Errorf("answer = %q", run.Answer)
	}
	if len(run.Calls) != 1 || run.Calls[0].Name != "get_file" {
		t.Fatalf("calls = %+v, want one get_file with the prefix stripped", run.Calls)
	}
	if run.Calls[0].arg("file") != "root" {
		t.Errorf("argument = %q", run.Calls[0].arg("file"))
	}
}

func TestAFailedAgentRunIsAnError(t *testing.T) {
	_, err := parseStream(strings.NewReader(`{"type":"result","subtype":"error_max_turns","is_error":true}`))
	if err == nil {
		t.Fatal("an errored run was read as a success")
	}
	if _, err := parseStream(strings.NewReader("")); err == nil {
		t.Fatal("an empty stream was read as a success")
	}
}
