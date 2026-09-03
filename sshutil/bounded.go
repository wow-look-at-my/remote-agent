package sshutil

import (
	"errors"
	"fmt"
	"time"
)

// A remote command that never finishes must not hold its caller forever: the
// MCP server answers requests in arrival order, so a wedged command takes the
// whole session with it. Every transport therefore runs a command under a
// bound and tears the session down when it expires.

// ErrTimeout reports a command abandoned at its deadline.
var ErrTimeout = errors.New("timed out")

// CommandResult is a finished command, as the Runner methods return it.
type CommandResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Err      error
	// Started reports whether the server accepted the exec request. False means
	// the command never began, so another session may safely retry it.
	Started bool
}

// bounded runs a command through run and gives up after d, calling abort so
// the session is torn down rather than left holding a slot. A d that is not
// positive runs it unbounded. On expiry the abandoned run drains into the
// buffered channel; nothing waits for it, because the point of the deadline
// is that the command may never end.
func bounded(d time.Duration, abort func(), run func() CommandResult) CommandResult {
	if d <= 0 {
		return run()
	}
	done := make(chan CommandResult, 1)
	go func() { done <- run() }()

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case res := <-done:
		return res
	case <-timer.C:
		abort()
		// The remote process is the remote's to reap: closing the session frees
		// this side, and says nothing about a command that ignores its hangup.
		return CommandResult{
			ExitCode: -1,
			Started:  true, // it ran, which is why it hit the deadline: never retry it
			Err:      fmt.Errorf("command %w after %s; it may still be running on the remote host", ErrTimeout, d),
		}
	}
}
