package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// toolPrefix is what an MCP tool is called once a client has namespaced
// it. The name in the config below decides it.
const serverName = "gdrive"

const toolPrefix = "mcp__" + serverName + "__"

// agentRun is everything one task's agent produced: what it said, and
// every tool call it made on the way.
type agentRun struct {
	Answer string
	Calls  []toolCall
	// Text is the whole stream, for a transcript.
	Text string
}

// toolCall is one call the agent made, with the arguments it passed.
type toolCall struct {
	Name string
	Args map[string]any
}

// used reports whether a tool was called at all.
func (r agentRun) used(tool string) bool {
	for _, c := range r.Calls {
		if c.Name == tool {
			return true
		}
	}
	return false
}

// with returns every call to one tool.
func (r agentRun) with(tool string) []toolCall {
	var out []toolCall
	for _, c := range r.Calls {
		if c.Name == tool {
			out = append(out, c)
		}
	}
	return out
}

// arg reads a string argument, however the model spelled the value.
func (c toolCall) arg(name string) string {
	v, ok := c.Args[name]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// truthy reports whether an argument was passed as true. A model can
// pass a boolean or the word, and both mean the same thing to the
// server's JSON decoding.
func (c toolCall) truthy(name string) bool {
	switch v := c.Args[name].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	default:
		return false
	}
}

// mcpConfig is the client configuration the agent is given: this server
// and nothing else. -strict-mcp-config makes sure nothing the person
// running this happens to have configured joins in.
func mcpConfig(binary, localDir string) (string, error) {
	cfg := map[string]any{"mcpServers": map[string]any{
		serverName: map[string]any{
			"command": binary,
			// The features behind a flag are on, or the agent never sees
			// their tools and a task about them scores nothing.
			"env": map[string]string{
				"GDRIVE_LOCAL_DIR": localDir,
				"GDRIVE_LABELS":    "true",
				"GDRIVE_ACTIVITY":  "true",
			},
		},
	}}
	raw, err := json.Marshal(cfg)
	return string(raw), err
}

// ask runs one task through the agent and returns what it did.
//
// The agent gets this server's tools and nothing else: no Bash, no file
// tools, no web. That is the point of the exercise — an eval that let
// the model shell out would be scoring the shell.
func ask(ctx context.Context, o options, config, prompt string) (agentRun, error) {
	args := []string{
		"-p", prompt,
		"--output-format", "stream-json", "--verbose",
		"--mcp-config", config, "--strict-mcp-config",
		// Everything from this server, and nothing else at all.
		"--allowedTools", toolPrefix + "*",
		"--disallowedTools", "Bash,Edit,Write,Read,WebFetch,WebSearch,Task,Glob,Grep",
		"--permission-mode", "dontAsk",
	}
	if o.model != "" {
		args = append(args, "--model", o.model)
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agentRun{}, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return agentRun{}, fmt.Errorf("start claude: %w", err)
	}

	run, parseErr := parseStream(stdout)
	waitErr := cmd.Wait()
	if waitErr != nil {
		return run, fmt.Errorf("claude: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	if parseErr != nil {
		return run, parseErr
	}
	return run, nil
}

// parseStream reads the agent's stream-json output: one JSON object per
// line. Only two shapes matter — an assistant message, whose content may
// hold tool uses, and the final result — and an unknown shape is skipped
// rather than failed on, because the stream gains event types over time
// and an eval harness that breaks on a new one is an eval harness nobody
// runs.
func parseStream(r io.Reader) (agentRun, error) {
	var out agentRun
	lines := bufio.NewScanner(r)
	lines.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		if line == "" {
			continue
		}
		out.Text += line + "\n"
		var event struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			Result  string `json:"result"`
			IsError bool   `json:"is_error"`
			Message struct {
				Content []struct {
					Type  string         `json:"type"`
					Name  string         `json:"name"`
					Input map[string]any `json:"input"`
					Text  string         `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		switch event.Type {
		case "assistant":
			for _, c := range event.Message.Content {
				if c.Type == "tool_use" {
					out.Calls = append(out.Calls, toolCall{
						Name: strings.TrimPrefix(c.Name, toolPrefix), Args: c.Input,
					})
				}
			}
		case "result":
			out.Answer = event.Result
			if event.IsError {
				return out, errors.New("the agent ended with an error: " + event.Subtype)
			}
		}
	}
	if err := lines.Err(); err != nil {
		return out, err
	}
	if out.Answer == "" && len(out.Calls) == 0 {
		return out, errors.New("the agent produced no answer and made no tool call")
	}
	return out, nil
}
