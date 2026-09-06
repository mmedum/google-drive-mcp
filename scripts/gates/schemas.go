package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// schemaDump is the shape --dump-schemas writes.
type schemaDump struct {
	Server string `json:"server"`
	Tools  []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		InputSchema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		} `json:"inputSchema"`
	} `json:"tools"`
}

func (d schemaDump) names() []string {
	out := make([]string, 0, len(d.Tools))
	for _, t := range d.Tools {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

// dumpSchemas runs a build of the server and decodes its tool surface.
func dumpSchemas(binary string, env ...string) (schemaDump, []byte, error) {
	cmd := exec.Command(binary, "--dump-schemas")
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	raw, err := cmd.Output()
	if err != nil {
		return schemaDump{}, nil, fmt.Errorf("%s --dump-schemas: %w", binary, err)
	}
	var d schemaDump
	if err := json.Unmarshal(raw, &d); err != nil {
		return schemaDump{}, nil, fmt.Errorf("decode schemas from %s: %w", binary, err)
	}
	return d, raw, nil
}

// schemaDiff compares the tool surface with the last tag's. A removed
// tool, a removed field or a new required field is breaking, because a
// client written against the old surface stops working.
func schemaDiff(out io.Writer, args []string) error {
	binary := arg(args, 0, "./google-drive-mcp")
	current, raw, err := dumpSchemas(binary)
	if err != nil {
		return err
	}
	if err := os.WriteFile("schemas.json", raw, 0o644); err != nil {
		return fmt.Errorf("write schemas.json: %w", err)
	}

	last := lastTag()
	if last == "" {
		_, _ = fmt.Fprintln(out, "no previous tag; schemas.json written")
		return nil
	}

	old, cleanup, err := buildTag(last)
	if err != nil {
		return err
	}
	defer cleanup()
	previous, _, err := dumpSchemas(old)
	if err != nil {
		return err
	}

	var breaking, added []string
	oldTools := map[string]int{}
	for i, t := range previous.Tools {
		oldTools[t.Name] = i
	}
	newTools := map[string]int{}
	for i, t := range current.Tools {
		newTools[t.Name] = i
	}
	for name, i := range oldTools {
		j, ok := newTools[name]
		if !ok {
			breaking = append(breaking, "tool removed: "+name)
			continue
		}
		was, now := previous.Tools[i], current.Tools[j]
		required := map[string]bool{}
		for _, f := range was.InputSchema.Required {
			required[f] = true
		}
		for _, f := range now.InputSchema.Required {
			if !required[f] {
				breaking = append(breaking, name+": new required field "+f)
			}
		}
		for field := range was.InputSchema.Properties {
			if _, ok := now.InputSchema.Properties[field]; !ok {
				breaking = append(breaking, name+": field removed "+field)
			}
		}
	}
	for name := range newTools {
		if _, ok := oldTools[name]; !ok {
			added = append(added, name)
		}
	}
	sort.Strings(added)
	sort.Strings(breaking)

	_, _ = fmt.Fprintf(out, "added tools: %s\n", listOr(added, "none"))
	_, _ = fmt.Fprintf(out, "breaking changes: %s\n", listOr(breaking, "none"))
	if len(breaking) > 0 {
		return fmt.Errorf("%d breaking change(s) since %s", len(breaking), last)
	}
	return nil
}

func listOr(items []string, fallback string) string {
	if len(items) == 0 {
		return fallback
	}
	return strings.Join(items, ", ")
}

// lastTag returns the most recent tag, or "" when there is none. Having
// no tags is the normal state before the first release, so it is not an
// error and this cannot fail.
func lastTag() string {
	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// buildTag checks a tag out into a scratch worktree and builds it, so the
// comparison is against what that tag actually shipped rather than
// against a description of it.
func buildTag(tag string) (binary string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "gates-schema-*")
	if err != nil {
		return "", nil, err
	}
	done := func() { _ = os.RemoveAll(dir) }

	src := filepath.Join(dir, "src")
	if out, err := exec.Command("git", "worktree", "add", "-q", "--detach", src, tag).CombinedOutput(); err != nil {
		done()
		return "", nil, fmt.Errorf("git worktree add %s: %w: %s", tag, err, out)
	}
	remove := func() {
		_ = exec.Command("git", "worktree", "remove", "-f", src).Run()
		done()
	}

	binary = filepath.Join(dir, "old")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/google-drive-mcp")
	build.Dir = src
	if out, err := build.CombinedOutput(); err != nil {
		remove()
		return "", nil, fmt.Errorf("build %s: %w: %s", tag, err, out)
	}
	// The worktree has served its purpose; the binary outlives it.
	_ = exec.Command("git", "worktree", "remove", "-f", src).Run()
	return binary, done, nil
}
