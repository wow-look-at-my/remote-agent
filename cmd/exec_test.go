package cmd

import (
	"bytes"
	"encoding/json"
	"net"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// startLocalExecDaemon stands up a daemon socket whose exec action runs the
// command locally via `sh -c`. It is a faithful stand-in for the real
// SSH-backed daemon for the purpose of exercising the client/exec exit-code path.
func startLocalExecDaemon(t *testing.T, sockPath string) func() {
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
				if req.Action != "exec" {
					json.NewEncoder(c).Encode(protocol.DaemonResponse{OK: true, Data: map[string]any{"action": req.Action}})
					return
				}
				command, _ := req.Params["command"].(string)
				cmd := exec.Command("sh", "-c", command)
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				runErr := cmd.Run()
				code := 0
				if ee, ok := runErr.(*exec.ExitError); ok {
					code = ee.ExitCode()
				} else if runErr != nil {
					code = 1
				}
				json.NewEncoder(c).Encode(protocol.DaemonResponse{
					OK:   true,
					Data: protocol.ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code},
				})
			}(conn)
		}
	}()
	return func() { l.Close() }
}

func TestExecuteExecPropagatesNonzeroExit(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "exec.sock")
	cleanup := startLocalExecDaemon(t, sock)
	defer cleanup()
	t.Setenv("REMOTE_AGENT_SOCKET", sock)

	var gotCode int
	var called bool
	old := osExit
	osExit = func(c int) { called, gotCode = true, c }
	defer func() { osExit = old }()

	// Invoke the command's RunE directly to avoid mutating the shared rootCmd
	// argument state that other tests in this package rely on.
	suppressStdout(t, func() {
		assert.Nil(t, execCmd.RunE(execCmd, []string{"exit 7"}))
	})

	assert.True(t, called, "osExit should be invoked for a non-zero remote exit")
	assert.Equal(t, 7, gotCode)
}

func TestExecuteExecZeroExitDoesNotExit(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "exec.sock")
	cleanup := startLocalExecDaemon(t, sock)
	defer cleanup()
	t.Setenv("REMOTE_AGENT_SOCKET", sock)

	var called bool
	old := osExit
	osExit = func(c int) { called = true }
	defer func() { osExit = old }()

	suppressStdout(t, func() {
		assert.Nil(t, execCmd.RunE(execCmd, []string{"true"}))
	})

	assert.False(t, called, "osExit must not be called when the remote command succeeds")
}
