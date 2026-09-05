package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// callTimeout bounds one tool call against a real account.
const callTimeout = 2 * time.Minute

// session is one stdio conversation with the server.
type session struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  *bufio.Scanner
	nextID int

	mu     sync.Mutex
	stderr []string
}

// start launches the server. env adds to the process environment, which
// is how the local directory reaches it: an MCP client passes command,
// args and env, and nothing else.
func start(binary string, env ...string) (*session, error) {
	cmd := exec.Command(binary)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", binary, err)
	}
	s := &session{cmd: cmd, stdin: stdin, lines: bufio.NewScanner(stdout)}
	s.lines.Buffer(make([]byte, 0, 64*1024), 16<<20)
	go s.drainStderr(stderr)
	return s, nil
}

func (s *session) drainStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		s.mu.Lock()
		s.stderr = append(s.stderr, strings.TrimRight(scanner.Text(), "\r"))
		s.mu.Unlock()
	}
}

// stderrTail returns the last n log lines the server wrote.
func (s *session) stderrTail(n int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.stderr) <= n {
		return append([]string(nil), s.stderr...)
	}
	return append([]string(nil), s.stderr[len(s.stderr)-n:]...)
}

func (s *session) close() {
	_ = s.stdin.Close()
	_ = s.cmd.Wait()
}

// request sends one frame and reads until its reply arrives. Frames the
// server sends on its own account (logs, notifications) are skipped.
func (s *session) request(method string, params any) (map[string]any, error) {
	s.nextID++
	id := s.nextID
	frame := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		frame["params"] = params
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		return nil, err
	}
	if _, err := s.stdin.Write(append(raw, '\n')); err != nil {
		return nil, fmt.Errorf("write %s: %w", method, err)
	}

	deadline := time.Now().Add(callTimeout)
	for s.lines.Scan() {
		line := strings.TrimSpace(s.lines.Text())
		if line == "" {
			continue
		}
		var reply map[string]any
		if err := json.Unmarshal([]byte(line), &reply); err != nil {
			return nil, fmt.Errorf("stdout carried a line that is not JSON-RPC: %q", line)
		}
		if got, ok := reply["id"].(float64); ok && int(got) == id {
			if e, ok := reply["error"]; ok {
				return nil, fmt.Errorf("%s: %v", method, e)
			}
			return reply, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s: timed out", method)
		}
	}
	if err := s.lines.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%s: the server closed the connection; stderr:\n%s",
		method, strings.Join(s.stderrTail(20), "\n"))
}

func (s *session) notify(method string) error {
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	if err != nil {
		return err
	}
	_, err = s.stdin.Write(append(raw, '\n'))
	return err
}

// initialize completes the handshake and lists the tool surface.
func (s *session) initialize() (protocol string, tools []string, err error) {
	reply, err := s.request("initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "livedrive", "version": "0"},
	})
	if err != nil {
		return "", nil, err
	}
	result, _ := reply["result"].(map[string]any)
	protocol, _ = result["protocolVersion"].(string)
	if err := s.notify("notifications/initialized"); err != nil {
		return "", nil, err
	}

	listed, err := s.request("tools/list", nil)
	if err != nil {
		return "", nil, err
	}
	result, _ = listed["result"].(map[string]any)
	raw, _ := result["tools"].([]any)
	for _, t := range raw {
		if m, ok := t.(map[string]any); ok {
			if name, ok := m["name"].(string); ok {
				tools = append(tools, name)
			}
		}
	}
	return protocol, tools, nil
}

// callTool runs one tool and returns its text and whether it refused.
func (s *session) callTool(name string, args map[string]any) (text string, isError bool, err error) {
	reply, err := s.request("tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", false, err
	}
	result, _ := reply["result"].(map[string]any)
	isError, _ = result["isError"].(bool)
	var b strings.Builder
	content, _ := result["content"].([]any)
	for _, c := range content {
		if m, ok := c.(map[string]any); ok {
			if t, ok := m["text"].(string); ok {
				b.WriteString(t)
			}
		}
	}
	return b.String(), isError, nil
}

// encode renders arguments for the transcript heading.
func encode(args map[string]any) string {
	raw, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
