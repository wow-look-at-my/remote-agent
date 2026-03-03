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
					OK:   true,
					Data: map[string]string{"action": req.Action},
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
	err := printResponse(&protocol.DaemonResponse{Error: "test error"}, "exec")
	assert.NotNil(t, err)

	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	err = printResponse(&protocol.DaemonResponse{OK: true, Data: "hello"}, "exec")
	assert.Nil(t, err)
}

// withSuppressedStdout redirects stdout to suppress output during tests.
func withSuppressedStdout(t *testing.T, fn func()) {
	t.Helper()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old; f.Close() }()
	fn()
}

func TestDisconnect(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Disconnect())
	})
}

func TestExec(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Exec("ls -la"))
	})
}

func TestUpload(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Upload("/tmp", "/remote/path"))
	})
}

func TestDownload(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Download("/remote", "/local"))
	})
}

func TestRead(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Read("/remote/file"))
	})
}

func TestWrite(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Write("/remote/file", "0644", []byte("file content")))
	})
}

func TestEdit(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Edit("/remote/file", "hello", "world"))
	})
}

func TestLs(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Ls("/tmp", false))
	})
}

func TestLsRecursive(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Ls("/tmp", true))
	})
}

func TestPs(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Ps(""))
	})
}

func TestPsWithFilter(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Ps("nginx"))
	})
}

func TestSysinfo(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Sysinfo())
	})
}

func TestPing(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Ping())
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

func TestExecDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := Exec("ls")
	assert.NotNil(t, err)
}

func TestDisconnectDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := Disconnect()
	assert.NotNil(t, err)
}

func TestUploadDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := Upload("/tmp", "/remote")
	assert.NotNil(t, err)
}

func TestDownloadDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := Download("/remote", "/local")
	assert.NotNil(t, err)
}

func TestReadDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := Read("/remote/file")
	assert.NotNil(t, err)
}

func TestWriteDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := Write("/remote/file", "0644", []byte("data"))
	assert.NotNil(t, err)
}

func TestEditDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := Edit("/remote/file", "a", "b")
	assert.NotNil(t, err)
}

func TestLsDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := Ls("/tmp", false)
	assert.NotNil(t, err)
}

func TestPsDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := Ps("")
	assert.NotNil(t, err)
}

func TestSysinfoDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := Sysinfo()
	assert.NotNil(t, err)
}

func TestPingDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := Ping()
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
	l.Close() // Close immediately - socket file remains but nobody is listening

	_, err := sendRequest(&protocol.DaemonRequest{Action: "ping"})
	assert.NotNil(t, err)
}

func TestConnectWithPort(t *testing.T) {
	// This will fail at SSH connect but exercises the Connect function
	err := Connect("user@host", 2222)
	assert.NotNil(t, err)
}

func TestWriteWithMode(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Write("/remote/file", "0755", []byte("data")))
	})
}

func TestLsDefaultPath(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Ls(".", false))
	})
}

func TestReadlink(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		assert.NoError(t, Readlink("/usr/bin/python"))
	})
}

func TestReadlinkDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := Readlink("/usr/bin/python")
	assert.NotNil(t, err)
}

func TestPrintResponseJSON(t *testing.T) {
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = true
	defer func() { OutputJSON = false }()

	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: map[string]interface{}{"stdout": "hello\n", "stderr": "", "exit_code": float64(0)},
	}, "exec")
	assert.Nil(t, err)
}

func TestPrintResponseTextExec(t *testing.T) {
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: map[string]interface{}{"stdout": "hello\n", "stderr": "", "exit_code": float64(0)},
	}, "exec")
	assert.Nil(t, err)
}

func TestPrintResponseTextLs(t *testing.T) {
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK: true,
		Data: map[string]interface{}{
			"path": "/tmp",
			"entries": []interface{}{
				map[string]interface{}{
					"name": "/tmp/dir", "size": float64(4096), "mode": "755",
					"is_dir": true, "is_link": false,
				},
				map[string]interface{}{
					"name": "/tmp/file.txt", "size": float64(100), "mode": "644",
					"is_dir": false, "is_link": false,
				},
			},
		},
	}, "ls")
	assert.Nil(t, err)
}

func TestPrintResponseTextPing(t *testing.T) {
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: map[string]interface{}{"pong": true},
	}, "ping")
	assert.Nil(t, err)
}
