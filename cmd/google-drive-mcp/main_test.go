package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

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

// A mistyped subcommand and a flag reach the default path the same way, and
// the leading dash is all that tells them apart. Both directions are covered
// because both are quiet when wrong: reject too much and every documented
// flag stops working, reject too little and a typo starts the server and
// reports success.
func TestLooksLikeSubcommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		arg  string
		want bool
	}{
		{"a mistyped subcommand", "statsu", true},
		{"a subcommand that does not exist", "zzz-bogus", true},
		{"the right word in the wrong case", "Status", true},
		{"a flag the server parses", "--dump-schemas", false},
		{"the single-dash spelling Go also accepts", "-version", false},
		{"a flag carrying a value", "-profile=work", false},
		{"no argument at all", "", false},
	} {
		if got := looksLikeSubcommand(tc.arg); got != tc.want {
			t.Errorf("%s: looksLikeSubcommand(%q) = %t, want %t", tc.name, tc.arg, got, tc.want)
		}
	}
}
