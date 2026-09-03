package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// findCall returns the recorded call for a command.
func findCall(t *testing.T, mock *mockRunner, command string) mockCall {
	t.Helper()
	for _, c := range mock.snapshotCalls() {
		if c.Command == command {
			return c
		}
	}
	t.Fatalf("no call recorded for %q", command)
	return mockCall{}
}

// A command with no deadline of its own still gets one: an MCP server answers
// in arrival order, so one command that never ends takes the session with it.
func TestExecCarriesTheDefaultDeadline(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("tail -f /var/log/syslog", nil, 0)

	resp := h.handleExec(map[string]any{"command": "tail -f /var/log/syslog"})
	require.True(t, resp.OK)
	assert.Equal(t, protocol.ExecDefaultTimeout, findCall(t, mock, "tail -f /var/log/syslog").Timeout)
}

func TestExecCarriesTheDeadlineItWasGiven(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("make", nil, 0)

	resp := h.handleExec(map[string]any{"command": "make", "timeout": float64(90)})
	require.True(t, resp.OK)
	assert.Equal(t, 90*time.Second, findCall(t, mock, "make").Timeout)
}

func TestExecRejectsADeadlineItCannotHonor(t *testing.T) {
	for name, timeout := range map[string]float64{
		"negative":       -1,
		"above the cap":  protocol.ExecMaxTimeout.Seconds() + 1,
		"far above it":   86400,
		"barely over it": protocol.ExecMaxTimeout.Seconds() + 0.5,
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := newTestHandler()
			resp := h.handleExec(map[string]any{"command": "echo hi", "timeout": timeout})
			assert.False(t, resp.OK)
			assert.Contains(t, resp.Error, "timeout")
		})
	}
}

// The helper path is an argument like any other: a remote home directory with a
// space in it splits into two words unquoted, and the command names a file that
// is not there. see docs/ape.md
func TestHelperCommandsQuoteTheBinaryPath(t *testing.T) {
	mock := newMockRunner()
	d := &Daemon{runner: mock, remotePath: "/home/ann smith/.cache/remote-agent/agent-ab12cd"}
	h := &Handler{daemon: d}

	h.handlePs(map[string]any{})
	h.handleSysinfo()
	h.handleGlob(map[string]any{"pattern": "*.go"})
	h.handleGrep(map[string]any{"pattern": "func"})
	h.handleEdit(map[string]any{"path": "/srv/x", "old": "a", "new": "b"})
	d.auditAsync("exec", "echo hi")
	d.auditWG.Wait()

	calls := mock.snapshotCalls()
	require.NotEmpty(t, calls)
	for _, c := range calls {
		if !strings.Contains(c.Command, "serve ") {
			continue
		}
		assert.True(t, strings.HasPrefix(c.Command, "'/home/ann smith/.cache/remote-agent/agent-ab12cd' serve "),
			"the helper path must reach the remote shell as one word: %s", c.Command)
	}
}
