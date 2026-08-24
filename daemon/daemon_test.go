package daemon

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

// Several endpoints behind one user@host, on different ports, are the whole
// reason the port is part of the key.
func TestSocketPathSeparatesPorts(t *testing.T) {
	bare := SocketPath("root@127.0.0.1")
	first := SocketPath("root@127.0.0.1:2201")
	second := SocketPath("root@127.0.0.1:2202")

	assert.NotEqual(t, first, second, "two ports on one host must not share a daemon socket")
	assert.NotEqual(t, bare, first)
	assert.NotEqual(t, bare, second)

	// The same holds for the PID file and the target record beside the socket.
	assert.NotEqual(t, PIDPath("root@127.0.0.1:2201"), PIDPath("root@127.0.0.1:2202"))
	assert.NotEqual(t, TargetPath("root@127.0.0.1:2201"), TargetPath("root@127.0.0.1:2202"))
}

// Every spelling of one endpoint keys on the same socket, whichever way the
// port arrived.
func TestSocketPathCanonicalizes(t *testing.T) {
	merged, err := CanonicalTarget("root@127.0.0.1", 2201)
	require.NoError(t, err)
	assert.Equal(t, SocketPath("root@127.0.0.1:2201"), SocketPath(merged))
	assert.Equal(t, SocketPath("root@[::1]:2201"), SocketPath(" root@[::1]:2201 "))
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

// Start refuses a target and a --port that name two different endpoints. It
// fails before it dials, so the test needs no SSH host.
func TestStartRejectsConflictingPorts(t *testing.T) {
	err := Start(StartOptions{Target: "root@127.0.0.1:2201", Port: 2202})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2202")

	err = Start(StartOptions{Target: "root@127.0.0.1:bad"})
	assert.ErrorContains(t, err, "bad port")
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
		ep, err := ParseTarget(tt.input)
		assert.Nil(t, err)
		assert.Equal(t, tt.wantUser, ep.User)
		assert.Equal(t, tt.wantHost, ep.Host)
		assert.Equal(t, 0, ep.Port)
	}
}

func TestParseTargetNoUser(t *testing.T) {
	ep, err := ParseTarget("server.com")
	require.Nil(t, err)
	assert.Equal(t, "server.com", ep.Host)
	assert.Equal(t, "", ep.User)
	assert.NotEqual(t, "", ep.Login())
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

// Delays audits and records the order, to prove shutdown drains them first.
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

	// The exec audit still sleeps here, and shutdown must wait for it.
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

func deployTestFixture() (data []byte, cachePath string) {
	data = []byte("fake-helper-binary")
	sum := sha256.Sum256(data)
	return data, fmt.Sprintf("/home/u/.cache/remote-agent/agent-%x", sum[:8])
}

func TestDeployBinaryDataUploadsWhenNotCached(t *testing.T) {
	mock := newMockRunner()
	data, wantPath := deployTestFixture()

	mock.onCommand(`printf %s "$HOME"`, []byte("/home/u"), 0)
	mock.onCommandErr(fmt.Sprintf("sha256sum '%s' 2>/dev/null", wantPath), []byte(""), 1)

	path, reused, err := deployBinaryData(mock, data)
	require.Nil(t, err)
	assert.False(t, reused)
	assert.Equal(t, wantPath, path)
	assert.True(t, cachedDeploy(path))

	// stdin to a temp path, chmod, then mv: a concurrent connect never sees a partial file.
	uploads := 0
	for _, c := range mock.snapshotCalls() {
		if c.Stdin != nil {
			uploads++
			assert.Equal(t, data, c.Stdin)
			assert.Contains(t, c.Command, "mkdir -p '/home/u/.cache/remote-agent'")
			assert.Contains(t, c.Command, "chmod 700")
			assert.Contains(t, c.Command, fmt.Sprintf("mv -f"))
			assert.Contains(t, c.Command, fmt.Sprintf("'%s'", wantPath))
		}
	}
	assert.Equal(t, 1, uploads, "exactly one upload expected")
}

func TestDeployBinaryDataReusesCachedBinary(t *testing.T) {
	mock := newMockRunner()
	data, wantPath := deployTestFixture()
	sum := sha256.Sum256(data)

	mock.onCommand(`printf %s "$HOME"`, []byte("/home/u"), 0)
	mock.onCommand(fmt.Sprintf("sha256sum '%s' 2>/dev/null", wantPath),
		[]byte(fmt.Sprintf("%x  %s\n", sum, wantPath)), 0)

	path, reused, err := deployBinaryData(mock, data)
	require.Nil(t, err)
	assert.True(t, reused)
	assert.Equal(t, wantPath, path)

	for _, c := range mock.snapshotCalls() {
		assert.Nil(t, c.Stdin, "cache hit must not upload anything, ran: %s", c.Command)
	}
}

func TestDeployBinaryDataHashMismatchReuploads(t *testing.T) {
	mock := newMockRunner()
	data, wantPath := deployTestFixture()

	mock.onCommand(`printf %s "$HOME"`, []byte("/home/u"), 0)
	// A file exists at the cache path but its content differs (stale or
	// corrupt): it must be replaced, not trusted.
	mock.onCommand(fmt.Sprintf("sha256sum '%s' 2>/dev/null", wantPath),
		[]byte(fmt.Sprintf("deadbeef  %s\n", wantPath)), 0)

	path, reused, err := deployBinaryData(mock, data)
	require.Nil(t, err)
	assert.False(t, reused)
	assert.Equal(t, wantPath, path)

	uploads := 0
	for _, c := range mock.snapshotCalls() {
		if c.Stdin != nil {
			uploads++
			assert.Equal(t, data, c.Stdin)
		}
	}
	assert.Equal(t, 1, uploads)
}

func TestDeployBinaryDataNoHomeFallsBackToTmp(t *testing.T) {
	mock := newMockRunner()
	mock.onCommandErr(`printf %s "$HOME"`, []byte("no home"), 1)

	path, reused, err := deployBinaryData(mock, []byte("bin"))
	require.Nil(t, err)
	assert.False(t, reused)
	assert.True(t, strings.HasPrefix(path, "/tmp/.remote-agent-"), "got %s", path)
	assert.False(t, cachedDeploy(path))
}

func TestCachedDeploy(t *testing.T) {
	assert.True(t, cachedDeploy("/home/u/.cache/remote-agent/agent-ab12cd"))
	assert.False(t, cachedDeploy("/tmp/.remote-agent-xyz12345"))
}

func TestShutdownKeepsCachedBinary(t *testing.T) {
	mock := newMockRunner()
	d := &Daemon{
		runner:     mock,
		remotePath: "/home/u/.cache/remote-agent/agent-ab12cd",
		keepBinary: true,
		sockPath:   filepath.Join(t.TempDir(), "test.sock"),
		pidPath:    filepath.Join(t.TempDir(), "test.pid"),
	}
	d.shutdown()

	for _, c := range mock.snapshotCalls() {
		assert.False(t, strings.HasPrefix(c.Command, "rm -f"),
			"cached helper must not be deleted on shutdown, ran: %s", c.Command)
	}
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

func TestOpStartEndTracksActivity(t *testing.T) {
	d := &Daemon{}
	assert.True(t, d.lastActivity.IsZero())

	d.opStart()
	assert.False(t, d.lastActivity.IsZero())
	assert.Equal(t, 1, d.activeOps)

	d.opEnd()
	assert.Equal(t, 0, d.activeOps)
	assert.False(t, d.lastActivity.IsZero())
}

func TestWatchIdleWaitsForActiveOps(t *testing.T) {
	oldTimeout, oldInterval, oldExit := idleTimeout, idleCheckInterval, exitFunc
	defer func() {
		idleTimeout, idleCheckInterval, exitFunc = oldTimeout, oldInterval, oldExit
	}()

	idleTimeout = 1 * time.Millisecond
	idleCheckInterval = 5 * time.Millisecond

	done := make(chan struct{})
	var once sync.Once
	exitFunc = func(code int) { once.Do(func() { close(done) }) }

	d := &Daemon{
		sockPath:     filepath.Join(t.TempDir(), "busy.sock"),
		pidPath:      filepath.Join(t.TempDir(), "busy.pid"),
		lastActivity: time.Now().Add(-1 * time.Hour), // long past the idle timeout
	}
	// Simulate a long-running command (e.g. a 40-minute build) in flight.
	d.opStart()

	go d.watchIdle()

	select {
	case <-done:
		t.Fatal("watchIdle shut the daemon down while an operation was in flight")
	case <-time.After(50 * time.Millisecond):
		// good: several ticks passed without a shutdown
	}

	// Once the operation completes, the idle countdown restarts and may fire.
	d.opEnd()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchIdle did not fire after the operation completed")
	}
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
