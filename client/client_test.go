package client

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

// startMockDaemon creates a mock daemon that accepts connections and responds with okData.
func startMockDaemon(t *testing.T) (cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	sockPath := filepath.Join(dir, "remote-agent-mock123.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}

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
	if err == nil {
		t.Error("expected error when no socket exists")
	}
}

func TestFindSocketOne(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	sockPath := filepath.Join(dir, "remote-agent-abc123.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	found, err := findSocket()
	if err != nil {
		t.Fatal(err)
	}
	if found != sockPath {
		t.Errorf("found = %q, want %q", found, sockPath)
	}
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
	if err == nil {
		t.Error("expected error when multiple sockets exist")
	}
}

func TestSendRequestAndReceive(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()

	resp, err := sendRequest(&protocol.DaemonRequest{Action: "ping"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Error("response should be OK")
	}
}

func TestPrintResponse(t *testing.T) {
	err := printResponse(&protocol.DaemonResponse{Error: "test error"})
	if err == nil {
		t.Error("expected error from error response")
	}

	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	err = printResponse(&protocol.DaemonResponse{OK: true, Data: "hello"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunConnectMissingTarget(t *testing.T) {
	err := RunConnect(nil)
	if err == nil {
		t.Error("expected error with no target")
	}
}

func TestRunExecMissingCommand(t *testing.T) {
	err := RunExec(nil)
	if err == nil {
		t.Error("expected error with no command")
	}
}

func TestRunUploadMissingArgs(t *testing.T) {
	if err := RunUpload(nil); err == nil {
		t.Error("expected error with no args")
	}
	if err := RunUpload([]string{"one"}); err == nil {
		t.Error("expected error with one arg")
	}
}

func TestRunDownloadMissingArgs(t *testing.T) {
	if err := RunDownload(nil); err == nil {
		t.Error("expected error with no args")
	}
}

func TestRunReadMissingPath(t *testing.T) {
	if err := RunRead(nil); err == nil {
		t.Error("expected error with no path")
	}
}

func TestRunEditMissingArgs(t *testing.T) {
	if err := RunEdit(nil); err == nil {
		t.Error("expected error with no args")
	}
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
		if err := RunDisconnect(nil); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestRunExecWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		if err := RunExec([]string{"ls", "-la"}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestRunUploadWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		if err := RunUpload([]string{"/tmp", "/remote/path"}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestRunDownloadWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		if err := RunDownload([]string{"/remote", "/local"}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestRunReadWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		if err := RunRead([]string{"/remote/file"}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
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
		if err := RunWrite([]string{"/remote/file"}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestRunEditWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		if err := RunEdit([]string{"--old", "hello", "--new", "world", "/remote/file"}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestRunLsWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		if err := RunLs([]string{"/tmp"}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestRunLsRecursive(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		if err := RunLs([]string{"--recursive", "/tmp"}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestRunPsWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		if err := RunPs(nil); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestRunPsWithFilter(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		if err := RunPs([]string{"--filter", "nginx"}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestRunSysinfoWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		if err := RunSysinfo(nil); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestRunPingWithDaemon(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		if err := RunPing(nil); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// startErrorDaemon creates a mock daemon that returns error responses.
func startErrorDaemon(t *testing.T) (cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	sockPath := filepath.Join(dir, "remote-agent-err123.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}

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
	if err == nil {
		t.Error("expected error from daemon")
	}
}

func TestRunDisconnectDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunDisconnect(nil)
	if err == nil {
		t.Error("expected error from daemon")
	}
}

func TestRunUploadDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunUpload([]string{"/tmp", "/remote"})
	if err == nil {
		t.Error("expected error from daemon")
	}
}

func TestRunDownloadDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunDownload([]string{"/remote", "/local"})
	if err == nil {
		t.Error("expected error from daemon")
	}
}

func TestRunReadDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunRead([]string{"/remote/file"})
	if err == nil {
		t.Error("expected error from daemon")
	}
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
	if err == nil {
		t.Error("expected error from daemon")
	}
}

func TestRunEditDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunEdit([]string{"--old", "a", "--new", "b", "/remote/file"})
	if err == nil {
		t.Error("expected error from daemon")
	}
}

func TestRunLsDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunLs([]string{"/tmp"})
	if err == nil {
		t.Error("expected error from daemon")
	}
}

func TestRunPsDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunPs(nil)
	if err == nil {
		t.Error("expected error from daemon")
	}
}

func TestRunSysinfoDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunSysinfo(nil)
	if err == nil {
		t.Error("expected error from daemon")
	}
}

func TestRunPingDaemonError(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()
	err := RunPing(nil)
	if err == nil {
		t.Error("expected error from daemon")
	}
}

func TestSendRequestNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	_, err := sendRequest(&protocol.DaemonRequest{Action: "ping"})
	if err == nil {
		t.Error("expected error when no socket")
	}
}

func TestSendRequestStaleSocket(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	// Create a socket file but don't listen on it (stale)
	sockPath := filepath.Join(dir, "remote-agent-stale.sock")
	l, _ := net.Listen("unix", sockPath)
	l.Close() // Close immediately - socket file remains but nobody is listening

	_, err := sendRequest(&protocol.DaemonRequest{Action: "ping"})
	if err == nil {
		t.Error("expected error for stale socket")
	}
}

func TestRunConnectWithPort(t *testing.T) {
	// Test flag parsing - this will fail at SSH connect but exercises the flag parsing code
	err := RunConnect([]string{"--port", "2222", "user@host"})
	if err == nil {
		t.Error("expected error (no SSH server)")
	}
}

func TestRunConnectBadFlags(t *testing.T) {
	err := RunConnect([]string{"--invalid-flag"})
	if err == nil {
		t.Error("expected error for invalid flag")
	}
}

func TestRunWriteMissingPath(t *testing.T) {
	err := RunWrite(nil)
	if err == nil {
		t.Error("expected error with no path")
	}
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
		if err := RunWrite([]string{"--mode", "0755", "/remote/file"}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestRunDownloadMissingSecondArg(t *testing.T) {
	if err := RunDownload([]string{"one"}); err == nil {
		t.Error("expected error with only one arg")
	}
}

func TestRunLsDefaultPath(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()
	withSuppressedStdout(t, func() {
		// No path argument - should default to "."
		if err := RunLs(nil); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
