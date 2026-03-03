package client

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/remote-agent/protocol"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

// startMockDaemon creates a mock daemon that accepts connections and responds with okData.
func startMockDaemon(t *testing.T) (cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	sockPath := filepath.Join(dir, "remote-agent-mock123.sock")
	l, err := net.Listen("unix", sockPath)
	require.Nil(t, err)

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
				resp := protocol.DaemonResponse{
					OK:	true,
					Data:	map[string]string{"action": req.Action},
				}
				json.NewEncoder(c).Encode(resp)
			}(conn)
		}
	}()

	return func() { l.Close() }
}

func TestFindSocketNone(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	_, err := findSocket()
	assert.NotNil(t, err)

}

func TestFindSocketOne(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	sockPath := filepath.Join(dir, "remote-agent-abc123.sock")
	l, err := net.Listen("unix", sockPath)
	require.Nil(t, err)

	defer l.Close()

	found, err := findSocket()
	require.Nil(t, err)
	assert.Equal(t, sockPath, found)

}

func TestFindSocketMultiple(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	sock1 := filepath.Join(dir, "remote-agent-aaa.sock")
	sock2 := filepath.Join(dir, "remote-agent-bbb.sock")
	l1, _ := net.Listen("unix", sock1)
	l2, _ := net.Listen("unix", sock2)
	defer l1.Close()
	defer l2.Close()

	_, err := findSocket()
	assert.NotNil(t, err)

}

func TestSendRequestAndReceive(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()

	resp, err := sendRequest(&protocol.DaemonRequest{Action: "ping"})
	require.Nil(t, err)
	assert.True(t, resp.OK)

}

func TestPrintResponse(t *testing.T) {
	err := printResponse(&protocol.DaemonResponse{Error: "test error"})
	assert.NotNil(t, err)

	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	err = printResponse(&protocol.DaemonResponse{OK: true, Data: "hello"})
	assert.Nil(t, err)

}

func TestRunConnectMissingTarget(t *testing.T) {
	err := RunConnect(nil)
	assert.NotNil(t, err)

}

func TestRunExecMissingCommand(t *testing.T) {
	err := RunExec(nil)
	assert.NotNil(t, err)

}

func TestRunUploadMissingArgs(t *testing.T) {
	err := RunUpload(nil)
	assert.NotNil(t, err)

	err = RunUpload([]string{"one"})
	assert.NotNil(t, err)

}

func TestRunDownloadMissingArgs(t *testing.T) {
	err := RunDownload(nil)
	assert.NotNil(t, err)

}

func TestRunReadMissingPath(t *testing.T) {
	err := RunRead(nil)
	assert.NotNil(t, err)

}

func TestRunEditMissingArgs(t *testing.T) {
	err := RunEdit(nil)
	assert.NotNil(t, err)

}

// Test that all commands talk to the daemon correctly.
// We redirect stdout to suppress output.
func withSuppressedStdout(t *testing.T, fn func()) {
	t.Helper()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old; f.Close() }()
	fn()
}

func TestRunDisconnect(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, RunDisconnect(nil))

	})
}

func TestRunExecWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, RunExec([]string{"ls", "-la"}))

	})
}

func TestRunUploadWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, RunUpload([]string{"/tmp", "/remote/path"}))

	})
}

func TestRunDownloadWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, RunDownload([]string{"/remote", "/local"}))

	})
}

func TestRunReadWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, RunRead([]string{"/remote/file"}))

	})
}

func TestRunWriteWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()

	// Create a fake stdin
	r, w, _ := os.Pipe()
	w.Write([]byte("file content"))
	w.Close()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	withSuppressedStdout(t, func() {
		assert.NoError(t, RunWrite([]string{"/remote/file"}))

	})
}

func TestRunEditWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, RunEdit([]string{"--old", "hello", "--new", "world", "/remote/file"}))

	})
}

func TestRunLsWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, RunLs([]string{"/tmp"}))

	})
}

func TestRunLsRecursive(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, RunLs([]string{"--recursive", "/tmp"}))

	})
}

func TestRunPsWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, RunPs(nil))

	})
}

func TestRunPsWithFilter(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, RunPs([]string{"--filter", "nginx"}))

	})
}

func TestRunSysinfoWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, RunSysinfo(nil))

	})
}

func TestRunPingWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, RunPing(nil))

	})
}

// startErrorDaemon creates a mock daemon that returns error responses.
func startErrorDaemon(t *testing.T) (cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	sockPath := filepath.Join(dir, "remote-agent-err123.sock")
	l, err := net.Listen("unix", sockPath)
	require.Nil(t, err)

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
				resp := protocol.DaemonResponse{
					Error: "daemon error: " + req.Action,
				}
				json.NewEncoder(c).Encode(resp)
			}(conn)
		}
	}()

	return func() { l.Close() }
}

func TestRunExecDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunExec([]string{"ls"})
	assert.NotNil(t, err)

}

func TestRunDisconnectDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunDisconnect(nil)
	assert.NotNil(t, err)

}

func TestRunUploadDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunUpload([]string{"/tmp", "/remote"})
	assert.NotNil(t, err)

}

func TestRunDownloadDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunDownload([]string{"/remote", "/local"})
	assert.NotNil(t, err)

}

func TestRunReadDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunRead([]string{"/remote/file"})
	assert.NotNil(t, err)

}

func TestRunWriteDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()

	r, w, _ := os.Pipe()
	w.Write([]byte("data"))
	w.Close()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	err := RunWrite([]string{"/remote/file"})
	assert.NotNil(t, err)

}

func TestRunEditDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunEdit([]string{"--old", "a", "--new", "b", "/remote/file"})
	assert.NotNil(t, err)

}

func TestRunLsDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunLs([]string{"/tmp"})
	assert.NotNil(t, err)

}

func TestRunPsDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunPs(nil)
	assert.NotNil(t, err)

}

func TestRunSysinfoDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunSysinfo(nil)
	assert.NotNil(t, err)

}

func TestRunPingDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunPing(nil)
	assert.NotNil(t, err)

}

func TestSendRequestNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	_, err := sendRequest(&protocol.DaemonRequest{Action: "ping"})
	assert.NotNil(t, err)

}

func TestSendRequestStaleSocket(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	// Create a socket file but don't listen on it (stale)
	sockPath := filepath.Join(dir, "remote-agent-stale.sock")
	l, _ := net.Listen("unix", sockPath)
	l.Close()	// Close immediately - socket file remains but nobody is listening

	_, err := sendRequest(&protocol.DaemonRequest{Action: "ping"})
	assert.NotNil(t, err)

}

func TestRunConnectWithPort(t *testing.T) {
	// Test flag parsing - this will fail at SSH connect but exercises the flag parsing code
	err := RunConnect([]string{"--port", "2222", "user@host"})
	assert.NotNil(t, err)

}

func TestRunConnectBadFlags(t *testing.T) {
	err := RunConnect([]string{"--invalid-flag"})
	assert.NotNil(t, err)

}

func TestRunWriteMissingPath(t *testing.T) {
	err := RunWrite(nil)
	assert.NotNil(t, err)

}

func TestRunWriteWithMode(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()

	r, w, _ := os.Pipe()
	w.Write([]byte("data"))
	w.Close()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	withSuppressedStdout(t, func() {
		assert.NoError(t, RunWrite([]string{"--mode", "0755", "/remote/file"}))

	})
}

func TestRunDownloadMissingSecondArg(t *testing.T) {
	err := RunDownload([]string{"one"})
	assert.NotNil(t, err)

}

func TestRunLsDefaultPath(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.
		// No path argument - should default to "."
		NoError(t, RunLs(nil))

	})
}
