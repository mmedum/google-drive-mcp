package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mmedum/google-drive-mcp/internal/service"
)

// smokeTimeout bounds one session with the server.
const smokeTimeout = 20 * time.Second

// syntheticFileID is a file id of the right shape that names nothing.
// Everything in this repository's fixtures is invented.
const syntheticFileID = "1SyntheticFixtureFileIdAAAAAAAAAAAA"

// protocolVersions are the newest version the SDK knows and the one it
// negotiates down to. go-sdk v1.7.0 answers every initialize with
// 2025-11-25 — initialize is deprecated in 2026-07-28, so the handshake
// caps there by design — but both requests must produce a session.
var protocolVersions = []string{"2026-07-28", "2025-11-25"}

// negotiatedVersion is what the server may answer with.
const negotiatedVersion = "2025-11-25"

// smoke drives the built binary over stdio without credentials. It is
// the check that the shipped artifact speaks the protocol at all: every
// tool must answer with an [auth] tool error rather than crash, exit, or
// let the server die at startup, because a server that exits shows the
// person "failed to connect" and the model never learns why.
func smoke(out io.Writer, args []string) error {
	binary := arg(args, 0, "./google-drive-mcp")
	configDir, err := os.MkdirTemp("", "gates-smoke-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(configDir) }()
	env := append(os.Environ(), "GDRIVE_CONFIG_DIR="+configDir, "GDRIVE_LOG_LEVEL=error")

	for _, proto := range protocolVersions {
		frames := []string{
			request(1, "initialize", map[string]any{
				"protocolVersion": proto,
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "smoke", "version": "0"},
			}),
			notification("notifications/initialized"),
			request(2, "tools/list", nil),
			request(3, "tools/call", map[string]any{
				"name": "get_file", "arguments": map[string]any{"file": syntheticFileID},
			}),
			request(4, "resources/templates/list", nil),
			request(5, "resources/read", map[string]any{"uri": service.Scheme + syntheticFileID}),
		}
		replies, err := drive(binary, env, frames, []int{1, 2, 3, 4, 5})
		if err != nil {
			return fmt.Errorf("%s: %w", proto, err)
		}
		if err := checkSession(proto, replies); err != nil {
			return err
		}
	}

	// A configuration that removes tools must still serve the rest.
	readOnly := append(append([]string(nil), env...), "GDRIVE_READ_ONLY=true")
	replies, err := drive(binary, readOnly, []string{
		request(1, "initialize", map[string]any{
			"protocolVersion": negotiatedVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "smoke", "version": "0"},
		}),
		notification("notifications/initialized"),
		request(2, "tools/list", nil),
	}, []int{1, 2})
	if err != nil {
		return fmt.Errorf("read-only mode: %w", err)
	}
	names := toolNames(replies[2])
	if _, ok := names["get_file"]; !ok {
		return fmt.Errorf("read-only mode dropped a read tool")
	}
	// The gate is really about the other direction: a mode that removes
	// tools must actually remove them.
	for _, write := range []string{"create_file", "upload_file", "trash_file", "move_file"} {
		if _, ok := names[write]; ok {
			return fmt.Errorf("read-only mode registered %s", write)
		}
	}

	if err := checkAbruptDisconnect(binary, env); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(out, "stdio smoke ok")
	return nil
}

// checkAbruptDisconnect closes stdin while the server is still working.
// A client that goes away mid-request is how a session ends when the
// person quits their editor, and the server must treat it as the end of
// the conversation rather than a failure: a non-zero exit here shows up
// in the client's log as a crash.
func checkAbruptDisconnect(binary string, env []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), smokeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary)
	cmd.Env = env
	// The reader reaches EOF the instant the frame is read, so stdin is
	// gone before the answer is written.
	cmd.Stdin = strings.NewReader(request(1, "initialize", map[string]any{
		"protocolVersion": negotiatedVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "smoke", "version": "0"},
	}) + "\n")
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("a client disconnecting mid-request must be a clean exit: %w\nstderr:\n%s", err, stderr.String())
	}
	return nil
}

// checkSession asserts what every client depends on.
func checkSession(proto string, replies map[int]map[string]any) error {
	init, ok := replies[1]
	if !ok {
		return fmt.Errorf("%s: no initialize response", proto)
	}
	result, _ := init["result"].(map[string]any)
	if got, _ := result["protocolVersion"].(string); got != negotiatedVersion {
		return fmt.Errorf("%s: server negotiated %q, want %q", proto, got, negotiatedVersion)
	}

	names := toolNames(replies[2])
	for _, want := range []string{"get_account", "get_file", "search_files", "list_folder"} {
		if _, ok := names[want]; !ok {
			return fmt.Errorf("%s: %s missing from tools/list", proto, want)
		}
	}

	call, ok := replies[3]
	if !ok {
		return fmt.Errorf("%s: no response to the tool call", proto)
	}
	callResult, _ := call["result"].(map[string]any)
	if isError, _ := callResult["isError"].(bool); !isError {
		return fmt.Errorf("%s: a tool call without credentials should be a tool error", proto)
	}
	if text := resultText(callResult); !strings.Contains(text, "[auth]") {
		return fmt.Errorf("%s: tool error should carry the [auth] class, got %q", proto, text)
	}
	templates, ok := replies[4]
	if !ok {
		return fmt.Errorf("%s: no resources/templates/list response", proto)
	}
	listed := templateURIs(templates)
	for _, want := range []string{"gdrive://{file}", "gdrive://{file}/meta", "gdrive://{folder}/children"} {
		if !listed[want] {
			return fmt.Errorf("%s: %s missing from resources/templates/list", proto, want)
		}
	}
	// A resource read without credentials must fail the way a tool call
	// does: with the [auth] class and a live server, not a crash. A
	// resource read is a JSON-RPC error rather than a result carrying
	// isError, which is the one shape difference between the two.
	read, ok := replies[5]
	if !ok {
		return fmt.Errorf("%s: no resources/read response", proto)
	}
	rpcErr, _ := read["error"].(map[string]any)
	message, _ := rpcErr["message"].(string)
	if !strings.Contains(message, "[auth]") {
		return fmt.Errorf("%s: a resource read without credentials should carry the [auth] class, got %q",
			proto, message)
	}
	return nil
}

func templateURIs(reply map[string]any) map[string]bool {
	out := map[string]bool{}
	result, _ := reply["result"].(map[string]any)
	templates, _ := result["resourceTemplates"].([]any)
	for _, t := range templates {
		if m, ok := t.(map[string]any); ok {
			if uri, ok := m["uriTemplate"].(string); ok {
				out[uri] = true
			}
		}
	}
	return out
}

func toolNames(reply map[string]any) map[string]bool {
	out := map[string]bool{}
	result, _ := reply["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	for _, t := range tools {
		if m, ok := t.(map[string]any); ok {
			if name, ok := m["name"].(string); ok {
				out[name] = true
			}
		}
	}
	return out
}

func resultText(result map[string]any) string {
	var b strings.Builder
	content, _ := result["content"].([]any)
	for _, c := range content {
		if m, ok := c.(map[string]any); ok {
			if text, ok := m["text"].(string); ok {
				b.WriteString(text)
			}
		}
	}
	return b.String()
}

func request(id int, method string, params any) string {
	frame := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		frame["params"] = params
	}
	return encode(frame)
}

func notification(method string) string {
	return encode(map[string]any{"jsonrpc": "2.0", "method": method})
}

func encode(frame map[string]any) string {
	raw, err := json.Marshal(frame)
	if err != nil {
		panic(err) // the frames are literals; a failure here is a bug in this file
	}
	return string(raw)
}

// drive feeds the frames to the server over stdio and returns the
// responses by id, keeping stdin open until every expected reply has
// arrived — which is what a real client does, and what a server needs in
// order to answer at all.
//
// Every line of stdout must be a JSON-RPC frame: a stray print there
// breaks the protocol for every client, and is the single most common
// defect in servers of this kind. The exit status has to be zero too: a
// client disconnecting is how every stdio session ends, and a non-zero
// exit shows up in the client's log as a crash.
func drive(binary string, env, frames []string, wantIDs []int) (map[int]map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), smokeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary)
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", binary, err)
	}

	if _, err := io.WriteString(stdin, strings.Join(frames, "\n")+"\n"); err != nil {
		_ = stdin.Close()
		// Wait first, then report what the server said. A write that
		// fails here fails because the server has already gone, so the
		// error in hand is a broken pipe and the reason is in stderr —
		// and this returned the pipe alone, discarding the panic that
		// explained it. A sibling repository found the same in its port.
		_ = cmd.Wait()
		return nil, fmt.Errorf("write frames: %w\nstderr:\n%s", err, stderr.String())
	}

	want := make(map[int]bool, len(wantIDs))
	for _, id := range wantIDs {
		want[id] = true
	}
	replies := map[int]map[string]any{}
	scanErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var frame map[string]any
			if err := json.Unmarshal([]byte(line), &frame); err != nil {
				scanErr <- fmt.Errorf("stdout carried a line that is not JSON-RPC: %q", line)
				return
			}
			if id, ok := frame["id"].(float64); ok {
				replies[int(id)] = frame
				delete(want, int(id))
				if len(want) == 0 {
					scanErr <- nil
					return
				}
			}
		}
		scanErr <- scanner.Err()
	}()

	select {
	case err := <-scanErr:
		if err != nil {
			_ = stdin.Close()
			_ = cmd.Wait()
			return nil, err
		}
	case <-ctx.Done():
		_ = stdin.Close()
		_ = cmd.Wait()
		return nil, fmt.Errorf("timed out waiting for %d response(s); stderr:\n%s", len(want), stderr.String())
	}

	// Closing stdin is the client hanging up: the server must notice and
	// exit cleanly.
	if err := stdin.Close(); err != nil {
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("server exited non-zero after the client disconnected: %w\nstderr:\n%s", err, stderr.String())
	}
	return replies, nil
}
