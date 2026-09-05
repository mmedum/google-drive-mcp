package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
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
		// What go-sdk v1.7.0 actually returns. Its sentinel lives in an
		// internal package, so this is matched by message; if the SDK
		// rewords it, this test is what says so.
		errors.New("server is closing: EOF"),
		fmt.Errorf("run: %w", errors.New("server is closing")),
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
	}
	for _, err := range broken {
		if isDisconnect(err) {
			t.Errorf("isDisconnect(%v) = true; a real failure must not be swallowed", err)
		}
	}
}
