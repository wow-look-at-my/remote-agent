package client

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/daemon"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// isolate gives a test its own TempDir and resets the process-wide daemon
// selection state, so auto-start in one test cannot leak into the next.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("REMOTE_AGENT_SOCKET", "")
	t.Setenv("REMOTE_AGENT_TARGET", "")
	t.Setenv("REMOTE_AGENT_PORT", "")
	prevTarget, prevResolved, prevWait := TargetOverride, resolvedSocket, daemonReadyWait
	TargetOverride, resolvedSocket, daemonReadyWait = "", "", 5*time.Second
	t.Cleanup(func() {
		TargetOverride, resolvedSocket, daemonReadyWait = prevTarget, prevResolved, prevWait
	})
	return dir
}

// listenAt serves the mock daemon responses at an explicit socket path.
func listenAt(t *testing.T, sockPath string) {
	t.Helper()
	l, err := net.Listen("unix", sockPath)
	require.Nil(t, err)
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				var req protocol.DaemonRequest
				json.NewDecoder(c).Decode(&req)
				data := map[string]any{"action": req.Action}
				if req.Action == "ping" {
					data["pong"] = true // what waitForDaemon looks for
				}
				json.NewEncoder(c).Encode(protocol.DaemonResponse{OK: true, Data: data})
			}(conn)
		}
	}()
}

// stubStart replaces the daemon launcher with one that just starts a mock
// daemon at the socket the real one would have created, and records the call.
func stubStart(t *testing.T, calls *[]daemon.TargetRecord) {
	t.Helper()
	prev := startDaemonFunc
	startDaemonFunc = func(self, target string, port int, logPath string) (*os.Process, error) {
		*calls = append(*calls, daemon.TargetRecord{Target: target, Port: port})
		listenAt(t, daemon.SocketPath(target))
		return nil, nil
	}
	t.Cleanup(func() { startDaemonFunc = prev })
}

func TestResolveTargetFromFlag(t *testing.T) {
	isolate(t)
	TargetOverride = "root@flag-host"
	rec, err := resolveTarget()
	require.NoError(t, err)
	assert.Equal(t, "root@flag-host", rec.Target)
	assert.Equal(t, 22, rec.Port)
}

func TestResolveTargetFlagBeatsEnv(t *testing.T) {
	isolate(t)
	t.Setenv("REMOTE_AGENT_TARGET", "root@env-host")
	TargetOverride = "root@flag-host"
	rec, err := resolveTarget()
	require.NoError(t, err)
	assert.Equal(t, "root@flag-host", rec.Target)
}

func TestResolveTargetPortFromRecordAndEnv(t *testing.T) {
	isolate(t)
	require.NoError(t, daemon.WriteTargetRecord("root@host", 2222))

	TargetOverride = "root@host"
	rec, err := resolveTarget()
	require.NoError(t, err)
	assert.Equal(t, 2222, rec.Port)

	t.Setenv("REMOTE_AGENT_PORT", "2022")
	rec, err = resolveTarget()
	require.NoError(t, err)
	assert.Equal(t, 2022, rec.Port)
}

func TestResolveTargetFromRecord(t *testing.T) {
	isolate(t)
	require.NoError(t, daemon.WriteTargetRecord("root@remembered", 2222))
	rec, err := resolveTarget()
	require.NoError(t, err)
	assert.Equal(t, "root@remembered", rec.Target)
	assert.Equal(t, 2222, rec.Port)
}

func TestResolveTargetNoneKnown(t *testing.T) {
	isolate(t)
	_, err := resolveTarget()
	assert.ErrorContains(t, err, "no target known")
}

func TestResolveTargetAmbiguous(t *testing.T) {
	isolate(t)
	require.NoError(t, daemon.WriteTargetRecord("root@a", 22))
	require.NoError(t, daemon.WriteTargetRecord("root@b", 22))
	_, err := resolveTarget()
	assert.ErrorContains(t, err, "several targets known")
	assert.ErrorContains(t, err, "root@a")
}

func TestSendRequestAutoStartsFromRecord(t *testing.T) {
	isolate(t)
	require.NoError(t, daemon.WriteTargetRecord("root@remembered", 2222))
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	resp, err := sendRequest(&protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	assert.True(t, resp.OK)
	require.Len(t, calls, 1)
	assert.Equal(t, "root@remembered", calls[0].Target)
	assert.Equal(t, 2222, calls[0].Port)
}

func TestSendRequestAutoStartsFromTargetFlag(t *testing.T) {
	isolate(t)
	TargetOverride = "root@fresh-host"
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	_, err := sendRequest(&protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	require.Len(t, calls, 1)
	assert.Equal(t, "root@fresh-host", calls[0].Target)
}

func TestSendRequestRestartsStaleSocket(t *testing.T) {
	dir := isolate(t)
	TargetOverride = "root@stale-host"

	// A socket file left behind by a daemon that died.
	stale := daemon.SocketPath("root@stale-host")
	require.Equal(t, dir, filepath.Dir(stale))
	l, err := net.Listen("unix", stale)
	require.NoError(t, err)
	require.NoError(t, l.Close())

	var calls []daemon.TargetRecord
	prev := startDaemonFunc
	startDaemonFunc = func(self, target string, port int, logPath string) (*os.Process, error) {
		calls = append(calls, daemon.TargetRecord{Target: target, Port: port})
		os.Remove(daemon.SocketPath(target)) // as a real daemon does before listening
		listenAt(t, daemon.SocketPath(target))
		return nil, nil
	}
	t.Cleanup(func() { startDaemonFunc = prev })

	resp, err := sendRequest(&protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	assert.True(t, resp.OK)
	require.Len(t, calls, 1)
}

func TestSendRequestReusesRunningDaemon(t *testing.T) {
	isolate(t)
	TargetOverride = "root@live-host"
	listenAt(t, daemon.SocketPath("root@live-host"))

	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	resp, err := sendRequest(&protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	assert.True(t, resp.OK)
	assert.Empty(t, calls, "a running daemon must not be restarted")
}

func TestSendRequestNoAutoStartForDisconnect(t *testing.T) {
	isolate(t)
	TargetOverride = "root@host"
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	_, err := sendRequest(&protocol.DaemonRequest{Action: "disconnect"})
	assert.ErrorIs(t, err, errNoDaemon)
	assert.Empty(t, calls)
}

func TestSendRequestAutoStartDisabledByEnv(t *testing.T) {
	isolate(t)
	TargetOverride = "root@host"
	t.Setenv("REMOTE_AGENT_NO_AUTOSTART", "1")
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	_, err := sendRequest(&protocol.DaemonRequest{Action: "ls"})
	assert.ErrorIs(t, err, errNoDaemon)
	assert.Empty(t, calls)
}

func TestSendRequestNoTargetKeepsOriginalError(t *testing.T) {
	isolate(t)
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	_, err := sendRequest(&protocol.DaemonRequest{Action: "ls"})
	assert.ErrorIs(t, err, errNoDaemon)
	assert.ErrorContains(t, err, "no target known")
	assert.Empty(t, calls)
}

func TestAutoStartDaemonReportsStartFailure(t *testing.T) {
	isolate(t)
	TargetOverride = "root@host"
	prev := startDaemonFunc
	startDaemonFunc = func(self, target string, port int, logPath string) (*os.Process, error) {
		return nil, assert.AnError
	}
	t.Cleanup(func() { startDaemonFunc = prev })

	_, err := sendRequest(&protocol.DaemonRequest{Action: "ls"})
	assert.ErrorContains(t, err, "start daemon for root@host")
}

func TestAwaitDaemonReportsProcessExit(t *testing.T) {
	dir := isolate(t)
	logPath := filepath.Join(dir, "daemon.log")
	require.NoError(t, os.WriteFile(logPath, []byte("Connecting to root@host:22...\nssh connect: no route to host\n"), 0600))

	cmd := exec.Command("/bin/sh", "-c", "exit 1")
	require.NoError(t, cmd.Start())

	err := awaitDaemon(filepath.Join(dir, "never.sock"), cmd.Process, logPath, 10*time.Second)
	assert.ErrorContains(t, err, "daemon exited before it was ready")
	assert.ErrorContains(t, err, "no route to host")
}

func TestLogTailMissingFile(t *testing.T) {
	assert.Empty(t, logTail(filepath.Join(t.TempDir(), "absent.log")))
}

func TestSendRequestForStartsDaemonForNamedTarget(t *testing.T) {
	isolate(t)
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	resp, err := sendRequestFor("root@named", &protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	assert.True(t, resp.OK)
	require.Len(t, calls, 1)
	assert.Equal(t, "root@named", calls[0].Target)
}

func TestSendRequestForIgnoresProcessWideSelection(t *testing.T) {
	isolate(t)
	// A live daemon this process is otherwise pinned to. A call that names its
	// own target must not be answered by it.
	other := daemon.SocketPath("root@other")
	listenAt(t, other)
	t.Setenv("REMOTE_AGENT_SOCKET", other)
	TargetOverride = "root@other"

	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	_, err := sendRequestFor("root@named", &protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	require.Len(t, calls, 1)
	assert.Equal(t, "root@named", calls[0].Target)
}

func TestSendRequestForUsesRecordedPort(t *testing.T) {
	isolate(t)
	require.NoError(t, daemon.WriteTargetRecord("root@ported", 2222))
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	_, err := sendRequestFor("root@ported", &protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	require.Len(t, calls, 1)
	assert.Equal(t, 2222, calls[0].Port)
}

func TestSendRequestForReusesRunningDaemon(t *testing.T) {
	isolate(t)
	listenAt(t, daemon.SocketPath("root@live"))
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	resp, err := sendRequestFor("root@live", &protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	assert.True(t, resp.OK)
	assert.Empty(t, calls, "a running daemon must not be restarted")
}

func TestSendRequestForNoAutoStartHonored(t *testing.T) {
	isolate(t)
	t.Setenv("REMOTE_AGENT_NO_AUTOSTART", "1")
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	_, err := sendRequestFor("root@named", &protocol.DaemonRequest{Action: "ls"})
	assert.ErrorIs(t, err, errNoDaemon)
	assert.Empty(t, calls)
}

func TestSendRequestForEmptyTargetFallsBackToDiscovery(t *testing.T) {
	isolate(t)
	require.NoError(t, daemon.WriteTargetRecord("root@remembered", 22))
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	_, err := sendRequestFor("", &protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	require.Len(t, calls, 1)
	assert.Equal(t, "root@remembered", calls[0].Target)
}

func TestDefaultTargetPrecedence(t *testing.T) {
	isolate(t)
	assert.Empty(t, DefaultTarget())

	// A pinned socket names its host through the record beside it.
	require.NoError(t, daemon.WriteTargetRecord("root@socket-host", 22))
	t.Setenv("REMOTE_AGENT_SOCKET", daemon.SocketPath("root@socket-host"))
	assert.Equal(t, "root@socket-host", DefaultTarget())

	t.Setenv("REMOTE_AGENT_TARGET", "root@env-host")
	assert.Equal(t, "root@env-host", DefaultTarget())

	TargetOverride = "root@flag-host"
	assert.Equal(t, "root@flag-host", DefaultTarget())
}
