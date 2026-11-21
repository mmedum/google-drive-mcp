package office_test

import (
	"syscall"
	"time"
)

// cpuTime is the CPU time this process has spent, user and kernel.
func cpuTime() time.Duration {
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		panic(err)
	}
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		panic(err)
	}
	// A Filetime counts 100-nanosecond intervals; Windows updates these
	// times once a scheduler tick, so they are coarse.
	ticks := func(f syscall.Filetime) int64 { return int64(f.HighDateTime)<<32 | int64(f.LowDateTime) }
	return time.Duration((ticks(kernel) + ticks(user)) * 100)
}
