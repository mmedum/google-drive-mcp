//go:build !unix && !windows

package office_test

import "time"

var started = time.Now()

// cpuTime falls back to the wall clock where no process CPU time is read.
func cpuTime() time.Duration { return time.Since(started) }
