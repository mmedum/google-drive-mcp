// Command livedrive drives the built server over stdio against a real
// Google account, which is the check no fake can make: that the tools
// behave against Drive itself.
//
// Every result is printed with ids, links and addresses replaced by
// stable placeholders. What it cannot replace is the names of files,
// folders and shared drives: nothing distinguishes one from prose. Read
// a transcript before sharing it — the summary at the end says the same
// thing, every run.
//
//	go run ./scripts/livedrive -bin ./google-drive-mcp
//	go run ./scripts/livedrive -bin ./google-drive-mcp -file /Projects
//
// The read calls change nothing. -write adds the other half: it makes
// one scratch folder, exercises every tool that changes Drive inside it,
// and trashes it again. Nothing outside that folder is touched. Give
// -parent to say where the scratch folder goes.
//
//	go run ./scripts/livedrive -bin ./google-drive-mcp -write -parent /Scratch
//
// -share SOMEONE@EXAMPLE.COM adds the sharing calls that need a second
// person, including the ownership transfer of spike F. The transfer is
// made on a file created for it, and it CANNOT be undone from this
// account: trashing the scratch folder does not take back a file whose
// owner is now somebody else.
//
// -blocked PARTNER@EXAMPLE.ORG is the check §17a has been waiting
// for a Workspace administrator to make possible: a share the
// ORGANISATION refuses, rather than one Drive refuses. It reports
// Google's own reason beside the class this server gave it, and says
// loudly when the two disagree — a policy refusal reported as
// [forbidden] tells a model to try something else when the truth is that
// nothing it does will work.
//
//	go run ./scripts/livedrive -bin ./google-drive-mcp -write -blocked partner@example.org
//
// -raw turns redaction off. Do not use it in a terminal you are sharing.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mmedum/google-drive-mcp/scripts/internal/mcpstdio"
	"github.com/mmedum/google-drive-mcp/scripts/internal/redact"
)

func main() {
	binary := flag.String("bin", "./google-drive-mcp", "the server binary to drive")
	file := flag.String("file", "", "a file or folder reference to exercise get_file and a recursive listing against")
	raw := flag.Bool("raw", false, "print results without redaction (never in a shared terminal)")
	write := flag.Bool("write", false, "also exercise every tool that changes Drive, in one scratch folder that is trashed afterwards")
	parent := flag.String("parent", "", "where the scratch folder goes; defaults to the root of My Drive")
	drive := flag.String("drive", "", "a shared drive to move a file into and out of, by name or id; empty skips that half")
	share := flag.String("share", "", "an address to grant access to, for the half of sharing that needs a second person (spike F included); empty skips it")
	blocked := flag.String("blocked", "", "an address the organisation's own sharing policy refuses, to see a real [blocked] rather than an injected one (§17a); needs a Workspace administrator to have put it out of bounds")
	flag.Parse()

	if err := run(options{binary: *binary, file: *file, raw: *raw, write: *write,
		parent: *parent, drive: *drive, share: *share, blocked: *blocked}); err != nil {
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

// options are what one run of the driver was asked to do.
type options struct {
	binary  string
	file    string
	raw     bool
	write   bool
	parent  string
	drive   string
	share   string
	blocked string
}

func run(o options) error {
	red := redact.NewRedactor(o.raw)
	// The transfer tools need a local directory, and an MCP client passes
	// one only through the environment.
	dir, err := os.MkdirTemp("", "livedrive-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	sess, err := mcpstdio.Start(o.binary, "GDRIVE_LOCAL_DIR="+dir)
	if err != nil {
		return err
	}
	defer sess.Close()
	file := o.file

	proto, tools, err := sess.Initialize("livedrive")
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
	for _, c := range calls { //nolint:dupl // the read loop and the write loop print differently on purpose
		fmt.Printf("\n=== %s %s ===\n", c.tool, mcpstdio.Encode(c.args))
		if c.why != "" {
			fmt.Printf("(expecting a refusal: %s)\n", c.why)
		}
		text, isError, err := sess.CallTool(c.tool, c.args)
		if err != nil {
			return err
		}
		fmt.Println(strings.TrimRight(red.Do(text), "\n"))
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
	if o.write {
		failures, err := runWrites(sess, red, dir, o.parent, o.drive, o.share, o.blocked)
		unexpected += failures
		if err != nil {
			return err
		}
	} else {
		fmt.Println("(pass -write to exercise the tools that change Drive, in a scratch folder)")
	}
	if logs := sess.StderrTail(20); len(logs) > 0 {
		fmt.Println("\n=== stderr ===")
		for _, line := range logs {
			fmt.Println(red.Do(line))
		}
	}
	fmt.Println("\n" + red.Summary())
	if unexpected > 0 {
		return fmt.Errorf("%d call(s) did not behave as expected", unexpected)
	}
	fmt.Println("all calls behaved as expected")
	return nil
}
