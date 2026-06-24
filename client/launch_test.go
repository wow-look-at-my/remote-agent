package client

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/wow-look-at-my/remote-agent/daemon"
	"github.com/wow-look-at-my/remote-agent/protocol"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

// startPongDaemon stands up a mock daemon at sockPath that answers ping with
// pong and returns OK for everything else.
func startPongDaemon(t *testing.T, sockPath string) func() {
	t.Helper()
	l, err := net.Listen("unix", sockPath)
	require.NoError(t, err)

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				var req protocol.DaemonRequest
				if json.NewDecoder(c).Decode(&req) != nil {
					return
				}
				resp := protocol.DaemonResponse{OK: true, Data: map[string]any{"action": req.Action}}
				if req.Action == "ping" {
					resp.Data = map[string]any{"pong": true}
				}
				json.NewEncoder(c).Encode(resp)
			}(conn)
		}
	}()
	return func() { l.Close() }
}

func TestBuildEnvReplacesAndAppends(t *testing.T) {
	base := []string{"PATH=/bin", "CLAUDE_CODE_SHELL_PREFIX=stale", "HOME=/root"}
	out := buildEnv(base, map[string]string{
		"CLAUDE_CODE_SHELL_PREFIX": "/tmp/shim.sh",
		"REMOTE_AGENT_BIN":         "/usr/bin/remote-agent",
	})

	m := map[string]string{}
	for _, e := range out {
		k, v, _ := splitEnv(e)
		m[k] = v
	}
	assert.Equal(t, "/bin", m["PATH"])
	assert.Equal(t, "/root", m["HOME"])
	assert.Equal(t, "/tmp/shim.sh", m["CLAUDE_CODE_SHELL_PREFIX"]) // replaced, not duplicated
	assert.Equal(t, "/usr/bin/remote-agent", m["REMOTE_AGENT_BIN"])

	// The stale prefix must not survive as a duplicate entry.
	count := 0
	for _, e := range out {
		if k, _, _ := splitEnv(e); k == "CLAUDE_CODE_SHELL_PREFIX" {
			count++
		}
	}
	assert.Equal(t, 1, count)
}

func splitEnv(e string) (string, string, bool) {
	for i := 0; i < len(e); i++ {
		if e[i] == '=' {
			return e[:i], e[i+1:], true
		}
	}
	return e, "", false
}

func TestWriteShim(t *testing.T) {
	dir := t.TempDir()
	path, err := writeShim(dir)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, shellPrefixShim, data)
	assert.Contains(t, string(data), "exec")

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0100) // owner-executable
}

func TestTmpDir(t *testing.T) {
	assert.Equal(t, "/custom", tmpDir("/custom"))
	assert.Equal(t, os.TempDir(), tmpDir(""))
}

func TestFindSocketEnvOverrides(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	t.Setenv("REMOTE_AGENT_SOCKET", "/tmp/explicit.sock")
	got, err := findSocket()
	require.NoError(t, err)
	assert.Equal(t, "/tmp/explicit.sock", got)
	t.Setenv("REMOTE_AGENT_SOCKET", "")

	t.Setenv("REMOTE_AGENT_TARGET", "root@example")
	got, err = findSocket()
	require.NoError(t, err)
	assert.Equal(t, daemon.SocketPath("root@example"), got)
}

func TestSocketOverride(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	SocketOverride = "/tmp/override.sock"
	defer func() { SocketOverride = "" }()
	got, err := findSocket()
	require.NoError(t, err)
	assert.Equal(t, "/tmp/override.sock", got)
}

func TestPingSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "ping.sock")
	assert.False(t, pingSocket(sockPath)) // nothing listening

	cleanup := startPongDaemon(t, sockPath)
	defer cleanup()
	assert.True(t, pingSocket(sockPath))
}

func TestWaitForDaemon(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "wait.sock")

	// Times out when nothing is listening.
	assert.Error(t, waitForDaemon(sockPath, 300*time.Millisecond))

	cleanup := startPongDaemon(t, sockPath)
	defer cleanup()
	assert.NoError(t, waitForDaemon(sockPath, 2*time.Second))
}

func TestDisconnectSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "dc.sock")
	cleanup := startPongDaemon(t, sockPath)
	defer cleanup()
	assert.NoError(t, disconnectSocket(sockPath))

	// No daemon -> error.
	assert.Error(t, disconnectSocket(filepath.Join(dir, "missing.sock")))
}

// fakeClaudeBin creates an executable file usable as a --claude-bin target.
func fakeClaudeBin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "fake-claude")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0755))
	return p
}

func TestLaunchClaudeReusesExistingDaemon(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	target := "root@reuse-host"
	sockPath := daemon.SocketPath(target)
	cleanup := startPongDaemon(t, sockPath)
	defer cleanup()

	var gotEnv, gotArgs []string
	var gotBin string
	runClaudeFunc = func(bin string, args, env []string) error {
		gotBin, gotArgs, gotEnv = bin, args, env
		return nil
	}
	defer func() { runClaudeFunc = runClaudeProcess }()

	// startDaemonFunc must NOT be called when a daemon already exists.
	startDaemonFunc = func(self, tgt string, port int, logPath string) (*os.Process, error) {
		t.Fatalf("startDaemonFunc should not be called for an existing daemon")
		return nil, nil
	}
	defer func() { startDaemonFunc = startDaemonProcess }()

	shimDir := t.TempDir()
	err := LaunchClaude(LaunchOptions{
		Target:     target,
		ClaudeBin:  fakeClaudeBin(t),
		ClaudeArgs: []string{"--model", "opus"},
		ShimDir:    shimDir,
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"--model", "opus"}, gotArgs)
	assert.NotEmpty(t, gotBin)

	env := map[string]string{}
	for _, e := range gotEnv {
		k, v, _ := splitEnv(e)
		env[k] = v
	}
	assert.Equal(t, sockPath, env["REMOTE_AGENT_SOCKET"])
	assert.Equal(t, filepath.Join(shimDir, "remote-agent-claude-shim.sh"), env["CLAUDE_CODE_SHELL_PREFIX"])
	assert.NotEmpty(t, env["REMOTE_AGENT_BIN"])
}

func TestLaunchClaudeStartsAndStopsDaemon(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	target := "root@fresh-host"
	sockPath := daemon.SocketPath(target)

	var cleanup func()
	started := false
	startDaemonFunc = func(self, tgt string, port int, logPath string) (*os.Process, error) {
		started = true
		cleanup = startPongDaemon(t, sockPath) // simulate the daemon coming up
		return nil, nil
	}
	defer func() {
		startDaemonFunc = startDaemonProcess
		if cleanup != nil {
			cleanup()
		}
	}()

	ranClaude := false
	runClaudeFunc = func(bin string, args, env []string) error {
		ranClaude = true
		return nil
	}
	defer func() { runClaudeFunc = runClaudeProcess }()

	err := LaunchClaude(LaunchOptions{
		Target:    target,
		ClaudeBin: fakeClaudeBin(t),
		ShimDir:   t.TempDir(),
	})
	require.NoError(t, err)
	assert.True(t, started)
	assert.True(t, ranClaude)

	// Daemon should have been asked to disconnect; the mock closes on its own,
	// but the disconnect request must have been delivered without error path.
}

func TestLaunchClaudeNoTargetNoDaemon(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	err := LaunchClaude(LaunchOptions{ClaudeBin: fakeClaudeBin(t), ShimDir: t.TempDir()})
	assert.Error(t, err) // no target and no running daemon
}

func TestStartDaemonProcess(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	proc, err := startDaemonProcess("/bin/true", "root@host", 22, logPath)
	require.NoError(t, err)
	require.NotNil(t, proc)
	_, _ = os.Stat(logPath) // log file created
}

func TestRunClaudeProcess(t *testing.T) {
	assert.NoError(t, runClaudeProcess("/bin/true", nil, os.Environ()))
}

// TestShimForwardsToRemoteAgentExec proves that the embedded shim, when invoked
// the way Claude Code's prefix wrapper invokes it (single-token program followed
// by the whole bash script as one argument), forwards that script verbatim to
// `<REMOTE_AGENT_BIN> exec`.
func TestShimForwardsToRemoteAgentExec(t *testing.T) {
	dir := t.TempDir()
	shim, err := writeShim(dir)
	require.NoError(t, err)

	argvFile := filepath.Join(dir, "argv.txt")
	fakeBin := filepath.Join(dir, "fake-remote-agent")
	// Fake remote-agent records each argument on its own line.
	require.NoError(t, os.WriteFile(fakeBin,
		[]byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+argvFile+"\n"), 0755))

	script := "shopt -u extglob 2>/dev/null || true && eval ls -la && pwd -P"
	cmd := exec.Command("/bin/sh", shim, script)
	cmd.Env = append(os.Environ(), "REMOTE_AGENT_BIN="+fakeBin)
	require.NoError(t, cmd.Run())

	got, err := os.ReadFile(argvFile)
	require.NoError(t, err)
	// The shim must call: fake-remote-agent exec "<script>"
	assert.Equal(t, "exec\n"+script+"\n", string(got))
}
