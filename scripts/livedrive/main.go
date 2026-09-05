// Command livedrive drives the built server over stdio against a real
// Google account, which is the check no fake can make: that the tools
// behave against Drive itself.
//
// Every result is printed with ids, links and addresses replaced by
// stable placeholders, so a transcript can go into an issue or a commit
// message without leaking anything. Nothing here writes to Drive: this
// phase registers only read tools.
//
//	go run ./scripts/livedrive -bin ./google-drive-mcp
//	go run ./scripts/livedrive -bin ./google-drive-mcp -file /Projects
//
// -raw turns redaction off. Do not use it in a terminal you are sharing.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	binary := flag.String("bin", "./google-drive-mcp", "the server binary to drive")
	file := flag.String("file", "", "a file or folder reference to exercise get_file and a recursive listing against")
	raw := flag.Bool("raw", false, "print results without redaction (never in a shared terminal)")
	flag.Parse()

	if err := run(*binary, *file, *raw); err != nil {
		fmt.Fprintln(os.Stderr, "livedrive: "+err.Error())
		os.Exit(1)
	}
}

// call is one tool invocation and what it is expected to do. A refusal
// that is expected proves as much as a success: it is how the server
// says no to something it should not do.
type call struct {
	tool        string
	args        map[string]any
	expectError bool
	why         string
}

func run(binary, file string, raw bool) error {
	redact := NewRedactor(raw)
	session, err := start(binary)
	if err != nil {
		return err
	}
	defer session.close()

	proto, tools, err := session.initialize()
	if err != nil {
		return err
	}
	fmt.Println("protocol:", proto)
	fmt.Println("tools:   ", strings.Join(tools, ", "))

	calls := []call{
		{tool: "get_account", args: map[string]any{}},
		{tool: "list_folder", args: map[string]any{"folder": "root", "page_size": 10}},
		{tool: "search_files", args: map[string]any{"kind": "folder", "limit": 5, "order_by": "modified"}},
		{tool: "get_file", args: map[string]any{"file": "https://example.com/not-a-drive-link"},
			expectError: true, why: "a link that is not Drive's"},
		{tool: "search_files", args: map[string]any{},
			expectError: true, why: "a search with no criteria"},
		{tool: "get_file", args: map[string]any{"file": "1SyntheticFixtureFileIdAAAAAAAAAAAA"},
			expectError: true, why: "an id that names nothing"},
	}
	if file != "" {
		calls = append(calls,
			call{tool: "get_file", args: map[string]any{"file": file}},
			call{tool: "list_folder", args: map[string]any{"folder": file, "recursive": true, "max_depth": 2}},
		)
	}

	unexpected := 0
	for _, c := range calls {
		fmt.Printf("\n=== %s %s ===\n", c.tool, encode(c.args))
		if c.why != "" {
			fmt.Printf("(expecting a refusal: %s)\n", c.why)
		}
		text, isError, err := session.callTool(c.tool, c.args)
		if err != nil {
			return err
		}
		fmt.Println(strings.TrimRight(redact.Do(text), "\n"))
		if isError != c.expectError {
			unexpected++
			if c.expectError {
				fmt.Println("!! expected a refusal and did not get one")
			} else {
				fmt.Println("!! unexpected tool error")
			}
		}
	}

	if file == "" {
		fmt.Println("\n(pass -file REF to also exercise get_file and a recursive listing)")
	}
	if logs := session.stderrTail(20); len(logs) > 0 {
		fmt.Println("\n=== stderr ===")
		for _, line := range logs {
			fmt.Println(redact.Do(line))
		}
	}
	fmt.Println("\n" + redact.Summary())
	if unexpected > 0 {
		return fmt.Errorf("%d call(s) did not behave as expected", unexpected)
	}
	fmt.Println("all calls behaved as expected")
	return nil
}
