package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/scripts/internal/mcpstdio"
	"github.com/mmedum/google-drive-mcp/scripts/internal/redact"
)

// harness is one run: a session with the server for setting a task up
// and reading the answer back, and the scratch folder everything happens
// inside.
type harness struct {
	sess *mcpstdio.Session
	red  *redact.Redactor
	// folder is the scratch folder's id; every task works inside it.
	folder string
	// dir is the local directory the transfer tools use.
	dir string
}

// call runs one tool through the server, for setting a task up and for
// reading the end state back. It is deliberately the same path the agent
// uses: a check that read the end state another way could pass while the
// tool it is checking was broken.
func (h *harness) call(tool string, args map[string]any) (string, error) {
	text, isError, err := h.sess.CallTool(tool, args)
	if err != nil {
		return "", err
	}
	if isError {
		return text, fmt.Errorf("%s refused: %s", tool, firstLine(text))
	}
	return text, nil
}

// mustCall is call for a setup step, where a failure means the task
// cannot be scored at all.
func (h *harness) mustCall(tool string, args map[string]any) (string, error) {
	out, err := h.call(tool, args)
	if err != nil {
		return "", fmt.Errorf("setting the task up: %w", err)
	}
	return out, nil
}

// idIn reads the "id: …" line a card carries. Every write and every
// get_file prints one, and it is how a setup step hands an id to the
// checks that come after.
var idLine = regexp.MustCompile(`(?m)^id:\s*(\S+)\s*$`)

func idIn(card string) string {
	m := idLine.FindStringSubmatch(card)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// run drives every task and prints the score.
func run(o options) error {
	tasks := selected(o.only)
	if len(tasks) == 0 {
		return fmt.Errorf("no task matched %q; the names are %s",
			strings.Join(o.only, ","), strings.Join(taskNames(), ", "))
	}
	if _, err := os.Stat(o.binary); err != nil {
		return fmt.Errorf("the server binary is not there: %w (run `make build`)", err)
	}

	dir, err := os.MkdirTemp("", "evals-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	sess, err := mcpstdio.Start(o.binary, "GDRIVE_LOCAL_DIR="+dir)
	if err != nil {
		return err
	}
	defer sess.Close()
	if _, _, err := sess.Initialize("evals"); err != nil {
		return err
	}
	h := &harness{sess: sess, red: redact.NewRedactor(o.raw), dir: dir}

	// One scratch folder for the whole run, named so that anybody who
	// finds it knows what it is and that it can go.
	name := fmt.Sprintf("google-drive-mcp evals %s (safe to delete)", time.Now().UTC().Format("2006-01-02 15:04:05"))
	made, err := h.mustCall("create_folder", map[string]any{"name": name, "parent": o.parent})
	if err != nil {
		return err
	}
	h.folder = idIn(made)
	if h.folder == "" {
		return errors.New("could not read the scratch folder's id out of create_folder's result")
	}
	fmt.Printf("scratch folder: %s\n", h.red.Do(h.folder))
	defer func() {
		if o.keep {
			fmt.Printf("\nthe scratch folder was left behind: %s\n", h.red.Do(h.folder))
			return
		}
		if _, err := h.call("trash_file", map[string]any{"file": h.folder}); err != nil {
			fmt.Printf("\n!! the scratch folder was NOT cleaned up (%v); trash it by hand: %s\n",
				err, h.red.Do(h.folder))
			return
		}
		fmt.Println("\nscratch folder trashed")
	}()

	config, err := mcpConfig(o.binary, dir)
	if err != nil {
		return err
	}

	passed, failed := 0, 0
	for _, t := range tasks {
		fmt.Printf("\n=== %s ===\n%s\n", t.name, t.prompt)
		problems := h.score(o, config, t)
		if len(problems) == 0 {
			passed++
			fmt.Println("PASS")
			continue
		}
		failed++
		fmt.Println("FAIL")
		for _, p := range problems {
			fmt.Println("  - " + h.red.Do(p))
		}
	}

	fmt.Printf("\n%d passed, %d failed, of %d\n", passed, failed, len(tasks))
	fmt.Println(h.red.Summary())
	if failed > 0 {
		return fmt.Errorf("%d task(s) failed", failed)
	}
	return nil
}

// score sets one task up, runs the agent, and applies the checks. The
// setup and the checks talk to the server directly; only the task itself
// goes through the agent.
func (h *harness) score(o options, config string, t task) []string {
	state := &taskState{harness: h}
	if t.setup != nil {
		if err := t.setup(state); err != nil {
			return []string{"setup failed: " + err.Error()}
		}
	}
	prompt := t.prompt
	for key, value := range state.values {
		prompt = strings.ReplaceAll(prompt, "{"+key+"}", value)
	}
	fmt.Println("→ " + h.red.Do(prompt))

	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	start := time.Now()
	run, err := ask(ctx, o, config, prompt)
	took := time.Since(start)
	if err != nil {
		return []string{"the agent failed: " + err.Error()}
	}
	fmt.Printf("← %s\n(%d tool calls in %s)\n", h.red.Do(strings.TrimSpace(run.Answer)), len(run.Calls), took.Round(time.Second))
	for _, c := range run.Calls {
		fmt.Printf("   %s %s\n", c.Name, h.red.Do(mcpstdio.Encode(c.Args)))
	}

	problems := commonChecks(state, run)
	if t.check != nil {
		problems = append(problems, t.check(state, run)...)
	}
	return problems
}
