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
	"github.com/wow-look-at-my/remote-agent/client"
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

func TestExecuteExecStripsLeadingDashDash(t *testing.T) {
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

	// With flag parsing disabled the "--" separator reaches RunE as a literal
	// argument. It must not be joined into the remote command string: the exit
	// code below only comes back as 7 if the remote shell ran `exit 7` rather
	// than `-- exit 7` (which errors on every shell).
	suppressStdout(t, func() {
		assert.Nil(t, execCmd.RunE(execCmd, []string{"--", "exit", "7"}))
	})

	assert.True(t, called)
	assert.Equal(t, 7, gotCode)
}

func TestExecuteExecOnlyDashDashIsUsageError(t *testing.T) {
	err := execCmd.RunE(execCmd, []string{"--"})
	assert.Error(t, err)
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

// Cobra hands a DisableFlagParsing command the global flags typed before it,
// unparsed. Left alone they select no target (so the command runs on whatever
// daemon discovery finds -- the wrong host) and end up inside the remote
// command string.
func TestExecAppliesGlobalTargetFlag(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "exec.sock")
	cleanup := startLocalExecDaemon(t, sock)
	defer cleanup()
	t.Setenv("REMOTE_AGENT_SOCKET", sock)

	old := osExit
	var gotCode int
	osExit = func(c int) { gotCode = c }
	defer func() { osExit = old }()
	defer func(prev string) { client.TargetOverride = prev }(client.TargetOverride)
	client.TargetOverride = ""

	suppressStdout(t, func() {
		assert.Nil(t, execCmd.RunE(execCmd, []string{"--target", "root@elsewhere", "exit 7"}))
	})
	assert.Equal(t, 7, gotCode, "the remote shell must have run `exit 7`, not `--target root@elsewhere exit 7`")
	assert.Equal(t, "root@elsewhere", client.TargetOverride, "the global --target must be applied, not swallowed")
}

func TestApplyGlobalFlags(t *testing.T) {
	defer func(target string, jsonOut bool) {
		client.TargetOverride, client.OutputJSON = target, jsonOut
	}(client.TargetOverride, client.OutputJSON)

	tests := []struct {
		name       string
		args       []string
		want       []string
		wantTarget string
		wantJSON   bool
	}{
		{"no flags", []string{"ls", "-la"}, []string{"ls", "-la"}, "", false},
		{"target with value", []string{"--target", "root@h", "uptime"}, []string{"uptime"}, "root@h", false},
		{"target equals", []string{"--target=root@h", "uptime"}, []string{"uptime"}, "root@h", false},
		{"short target", []string{"-t", "root@h", "uptime"}, []string{"uptime"}, "root@h", false},
		{"json", []string{"--json", "uptime"}, []string{"uptime"}, "", true},
		{"json equals false", []string{"--json=false", "uptime"}, []string{"uptime"}, "", false},
		{"both", []string{"--json", "--target", "root@h", "df"}, []string{"df"}, "root@h", true},
		// A separator ends the scan, so a command really named --target survives.
		{"separator", []string{"--", "--target", "x"}, []string{"--target", "x"}, "", false},
		// Scanning stops at the first non-global: these belong to the command.
		{"command flags untouched", []string{"grep", "--target", "x"}, []string{"grep", "--target", "x"}, "", false},
		{"unknown global-looking flag", []string{"--nope", "ls"}, []string{"--nope", "ls"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client.TargetOverride, client.OutputJSON = "", false
			got, err := applyGlobalFlags(tt.args)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantTarget, client.TargetOverride)
			assert.Equal(t, tt.wantJSON, client.OutputJSON)
		})
	}
}

func TestApplyGlobalFlagsErrors(t *testing.T) {
	_, err := applyGlobalFlags([]string{"--target"})
	assert.ErrorContains(t, err, "needs a value")

	_, err = applyGlobalFlags([]string{"--json=maybe", "ls"})
	assert.ErrorContains(t, err, "boolean")
}
