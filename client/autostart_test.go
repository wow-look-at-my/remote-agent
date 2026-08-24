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
	startDaemonFunc = func(self string, rec daemon.TargetRecord, logPath string) (*os.Process, error) {
		*calls = append(*calls, rec)
		listenAt(t, daemon.SocketPath(rec.Target))
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
	// A target naming no port keeps none. ssh_config, else 22, decides on connect.
	assert.Equal(t, 0, rec.Port)
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
	require.NoError(t, daemon.WriteTargetRecord(daemon.TargetRecord{Target: "root@host:2222"}))

	TargetOverride = "root@host:2222"
	rec, err := resolveTarget()
	require.NoError(t, err)
	assert.Equal(t, 2222, rec.Port)

	// A port from the environment keys its own daemon, and never redirects the one above.
	TargetOverride = "root@host"
	t.Setenv("REMOTE_AGENT_PORT", "2022")
	rec, err = resolveTarget()
	require.NoError(t, err)
	assert.Equal(t, 2022, rec.Port)
	assert.Equal(t, "root@host:2022", rec.Target)
}

func TestResolveTargetFromRecord(t *testing.T) {
	isolate(t)
	require.NoError(t, daemon.WriteTargetRecord(daemon.TargetRecord{Target: "root@remembered:2222"}))
	rec, err := resolveTarget()
	require.NoError(t, err)
	assert.Equal(t, "root@remembered:2222", rec.Target)
	assert.Equal(t, 2222, rec.Port)
}

func TestResolveTargetNoneKnown(t *testing.T) {
	isolate(t)
	_, err := resolveTarget()
	assert.ErrorContains(t, err, "no target known")
}

func TestResolveTargetAmbiguous(t *testing.T) {
	isolate(t)
	require.NoError(t, daemon.WriteTargetRecord(daemon.TargetRecord{Target: "root@a", Port: 22}))
	require.NoError(t, daemon.WriteTargetRecord(daemon.TargetRecord{Target: "root@b", Port: 22}))
	_, err := resolveTarget()
	assert.ErrorContains(t, err, "several targets known")
	assert.ErrorContains(t, err, "root@a")
}

func TestSendRequestAutoStartsFromRecord(t *testing.T) {
	isolate(t)
	require.NoError(t, daemon.WriteTargetRecord(daemon.TargetRecord{Target: "root@remembered:2222"}))
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	resp, err := sendRequest(&protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	assert.True(t, resp.OK)
	require.Len(t, calls, 1)
	assert.Equal(t, "root@remembered:2222", calls[0].Target)
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
	startDaemonFunc = func(self string, rec daemon.TargetRecord, logPath string) (*os.Process, error) {
		calls = append(calls, rec)
		os.Remove(daemon.SocketPath(rec.Target)) // as a real daemon does before listening
		listenAt(t, daemon.SocketPath(rec.Target))
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
	startDaemonFunc = func(self string, rec daemon.TargetRecord, logPath string) (*os.Process, error) {
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

	resp, err := sendRequestFor(protocol.Route{Target: "root@named"}, &protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	assert.True(t, resp.OK)
	require.Len(t, calls, 1)
	assert.Equal(t, "root@named", calls[0].Target)
}

func TestSendRequestForIgnoresProcessWideSelection(t *testing.T) {
	isolate(t)
	// A live daemon this process is pinned to. A call naming its own target must miss it.
	other := daemon.SocketPath("root@other")
	listenAt(t, other)
	t.Setenv("REMOTE_AGENT_SOCKET", other)
	TargetOverride = "root@other"

	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	_, err := sendRequestFor(protocol.Route{Target: "root@named"}, &protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	require.Len(t, calls, 1)
	assert.Equal(t, "root@named", calls[0].Target)
}

func TestSendRequestForUsesRecordedPort(t *testing.T) {
	isolate(t)
	require.NoError(t, daemon.WriteTargetRecord(daemon.TargetRecord{Target: "root@ported:2222"}))
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	_, err := sendRequestFor(protocol.Route{Target: "root@ported:2222"}, &protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	require.Len(t, calls, 1)
	assert.Equal(t, 2222, calls[0].Port)
}

func TestSendRequestForReusesRunningDaemon(t *testing.T) {
	isolate(t)
	listenAt(t, daemon.SocketPath("root@live"))
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	resp, err := sendRequestFor(protocol.Route{Target: "root@live"}, &protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	assert.True(t, resp.OK)
	assert.Empty(t, calls, "a running daemon must not be restarted")
}

func TestSendRequestForNoAutoStartHonored(t *testing.T) {
	isolate(t)
	t.Setenv("REMOTE_AGENT_NO_AUTOSTART", "1")
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	_, err := sendRequestFor(protocol.Route{Target: "root@named"}, &protocol.DaemonRequest{Action: "ls"})
	assert.ErrorIs(t, err, errNoDaemon)
	assert.Empty(t, calls)
}

func TestSendRequestForEmptyTargetFallsBackToDiscovery(t *testing.T) {
	isolate(t)
	require.NoError(t, daemon.WriteTargetRecord(daemon.TargetRecord{Target: "root@remembered"}))
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	_, err := sendRequestFor(protocol.Route{Target: ""}, &protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	require.Len(t, calls, 1)
	assert.Equal(t, "root@remembered", calls[0].Target)
}

func TestDefaultTargetPrecedence(t *testing.T) {
	isolate(t)
	got, err := DefaultTarget()
	require.NoError(t, err)
	assert.Empty(t, got)

	// A pinned socket names its host through the record beside it.
	require.NoError(t, daemon.WriteTargetRecord(daemon.TargetRecord{Target: "root@socket-host"}))
	t.Setenv("REMOTE_AGENT_SOCKET", daemon.SocketPath("root@socket-host"))
	got, err = DefaultTarget()
	require.NoError(t, err)
	assert.Equal(t, "root@socket-host", got)

	t.Setenv("REMOTE_AGENT_TARGET", "root@env-host")
	got, err = DefaultTarget()
	require.NoError(t, err)
	assert.Equal(t, "root@env-host", got)

	TargetOverride = "root@flag-host"
	got, err = DefaultTarget()
	require.NoError(t, err)
	assert.Equal(t, "root@flag-host", got)
}

// A default target the MCP server hands to every call must carry the port, or
// the calls land on whichever endpoint of that host connected first.
func TestDefaultTargetKeepsPort(t *testing.T) {
	isolate(t)
	TargetOverride = "root@127.0.0.1:2201"
	got, err := DefaultTarget()
	require.NoError(t, err)
	assert.Equal(t, "root@127.0.0.1:2201", got)

	TargetOverride = "root@127.0.0.1"
	t.Setenv("REMOTE_AGENT_PORT", "2202")
	got, err = DefaultTarget()
	require.NoError(t, err)
	assert.Equal(t, "root@127.0.0.1:2202", got)
}

func TestTargetKeyFoldsPorts(t *testing.T) {
	isolate(t)
	key, err := TargetKey("root@127.0.0.1:2201")
	require.NoError(t, err)
	assert.Equal(t, "root@127.0.0.1:2201", key)

	t.Setenv("REMOTE_AGENT_PORT", "2202")
	key, err = TargetKey("root@127.0.0.1")
	require.NoError(t, err)
	assert.Equal(t, "root@127.0.0.1:2202", key, "REMOTE_AGENT_PORT is part of the endpoint's identity")

	_, err = TargetKey("root@127.0.0.1:2201")
	assert.Error(t, err, "a target port and REMOTE_AGENT_PORT that disagree name two hosts")

	key, err = TargetKey("")
	require.NoError(t, err)
	assert.Empty(t, key)
}

// The defect this fixes: two endpoints reachable as root@127.0.0.1 on
// different ports shared one daemon, so every call went to whichever one
// started first.
func TestPortedTargetsGetTheirOwnDaemon(t *testing.T) {
	isolate(t)
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	for _, target := range []string{"root@127.0.0.1:2201", "root@127.0.0.1:2202"} {
		_, err := sendRequestFor(protocol.Route{Target: target}, &protocol.DaemonRequest{Action: "ls"})
		require.NoError(t, err)
	}

	require.Len(t, calls, 2, "each port must start a daemon of its own")
	assert.Equal(t, "root@127.0.0.1:2201", calls[0].Target)
	assert.Equal(t, 2201, calls[0].Port)
	assert.Equal(t, "root@127.0.0.1:2202", calls[1].Target)
	assert.Equal(t, 2202, calls[1].Port)
	assert.NotEqual(t, daemon.SocketPath(calls[0].Target), daemon.SocketPath(calls[1].Target))
}

// A daemon on one port must not answer a call meant for another port on the
// same host, even while it is running.
func TestRunningDaemonDoesNotServeAnotherPort(t *testing.T) {
	isolate(t)
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	listenAt(t, daemon.SocketPath("root@127.0.0.1:2201"))
	_, err := sendRequestFor(protocol.Route{Target: "root@127.0.0.1:2202"}, &protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)

	require.Len(t, calls, 1)
	assert.Equal(t, "root@127.0.0.1:2202", calls[0].Target)
}

// A record written before the port joined the target keeps its port in a field
// of its own. Restarting that daemon has to reach the same endpoint.
func TestLegacyRecordPortFoldsIntoTheKey(t *testing.T) {
	isolate(t)
	require.NoError(t, daemon.WriteTargetRecord(daemon.TargetRecord{
		Target: "root@127.0.0.1", Port: 2201, ControlPath: "/tmp/legacy.sock",
	}))

	rec, err := recordFor(protocol.Route{Target: "root@127.0.0.1"})
	require.NoError(t, err)
	assert.Equal(t, "root@127.0.0.1:2201", rec.Target)
	assert.Equal(t, 2201, rec.Port)
	assert.Equal(t, "/tmp/legacy.sock", rec.ControlPath)
}

func TestSendRequestForStartsDaemonThroughControlMaster(t *testing.T) {
	isolate(t)
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	_, err := sendRequestFor(protocol.Route{
		Target:      "root@locked-down",
		ControlPath: "/tmp/cm.sock",
	}, &protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	require.Len(t, calls, 1)
	assert.Equal(t, "/tmp/cm.sock", calls[0].ControlPath,
		"the control socket the call named must reach the daemon being started")
}

// A daemon that idled out is restarted through the same control master it used
// before, without the caller having to name it again.
func TestControlPathRememberedAcrossRestarts(t *testing.T) {
	isolate(t)
	require.NoError(t, daemon.WriteTargetRecord(daemon.TargetRecord{
		Target: "root@remembered", Port: 22, ControlPath: "/tmp/remembered.sock",
	}))
	var calls []daemon.TargetRecord
	stubStart(t, &calls)

	_, err := sendRequestFor(protocol.Route{Target: "root@remembered"}, &protocol.DaemonRequest{Action: "ls"})
	require.NoError(t, err)
	require.Len(t, calls, 1)
	assert.Equal(t, "/tmp/remembered.sock", calls[0].ControlPath)
}

func TestControlPathFromFlagAndEnv(t *testing.T) {
	isolate(t)
	t.Setenv("REMOTE_AGENT_CONTROL_PATH", "/tmp/env.sock")
	assert.Equal(t, "/tmp/env.sock", ControlPathFor(protocol.Route{}))

	ControlPathOverride = "/tmp/flag.sock"
	t.Cleanup(func() { ControlPathOverride = "" })
	assert.Equal(t, "/tmp/flag.sock", ControlPathFor(protocol.Route{}))

	// A call that names its own beats both: one server, several hosts.
	assert.Equal(t, "/tmp/call.sock", ControlPathFor(protocol.Route{ControlPath: "/tmp/call.sock"}))
}

// A running daemon cannot be re-pointed at another control master, so a call
// naming a different one must fail rather than run on the wrong connection.
func TestCallThroughDifferentControlMasterIsRefused(t *testing.T) {
	isolate(t)
	require.NoError(t, daemon.WriteTargetRecord(daemon.TargetRecord{
		Target: "root@live", Port: 22, ControlPath: "/tmp/first.sock",
	}))
	listenAt(t, daemon.SocketPath("root@live"))

	_, err := sendRequestFor(protocol.Route{
		Target: "root@live", ControlPath: "/tmp/second.sock",
	}, &protocol.DaemonRequest{Action: "ls"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "/tmp/first.sock")
	assert.ErrorContains(t, err, "disconnect")

	// The same socket is fine, and so is a daemon that is not running.
	_, err = sendRequestFor(protocol.Route{
		Target: "root@live", ControlPath: "/tmp/first.sock",
	}, &protocol.DaemonRequest{Action: "ls"})
	assert.NoError(t, err)
}

func TestDirectDaemonRefusesControlPathCall(t *testing.T) {
	isolate(t)
	require.NoError(t, daemon.WriteTargetRecord(daemon.TargetRecord{Target: "root@direct", Port: 22}))
	listenAt(t, daemon.SocketPath("root@direct"))

	_, err := sendRequestFor(protocol.Route{
		Target: "root@direct", ControlPath: "/tmp/cm.sock",
	}, &protocol.DaemonRequest{Action: "ls"})
	assert.ErrorContains(t, err, "no control master")
}
