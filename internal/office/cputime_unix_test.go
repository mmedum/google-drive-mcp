//go:build unix

package office_test

import (
	"syscall"
	"time"
)

// cpuTime is the CPU time this process has spent, user and system.
func cpuTime() time.Duration {
	var u syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &u); err != nil {
		panic(err)
	}
	return time.Duration(u.Utime.Nano() + u.Stime.Nano())
}
