package sshutil

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoundedReturnsAFinishedCommand(t *testing.T) {
	aborted := false
	res := bounded(time.Minute, func() { aborted = true }, func() CommandResult {
		return CommandResult{Stdout: []byte("out"), ExitCode: 0, Started: true}
	})
	assert.Equal(t, "out", string(res.Stdout))
	assert.NoError(t, res.Err)
	assert.False(t, aborted, "a command that finished must not be torn down")
}

// The deadline has to release the caller, and it has to abort the session --
// without the abort the command keeps a slot and the next caller waits behind
// it.
func TestBoundedAbortsAtTheDeadline(t *testing.T) {
	release := make(chan struct{})
	aborted := make(chan struct{})

	start := time.Now()
	res := bounded(20*time.Millisecond, func() { close(aborted) }, func() CommandResult {
		<-release
		return CommandResult{Stdout: []byte("too late"), Started: true}
	})
	assert.Less(t, time.Since(start), 5*time.Second, "the caller must be released at the deadline")

	require.Error(t, res.Err)
	assert.True(t, errors.Is(res.Err, ErrTimeout))
	assert.Contains(t, res.Err.Error(), "may still be running on the remote host")
	assert.Equal(t, -1, res.ExitCode)
	assert.True(t, res.Started, "an expired command ran, so it must never be retried")
	assert.Empty(t, res.Stdout)

	select {
	case <-aborted:
	case <-time.After(5 * time.Second):
		t.Fatal("the deadline did not abort the session")
	}
	close(release)
}

func TestBoundedWithoutADeadlineWaits(t *testing.T) {
	res := bounded(0, func() { t.Error("nothing to abort") }, func() CommandResult {
		time.Sleep(10 * time.Millisecond)
		return CommandResult{Stdout: []byte("waited"), Started: true}
	})
	assert.Equal(t, "waited", string(res.Stdout))
}
