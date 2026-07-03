package sshutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRunner(t *testing.T) (*CommandRunner, func()) {
	t.Helper()
	addr, cleanup := startTestSSHServer(t)
	client := dialTestServer(t, addr)
	r := NewCommandRunner(client)
	return r, func() {
		r.Close()
		client.Close()
		cleanup()
	}
}

func spareCount(r *CommandRunner) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.spares)
}

func waitForSpares(t *testing.T, r *CommandRunner, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if spareCount(r) >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("spare pool never reached %d (have %d)", want, spareCount(r))
}

func TestCommandRunnerRun(t *testing.T) {
	r, cleanup := newTestRunner(t)
	defer cleanup()

	for range 3 { // repeat so both spare and fresh session paths are exercised
		stdout, stderr, code, err := r.Run("echo pong")
		require.Nil(t, err)
		assert.Equal(t, 0, code)
		assert.Equal(t, "pong\n", string(stdout))
		assert.Equal(t, 0, len(stderr))
	}
}

func TestCommandRunnerExitCode(t *testing.T) {
	r, cleanup := newTestRunner(t)
	defer cleanup()

	_, _, code, err := r.Run("exit 42")
	require.Nil(t, err)
	assert.Equal(t, 42, code)

	_, stderr, code, err := r.Run("fail")
	require.Nil(t, err)
	assert.Equal(t, 1, code)
	assert.Equal(t, "command failed\n", string(stderr))
}

func TestCommandRunnerRunStdin(t *testing.T) {
	r, cleanup := newTestRunner(t)
	defer cleanup()

	stdout, _, code, err := r.RunStdin("cat", []byte("hello spare"))
	require.Nil(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello spare", string(stdout))
}

func TestCommandRunnerRunStdinNil(t *testing.T) {
	r, cleanup := newTestRunner(t)
	defer cleanup()

	stdout, _, code, err := r.RunStdin("cat", nil)
	require.Nil(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, 0, len(stdout))
}

func TestCommandRunnerPrewarmsSpares(t *testing.T) {
	r, cleanup := newTestRunner(t)
	defer cleanup()
	// NewCommandRunner starts prewarming immediately, no Run needed.
	waitForSpares(t, r, spareTarget)
}

func TestCommandRunnerStaleSpareRetry(t *testing.T) {
	r, cleanup := newTestRunner(t)
	defer cleanup()

	// Wait for the full pool so the prewarm goroutine is quiescent and cannot
	// slip a healthy spare in after we sabotage the pool.
	waitForSpares(t, r, spareTarget)
	// Kill the spares behind the runner's back, simulating a server that tore
	// down the pre-opened channels. The next Run must transparently retry on
	// a fresh session.
	r.mu.Lock()
	for _, s := range r.spares {
		s.Close()
	}
	r.mu.Unlock()

	stdout, _, code, err := r.Run("echo pong")
	require.Nil(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "pong\n", string(stdout))
}

func TestCommandRunnerCloseReleasesSpares(t *testing.T) {
	r, cleanup := newTestRunner(t)
	defer cleanup()

	waitForSpares(t, r, 1)
	r.Close()
	assert.Equal(t, 0, spareCount(r))

	// Run still works after Close (falls back to on-demand sessions)...
	stdout, _, code, err := r.Run("echo pong")
	require.Nil(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "pong\n", string(stdout))

	// ...and does not resurrect the spare pool.
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, 0, spareCount(r))
}
