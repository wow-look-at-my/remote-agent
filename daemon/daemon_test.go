package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/protocol"
	"github.com/wow-look-at-my/remote-agent/sshutil"
)

func TestSocketPath(t *testing.T) {
	path := SocketPath("user@host.example.com")
	assert.NotEqual(t, "", path)
	assert.Equal(t, path, SocketPath("user@host.example.com"))
	assert.NotEqual(t, path, SocketPath("other@host.example.com"))

}

func TestPIDPath(t *testing.T) {
	path := PIDPath("user@host.example.com")
	assert.NotEqual(t, "", path)
	assert.Equal(t, path, PIDPath("user@host.example.com"))

}

func TestSocketAndPIDPathDiffer(t *testing.T) {
	target := "user@host.example.com"
	assert.NotEqual(t, PIDPath(target), SocketPath(target))

}

func TestParseTarget(t *testing.T) {
	tests := []struct {
		input    string
		wantUser string
		wantHost string
	}{
		{"admin@server.com", "admin", "server.com"},
		{"root@10.0.0.1", "root", "10.0.0.1"},
		{"deploy@my-host", "deploy", "my-host"},
	}
	for _, tt := range tests {
		user, host, err := parseTarget(tt.input)
		assert.Nil(t, err)
		assert.Equal(t, tt.wantUser, user)
		assert.Equal(t, tt.wantHost, host)

	}
}

func TestParseTargetNoUser(t *testing.T) {
	user, host, err := parseTarget("server.com")
	require.Nil(t, err)
	assert.Equal(t, "server.com", host)
	assert.NotEqual(t, "", user)

}

func TestShellEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"with spaces", "'with spaces'"},
		{"it's", "'it'\"'\"'s'"},
		{"", "''"},
		{"hello world", "'hello world'"},
	}
	for _, tt := range tests {
		got := shellEscape(tt.input)
		assert.Equal(t, tt.want, got)

	}
}

func TestRandomSuffix(t *testing.T) {
	s1 := randomSuffix()
	s2 := randomSuffix()
	assert.Equal(t, 8, len(s1))
	assert.NotEqual(t, s2, s1)

}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	pidPath := filepath.Join(dir, "test.pid")

	// Create files to clean up
	os.WriteFile(pidPath, []byte("12345"), 0644)
	l, err := net.Listen("unix", sockPath)
	require.Nil(t, err)

	d := &Daemon{
		listener: l,
		sockPath: sockPath,
		pidPath:  pidPath,
	}
	d.cleanup()

	_, err = os.Stat(sockPath)
	assert.True(t, os.IsNotExist(err))

	_, err = os.Stat(pidPath)
	assert.True(t, os.IsNotExist(err))

}

func TestCleanupNilListener(t *testing.T) {
	dir := t.TempDir()
	d := &Daemon{
		sockPath: filepath.Join(dir, "nonexistent.sock"),
		pidPath:  filepath.Join(dir, "nonexistent.pid"),
	}
	// Should not panic
	d.cleanup()
}

func TestShutdownWithRunner(t *testing.T) {
	mock := newMockRunner()
	d := &Daemon{
		runner:     mock,
		remotePath: "/tmp/.remote-agent-test",
		sockPath:   filepath.Join(t.TempDir(), "test.sock"),
		pidPath:    filepath.Join(t.TempDir(), "test.pid"),
	}
	d.shutdown()
	assert.

		// Verify audit and cleanup commands were run
		GreaterOrEqual(t, len(mock.calls), 2)

}

// slowAuditRunner delays audit commands and records completion order, so the
// test can prove shutdown drains in-flight async audits before proceeding.
type slowAuditRunner struct {
	mu        sync.Mutex
	completed []string
	delay     time.Duration
}

func (r *slowAuditRunner) Run(command string) (stdout, stderr []byte, exitCode int, err error) {
	if strings.Contains(command, "serve audit --action 'exec'") {
		time.Sleep(r.delay)
	}
	r.mu.Lock()
	r.completed = append(r.completed, command)
	r.mu.Unlock()
	return nil, nil, 0, nil
}

func (r *slowAuditRunner) RunStdin(command string, stdin []byte) (stdout, stderr []byte, exitCode int, err error) {
	return r.Run(command)
}

func TestShutdownDrainsPendingAudits(t *testing.T) {
	runner := &slowAuditRunner{delay: 50 * time.Millisecond}
	d := &Daemon{
		runner:     runner,
		remotePath: "/tmp/.remote-agent-test",
		sockPath:   filepath.Join(t.TempDir(), "test.sock"),
		pidPath:    filepath.Join(t.TempDir(), "test.pid"),
	}
	h := &Handler{daemon: d}

	resp := h.handleExec(map[string]any{"command": "whoami"})
	require.True(t, resp.OK)

	// The exec audit is still sleeping in its goroutine here. shutdown must
	// wait for it before writing the shutdown audit entry.
	d.shutdown()

	runner.mu.Lock()
	defer runner.mu.Unlock()
	execAuditIdx, shutdownAuditIdx := -1, -1
	for i, c := range runner.completed {
		if strings.Contains(c, "serve audit --action 'exec'") {
			execAuditIdx = i
		}
		if strings.Contains(c, "serve audit --action shutdown") {
			shutdownAuditIdx = i
		}
	}
	require.NotEqual(t, -1, execAuditIdx, "exec audit was lost: %v", runner.completed)
	require.NotEqual(t, -1, shutdownAuditIdx, "shutdown audit missing: %v", runner.completed)
	assert.Less(t, execAuditIdx, shutdownAuditIdx, "shutdown audit must come after drained exec audit")
}

func TestShutdownNilRunner(t *testing.T) {
	d := &Daemon{
		sockPath: filepath.Join(t.TempDir(), "test.sock"),
		pidPath:  filepath.Join(t.TempDir(), "test.pid"),
	}
	// Should not panic
	d.shutdown()
}

func TestHandleClient(t *testing.T) {
	mock := newMockRunner()
	mock.onCommand("echo pong", []byte("pong\n"), 0)

	d := &Daemon{
		runner:     mock,
		remotePath: "/tmp/.remote-agent-test",
	}

	// Create a pair of connected sockets
	server, client := net.Pipe()

	go func() {
		// Send request
		json.NewEncoder(client).Encode(protocol.DaemonRequest{Action: "ping"})
		// Read response
		var resp protocol.DaemonResponse
		json.NewDecoder(client).Decode(&resp)
		client.Close()
	}()

	d.handleClient(server)
}

func TestHandleClientInvalidJSON(t *testing.T) {
	d := &Daemon{
		runner:     newMockRunner(),
		remotePath: "/tmp/.remote-agent-test",
	}

	server, client := net.Pipe()

	go func() {
		client.Write([]byte("not json\n"))
		var resp protocol.DaemonResponse
		json.NewDecoder(client).Decode(&resp)
		assert.NotEqual(t, "", resp.Error)

		client.Close()
	}()

	d.handleClient(server)
}

func TestFindDeployBinary(t *testing.T) {
	// This test depends on the environment - we just verify it doesn't panic
	_, err := findDeployBinary()
	// May succeed (finds our test binary) or fail (no linux-amd64 variant)
	_ = err
}

func TestSshRunner(t *testing.T) {
	// Verify sshutil.CommandRunner satisfies the daemon's Runner seam.
	var _ Runner = (*sshutil.CommandRunner)(nil)
}

// startPingListener stands up a Unix socket that accepts one connection and
// replies to a request with a valid DaemonResponse, using the same JSON framing
// the real daemon uses. It returns a cleanup that closes the listener.
func startPingListener(t *testing.T, sockPath string) func() {
	t.Helper()
	l, err := net.Listen("unix", sockPath)
	require.Nil(t, err)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var req protocol.DaemonRequest
		if json.NewDecoder(conn).Decode(&req) != nil {
			return
		}
		json.NewEncoder(conn).Encode(protocol.DaemonResponse{OK: true, Data: protocol.PingResult{Pong: true}})
	}()

	return func() { l.Close() }
}

func TestPingSocketAlive(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "alive.sock")
	cleanup := startPingListener(t, sockPath)
	defer cleanup()

	assert.True(t, pingSocket(sockPath))
}

func TestPingSocketNoSocket(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "does-not-exist.sock")
	assert.False(t, pingSocket(sockPath))
}

func TestPingSocketStale(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "stale.sock")
	// Create the socket file, then close the listener so nothing accepts.
	l, err := net.Listen("unix", sockPath)
	require.Nil(t, err)
	l.Close()

	assert.False(t, pingSocket(sockPath))
}

func TestTouchActivity(t *testing.T) {
	d := &Daemon{}
	assert.True(t, d.lastActivity.IsZero())

	d.touchActivity()
	assert.False(t, d.lastActivity.IsZero())
}

func TestWatchIdleShutdown(t *testing.T) {
	oldTimeout, oldInterval, oldExit := idleTimeout, idleCheckInterval, exitFunc
	defer func() {
		idleTimeout, idleCheckInterval, exitFunc = oldTimeout, oldInterval, oldExit
	}()

	idleTimeout = 1 * time.Millisecond
	idleCheckInterval = 10 * time.Millisecond

	done := make(chan struct{})
	var once sync.Once
	exitFunc = func(code int) { once.Do(func() { close(done) }) }

	d := &Daemon{
		sockPath:     filepath.Join(t.TempDir(), "idle.sock"),
		pidPath:      filepath.Join(t.TempDir(), "idle.pid"),
		lastActivity: time.Now().Add(-1 * time.Hour),
	}

	go d.watchIdle()

	select {
	case <-done:
		// shutdown fired as expected
	case <-time.After(2 * time.Second):
		t.Fatal("watchIdle did not trigger shutdown within timeout")
	}
}

func TestHandleClientUpdatesActivity(t *testing.T) {
	mock := newMockRunner()
	mock.onCommand("echo pong", []byte("pong\n"), 0)
	d := &Daemon{
		runner:     mock,
		remotePath: "/tmp/.remote-agent-test",
	}

	server, client := net.Pipe()
	go func() {
		json.NewEncoder(client).Encode(protocol.DaemonRequest{Action: "ping"})
		var resp protocol.DaemonResponse
		json.NewDecoder(client).Decode(&resp)
		client.Close()
	}()

	d.handleClient(server)

	d.mu.Lock()
	last := d.lastActivity
	d.mu.Unlock()
	assert.False(t, last.IsZero())
}
