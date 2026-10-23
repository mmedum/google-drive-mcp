//go:build unix

package mcpstdio

import "syscall"

// ownGroup puts the server in a process group of its own. A Ctrl-C, or
// a terminal closing, signals the whole foreground group, and a server
// in it would exit before the driver could use it to clean up.
func ownGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
