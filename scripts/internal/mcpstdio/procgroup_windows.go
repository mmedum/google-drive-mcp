//go:build windows

package mcpstdio

import "syscall"

// ownGroup puts the server in a process group of its own, which Windows
// does not send a console's Ctrl-C to, so the driver can still use it to
// clean up.
func ownGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
