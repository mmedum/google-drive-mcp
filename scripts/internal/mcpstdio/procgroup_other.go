//go:build !unix && !windows

package mcpstdio

import "syscall"

// ownGroup leaves the server where it is: this platform has no process
// groups to put it in.
func ownGroup() *syscall.SysProcAttr { return nil }
