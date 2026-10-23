//go:build unix

package main

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// Ctrl-C, a plain kill and the terminal closing are each caught rather
// than left to Go's default, which exits on the spot and strands
// everything the run made. A signal not caught kills this test binary.
func TestTheStopSignalsAreCaught(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			stop, release := catchStops()
			defer release()
			if err := syscall.Kill(os.Getpid(), sig); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for stop.signal() == nil && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if got := stop.signal(); got != sig {
				t.Errorf("caught %v, want %v", got, sig)
			}
		})
	}
}
