//go:build unix

package mcpstdio

import (
	"os/exec"
	"syscall"
	"testing"
)

// A Ctrl-C signals the terminal's whole foreground process group. A
// server in the driver's group would exit with the driver's run, and the
// cleanup that runs after a stop signal would find nobody to talk to.
func TestTheServerRunsInAProcessGroupOfItsOwn(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to stand in for the server")
	}
	// sh with no arguments reads commands from stdin, so it waits on the
	// pipe the session holds, as the server does, until Close.
	s, err := Start(sh)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	group, err := syscall.Getpgid(s.cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if group == syscall.Getpgrp() {
		t.Errorf("the server is in the driver's process group %d, so a Ctrl-C stops it too", group)
	}
}
