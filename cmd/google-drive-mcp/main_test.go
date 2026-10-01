package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmedum/google-drive-mcp/v2/internal/userconfig"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestIsDisconnect(t *testing.T) {
	// A stdio session ends when the client goes away. Every one of these
	// is that, and none of them is a failure worth a non-zero exit: a
	// client reads one as a crash.
	clean := []error{
		context.Canceled,
		io.EOF,
		io.ErrUnexpectedEOF,
		fmt.Errorf("reading frame: %w", io.EOF),
		// What go-sdk v1.7.0 actually returns: a wire error carrying the
		// code, wrapped with the EOF as text. Matching the code rather
		// than the message means a reworded message changes nothing.
		fmt.Errorf("%w: EOF", &jsonrpc.Error{Code: -32004, Message: "server is closing"}),
		fmt.Errorf("run: %w", &jsonrpc.Error{Code: -32003, Message: "client is closing"}),
	}
	for _, err := range clean {
		if !isDisconnect(err) {
			t.Errorf("isDisconnect(%v) = false, want true", err)
		}
	}

	broken := []error{
		errors.New("write /dev/stdout: no space left on device"),
		errors.New("json: unsupported value"),
		context.DeadlineExceeded,
		// A wire error that is not a shutdown must still be a failure.
		fmt.Errorf("%w", &jsonrpc.Error{Code: -32603, Message: "internal error"}),
		// And a message that merely reads like one, with no code, is not
		// enough: this is what the string check used to accept.
		errors.New("the server is closing time at the pub"),
	}
	for _, err := range broken {
		if isDisconnect(err) {
			t.Errorf("isDisconnect(%v) = true; a real failure must not be swallowed", err)
		}
	}
}

// A mistyped subcommand and a flag reach the default path the same way,
// and the leading dash is all that tells them apart.
//
// This drives run() rather than a predicate. The predicate version of
// this test passed with the guard deleted from main entirely, which is
// the whole reason main was reshaped to take its arguments and streams:
// a test that cannot fail when the behavior is removed is not holding
// the behavior.
func TestAnUnknownCommandIsReported(t *testing.T) {
	for _, arg := range []string{"statsu", "zzz-bogus", "Status"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{arg}, &stdout, &stderr); code == 0 {
			t.Errorf("%q exited 0; a typo is indistinguishable from a correct invocation", arg)
		}
		if !strings.Contains(stderr.String(), arg) {
			t.Errorf("%q: stderr does not name the command: %s", arg, stderr.String())
		}
		if !strings.Contains(stderr.String(), "Usage:") {
			t.Errorf("%q: no usage on stderr: %s", arg, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Errorf("%q wrote to stdout, which carries only MCP frames: %s", arg, stdout.String())
		}
	}
}

// The guard keys on a leading dash, which is one typo away from
// rejecting a real flag. Every documented invocation that needs no
// credentials is held here; rejecting too much is as quiet as rejecting
// too little.
func TestDocumentedInvocationsStillReachTheirCommand(t *testing.T) {
	for _, arg := range []string{"--version", "-version", "--dump-schemas", "help", "--help", "-h"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{arg}, &stdout, &stderr); code != 0 {
			t.Errorf("%q exited %d, want 0: %s", arg, code, stderr.String())
		}
		if strings.Contains(stderr.String(), "unknown command") {
			t.Errorf("%q was rejected as an unknown command", arg)
		}
	}
}

// A profile file that cannot be read stops the command. Carrying on
// would report the profile as unconfigured, or let login overwrite a
// file the person may still want.
func TestACorruptProfileFileStopsTheCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(userconfig.EnvDir, dir)
	t.Setenv("GDRIVE_PROFILE", "corrupt-profile-gate")
	path, err := userconfig.Path("corrupt-profile-gate")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"status"}, &stdout, &stderr); code != 1 {
		t.Errorf("status over a corrupt profile exited %d, want 1; stdout: %s", code, stdout.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("status printed a report over a profile it could not read:\n%s", stdout.String())
	}
}
