package main

import (
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
)

// A run that is stopped halfway still holds what it made: a grant to
// another person, a file in somebody's shared drive, a scratch shared
// drive, a scratch folder. Go's default for SIGINT, SIGTERM and SIGHUP is
// to exit on the spot, which leaves all of them. So the driver catches
// the three, starts no further step, and runs the cleanup it would have
// run anyway.
//
// The check sits in the session every write call goes through, so it
// holds for a call nobody has written yet. A stop unwinds the run with a
// panic that runWrites catches; every deferred cleanup on the way out
// runs, and the calls those cleanups make go through.

// stopSignals are the signals that stop a run and still clean up:
// Ctrl-C, a plain kill, and the terminal closing.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// stopper carries the first stop signal from the goroutine that catches
// it to the run, which looks before every call it sends. A nil stopper
// never stops.
type stopper struct {
	caught atomic.Pointer[os.Signal]
}

// signal is the stop signal caught, or nil.
func (s *stopper) signal() os.Signal {
	if s == nil {
		return nil
	}
	if sig := s.caught.Load(); sig != nil {
		return *sig
	}
	return nil
}

// set records sig unless an earlier one is already recorded. A second
// Ctrl-C changes nothing: the cleanup the first one started runs to its
// end.
func (s *stopper) set(sig os.Signal) {
	s.caught.CompareAndSwap(nil, &sig)
}

// catchStops starts catching the stop signals, and returns what the run
// reads them from and the function that stops catching them.
//
// A broken pipe is ignored for the same reason: closing a terminal also
// ends a `| tee` reading the driver's output, and a write to that pipe
// would otherwise kill the driver halfway through its cleanup.
func catchStops() (s *stopper, release func()) {
	s = &stopper{}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, stopSignals...)
	signal.Ignore(syscall.SIGPIPE)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-signals:
				s.set(sig)
			case <-done:
				return
			}
		}
	}()
	return s, func() {
		signal.Stop(signals)
		signal.Reset(syscall.SIGPIPE)
		close(done)
	}
}

// stopped is the panic that unwinds a write run after a stop signal.
type stopped struct{ sig os.Signal }

// stoppable is the session a write run sends every call through. It
// looks for a stop signal before each one.
type stoppable struct {
	session
	w *writeRun
}

func (s stoppable) CallTool(name string, args map[string]any) (string, bool, error) {
	s.w.checkStop()
	return s.session.CallTool(name, args)
}

func (s stoppable) ReadResource(uri string) (string, string, error) {
	s.w.checkStop()
	return s.session.ReadResource(uri)
}

// checkStop unwinds the run once a stop signal has arrived. A call made
// by a cleanup, or by a deferred one while the run unwinds, goes through.
func (w *writeRun) checkStop() {
	sig := w.stop.signal()
	if sig == nil || w.unwinding {
		return
	}
	if !w.stopNoticed {
		w.stopNoticed = true
		w.out.Sayf("\n!! STOPPED by %v: no further step starts, and what this run made is cleaned up now. "+
			"A second signal is ignored; Ctrl-\\ quits at once and leaves it all where it is.", sig)
	}
	if w.cleaning > 0 {
		return
	}
	w.unwinding = true
	panic(stopped{sig})
}

// cleanup runs one step that takes back what the run made. A stop
// signal does not interrupt it.
func (w *writeRun) cleanup(undo func()) {
	w.cleaning++
	defer func() { w.cleaning-- }()
	undo()
}

// untilStopped runs steps, and returns early when a stop signal unwinds
// them. Any other panic goes on up.
func (w *writeRun) untilStopped(steps func()) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(stopped); !ok {
				panic(r)
			}
		}
	}()
	steps()
}
