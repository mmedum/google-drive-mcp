// Command evals runs an agent against this server and scores what it
// did, which is the check no unit test can make: whether the tool
// surface leads a model to the right call.
//
// Each task is a sentence a person might say, given to `claude -p` with
// ONLY this server's tools available. The task is scored twice: on the
// end state, read back through the server, and on the trace — which
// tools were called with which arguments. A task can pass on its answer
// and fail on its trace, and that is the interesting case: a model that
// guessed an id and happened to be right, or passed allow_anyone nobody
// asked for.
//
// It needs a signed-in account and the `claude` command on PATH. Every
// task runs inside one scratch folder, which is trashed at the end;
// nothing outside it is touched.
//
//	go run ./scripts/evals -bin ./google-drive-mcp
//	go run ./scripts/evals -bin ./google-drive-mcp -only share,duplicate
//	go run ./scripts/evals -bin ./google-drive-mcp -keep     # leave the folder
//
// Output is redacted the way the live driver's is: ids, links and
// addresses become placeholders. File names cannot be, and the summary
// says so.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	binary := flag.String("bin", "./google-drive-mcp", "the server binary the agent talks to")
	parent := flag.String("parent", "", "where the scratch folder goes; defaults to the root of My Drive")
	only := flag.String("only", "", "run only these tasks, by name, comma-separated")
	model := flag.String("model", "", "the model to run the tasks with; empty uses the CLI's default")
	keep := flag.Bool("keep", false, "leave the scratch folder behind, to look at what happened")
	raw := flag.Bool("raw", false, "print results without redaction (never in a shared terminal)")
	timeout := flag.Duration("timeout", 5*time.Minute, "how long one task may take")
	flag.Parse()

	o := options{
		binary: *binary, parent: *parent, model: *model, keep: *keep, raw: *raw, timeout: *timeout,
	}
	if *only != "" {
		o.only = strings.Split(*only, ",")
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "evals: "+err.Error())
		os.Exit(1)
	}
}

type options struct {
	binary  string
	parent  string
	only    []string
	model   string
	keep    bool
	raw     bool
	timeout time.Duration
}
