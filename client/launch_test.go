package client

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/daemon"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// What the mock daemon reports as the remote working directory, so the launcher mounts it.
const mockRemoteHome = "/remote/home"

// mockDaemonCalls records the mount traffic a launch generates.
var mockDaemonCalls mountCallLog

type mountCallLog struct {
	mu    sync.Mutex
	calls []mountCall
}

type mountCall struct {
	Action string
	Local  string
	Remote string
}

func (l *mountCallLog) record(action, local, remote string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, mountCall{Action: action, Local: local, Remote: remote})
}

func (l *mountCallLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = nil
}

func (l *mountCallLog) snapshot() []mountCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]mountCall(nil), l.calls...)
}

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
				switch req.Action {
				case "ping":
					resp.Data = map[string]any{"pong": true}
				case "exec":
					// The launcher asks the remote for its working directory.
					resp.Data = map[string]any{"stdout": mockRemoteHome + "\n", "exit_code": float64(0)}
				case "mount", "unmount":
					local, _ := req.Params["local_path"].(string)
					remote, _ := req.Params["remote_path"].(string)
					mockDaemonCalls.record(req.Action, local, remote)
					resp.Data = map[string]any{
						"local_path":  local,
						"remote_path": remote,
						"mounted":     req.Action == "mount",
					}
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
	var gotBin, gotDir string
	runClaudeFunc = func(bin string, args, env []string, dir string) error {
		gotBin, gotArgs, gotEnv, gotDir = bin, args, env, dir
		return nil
	}
	defer func() { runClaudeFunc = runClaudeProcess }()

	// startDaemonFunc must NOT be called when a daemon already exists.
	startDaemonFunc = func(self string, rec daemon.TargetRecord, logPath string) (*os.Process, error) {
		t.Fatalf("startDaemonFunc should not be called for an existing daemon")
		return nil, nil
	}
	defer func() { startDaemonFunc = startDaemonProcess }()

	shimDir := t.TempDir()
	mockDaemonCalls.reset()
	err := LaunchClaude(LaunchOptions{
		Target:     target,
		ClaudeBin:  fakeClaudeBin(t),
		ClaudeArgs: []string{"--model", "opus"},
		ShimDir:    shimDir,
	})
	require.NoError(t, err)

	// Mounting is the default, so claude's own tools are left alone: no tool
	// is disabled and no replacement toolset is registered.
	assert.Equal(t, []string{"--model", "opus"}, gotArgs,
		"a mounted session must not touch claude's tool configuration")

	// One path, both halves: the mount point equals the remote home, and claude runs there.
	calls := mockDaemonCalls.snapshot()
	require.NotEmpty(t, calls)
	assert.Equal(t, mountCall{Action: "mount", Local: mockRemoteHome, Remote: mockRemoteHome}, calls[0])
	assert.Equal(t, mockRemoteHome, gotDir, "claude must run in the mount")

	// The mount is released when claude exits.
	require.Len(t, calls, 2)
	assert.Equal(t, mountCall{Action: "unmount", Local: mockRemoteHome}, calls[1])
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
	startDaemonFunc = func(self string, rec daemon.TargetRecord, logPath string) (*os.Process, error) {
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
	runClaudeFunc = func(bin string, args, env []string, dir string) error {
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
	proc, err := startDaemonProcess("/bin/true", daemon.TargetRecord{Target: "root@host", Port: 22}, logPath)
	require.NoError(t, err)
	require.NotNil(t, proc)
	_, _ = os.Stat(logPath) // log file created
}

// A daemon started for a route with a control master must be told about it:
// the child process is where the connection is actually made.
func TestStartDaemonProcessPassesControlPath(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	self := filepath.Join(dir, "echo-args")
	require.NoError(t, os.WriteFile(self, []byte("#!/bin/sh\necho \"$@\"\n"), 0755))

	proc, err := startDaemonProcess(self, daemon.TargetRecord{
		Target: "root@host", Port: 22, ControlPath: "/tmp/cm.sock",
	}, logPath)
	require.NoError(t, err)
	_, _ = proc.Wait()

	logged, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(logged), "--control-path /tmp/cm.sock")
}

func TestRunClaudeProcess(t *testing.T) {
	assert.NoError(t, runClaudeProcess("/bin/true", nil, os.Environ(), ""))
}

// TestLaunchClaudeNoMountFallsBackToRemoteTools covers the FUSE-less path:
// without a mount, the built-in file tools would act on the local machine, so
// they are replaced by the MCP toolset instead.
func TestLaunchClaudeNoMountFallsBackToRemoteTools(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	target := "root@nomount-host"
	sockPath := daemon.SocketPath(target)
	cleanup := startPongDaemon(t, sockPath)
	defer cleanup()

	var gotArgs []string
	var gotDir string
	runClaudeFunc = func(bin string, args, env []string, dir string) error {
		gotArgs, gotDir = args, dir
		return nil
	}
	defer func() { runClaudeFunc = runClaudeProcess }()

	shimDir := t.TempDir()
	mockDaemonCalls.reset()
	require.NoError(t, LaunchClaude(LaunchOptions{
		Target:     target,
		ClaudeBin:  fakeClaudeBin(t),
		ClaudeArgs: []string{"--model", "opus"},
		ShimDir:    shimDir,
		NoMount:    true,
	}))

	assert.Empty(t, mockDaemonCalls.snapshot(), "--no-mount must not mount anything")
	assert.Empty(t, gotDir, "without a mount there is no remote working directory to run in")

	configPath := filepath.Join(shimDir, "remote-agent-claude-mcp.json")
	assert.Equal(t, []string{
		"--mcp-config=" + configPath,
		"--disallowedTools=" + strings.Join(disabledLocalTools, ","),
		"--allowedTools=" + strings.Join(preAllowedRemoteTools, ","),
		"--model", "opus",
	}, gotArgs)
	// Every flag uses the "=" form: claude declares these options variadic, so
	// a space-separated value would swallow the user's own arguments.
	for _, arg := range gotArgs[:3] {
		assert.Contains(t, arg, "=", "remote toolset flags must bind their value with '='")
	}

	// The config points claude at this binary's `mcp`, pinned to this launch's socket.
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var config struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(data, &config))
	server, ok := config.MCPServers[mcpServerName]
	require.True(t, ok, "config should define the %q server", mcpServerName)
	assert.Equal(t, "stdio", server.Type)
	// Claude spawns this itself, with an execve that cannot start an APE, so the
	// command is a shell and the binary is its first argument. see docs/ape.md
	self, err := os.Executable()
	require.NoError(t, err)
	wantCommand, wantArgs := SelfCommand(self, "mcp", "root@nomount-host")
	assert.Equal(t, wantCommand, server.Command)
	// The target rides in the argv, so the server can restart a daemon that idles out.
	assert.Equal(t, wantArgs, server.Args)
	assert.Contains(t, server.Args, self, "the binary travels as an argument of the spawned command")
	assert.Equal(t, sockPath, server.Env["REMOTE_AGENT_SOCKET"])
	assert.Equal(t, "root@nomount-host", server.Env["REMOTE_AGENT_TARGET"])
}

// TestLaunchClaudeNoMountLocalTools leaves claude's tools entirely alone.
func TestLaunchClaudeNoMountLocalTools(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	target := "root@localtools-host"
	sockPath := daemon.SocketPath(target)
	cleanup := startPongDaemon(t, sockPath)
	defer cleanup()

	var gotArgs []string
	runClaudeFunc = func(bin string, args, env []string, dir string) error {
		gotArgs = args
		return nil
	}
	defer func() { runClaudeFunc = runClaudeProcess }()

	require.NoError(t, LaunchClaude(LaunchOptions{
		Target:     target,
		ClaudeBin:  fakeClaudeBin(t),
		ClaudeArgs: []string{"--model", "opus"},
		ShimDir:    t.TempDir(),
		NoMount:    true,
		LocalTools: true,
	}))
	assert.Equal(t, []string{"--model", "opus"}, gotArgs)
}

// TestLaunchClaudeMountAt puts the mount somewhere other than the remote path.
func TestLaunchClaudeMountAt(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	target := "root@mountat-host"
	sockPath := daemon.SocketPath(target)
	cleanup := startPongDaemon(t, sockPath)
	defer cleanup()

	var gotEnv []string
	var gotDir string
	runClaudeFunc = func(bin string, args, env []string, dir string) error {
		gotEnv, gotDir = env, dir
		return nil
	}
	defer func() { runClaudeFunc = runClaudeProcess }()

	mountAt := filepath.Join(t.TempDir(), "mnt")
	mockDaemonCalls.reset()
	require.NoError(t, LaunchClaude(LaunchOptions{
		Target:    target,
		ClaudeBin: fakeClaudeBin(t),
		ShimDir:   t.TempDir(),
		RemoteDir: "/srv/app",
		MountAt:   mountAt,
	}))

	calls := mockDaemonCalls.snapshot()
	require.NotEmpty(t, calls)
	assert.Equal(t, mountCall{Action: "mount", Local: mountAt, Remote: "/srv/app"}, calls[0])
	assert.Equal(t, mountAt, gotDir)

	// The two halves disagree on paths, so the shim must not cd into a local one.
	env := map[string]string{}
	for _, e := range gotEnv {
		k, v, _ := splitEnv(e)
		env[k] = v
	}
	_, set := env["REMOTE_AGENT_MOUNT"]
	assert.False(t, set, "REMOTE_AGENT_MOUNT is only valid when the mount point matches the remote path")
}

// TestLaunchClaudeMountFailureIsFatal: a failed mount must stop the launch,
// not silently continue with tools pointed at the local machine.
func TestLaunchClaudeMountFailureIsFatal(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	target := "root@badmount-host"
	sockPath := daemon.SocketPath(target)
	cleanup := startErrorMountDaemon(t, sockPath)
	defer cleanup()

	ran := false
	runClaudeFunc = func(bin string, args, env []string, dir string) error {
		ran = true
		return nil
	}
	defer func() { runClaudeFunc = runClaudeProcess }()

	err := LaunchClaude(LaunchOptions{
		Target:    target,
		ClaudeBin: fakeClaudeBin(t),
		ShimDir:   t.TempDir(),
		RemoteDir: "/srv/app",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--no-mount", "the error should point at the way out")
	assert.False(t, ran, "claude must not start when the mount failed")
}

// startErrorMountDaemon answers ping and exec, but refuses to mount.
func startErrorMountDaemon(t *testing.T, sockPath string) func() {
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
				resp := protocol.DaemonResponse{OK: true, Data: map[string]any{}}
				switch req.Action {
				case "ping":
					resp.Data = map[string]any{"pong": true}
				case "exec":
					resp.Data = map[string]any{"stdout": "/srv/app\n", "exit_code": float64(0)}
				case "mount":
					resp = protocol.DaemonResponse{Error: "mount point /srv/app is not empty"}
				}
				json.NewEncoder(c).Encode(resp)
			}(conn)
		}
	}()
	return func() { l.Close() }
}

// TestShimForwardsToClaudeShim proves that the embedded shim, when invoked the
// way Claude Code's prefix wrapper invokes it (single-token program followed by
// the whole command line as one argument), forwards that command line verbatim
// to `<REMOTE_AGENT_BIN> claude-shim` -- the hidden subcommand that routes Bash
// tool wrappers to the remote and hooks/MCP servers to local execution.
func TestShimForwardsToClaudeShim(t *testing.T) {
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
	// The shim must call: fake-remote-agent claude-shim "<script>"
	assert.Equal(t, "claude-shim\n"+script+"\n", string(got))
}
