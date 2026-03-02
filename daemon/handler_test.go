package daemon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

func TestHandlerUnknownAction(t *testing.T) {
	h := &Handler{daemon: &Daemon{}}
	resp := h.Handle(&protocol.DaemonRequest{Action: "nonexistent"})
	if resp.OK {
		t.Error("should not be OK for unknown action")
	}
	if resp.Error == "" {
		t.Error("should have error message")
	}
}

func TestOkResponse(t *testing.T) {
	resp := okResponse("test")
	if !resp.OK {
		t.Error("should be OK")
	}
	if resp.Data != "test" {
		t.Error("data should be 'test'")
	}
}

func TestErrResponse(t *testing.T) {
	resp := errResponse(fmt.Errorf("test error"))
	if resp.OK {
		t.Error("should not be OK")
	}
	if resp.Error != "test error" {
		t.Errorf("error = %q, want %q", resp.Error, "test error")
	}
}

// TestHandleDispatch tests that Handle routes each action correctly.
func TestHandleDispatchPing(t *testing.T) {
	mock := newMockRunner()
	mock.onCommand("echo pong", []byte("pong\n"), 0)
	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{Action: "ping"})
	if !resp.OK {
		t.Errorf("ping should be OK: %s", resp.Error)
	}
}

func TestHandleDispatchExec(t *testing.T) {
	mock := newMockRunner()
	mock.onCommand("ls", []byte("files\n"), 0)
	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{
		Action: "exec",
		Params: map[string]any{"command": "ls"},
	})
	if !resp.OK {
		t.Errorf("exec should be OK: %s", resp.Error)
	}
}

func TestHandleDispatchRead(t *testing.T) {
	mock := newMockRunner()
	encoded := base64.StdEncoding.EncodeToString([]byte("content"))
	mock.onCommand("base64 '/etc/hostname'", []byte(encoded), 0)
	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{
		Action: "read",
		Params: map[string]any{"path": "/etc/hostname"},
	})
	if !resp.OK {
		t.Errorf("read should be OK: %s", resp.Error)
	}
}

func TestHandleDispatchWrite(t *testing.T) {
	mock := newMockRunner()
	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{
		Action: "write",
		Params: map[string]any{"path": "/tmp/test", "content": "hello"},
	})
	if !resp.OK {
		t.Errorf("write should be OK: %s", resp.Error)
	}
}

func TestHandleDispatchUpload(t *testing.T) {
	mock := newMockRunner()
	dir := t.TempDir()
	localFile := filepath.Join(dir, "test.txt")
	os.WriteFile(localFile, []byte("data"), 0644)

	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{
		Action: "upload",
		Params: map[string]any{"local_path": localFile, "remote_path": "/tmp/remote"},
	})
	if !resp.OK {
		t.Errorf("upload should be OK: %s", resp.Error)
	}
}

func TestHandleDispatchDownload(t *testing.T) {
	mock := newMockRunner()
	dir := t.TempDir()
	localPath := filepath.Join(dir, "out.txt")
	mock.onCommand("cat '/tmp/remote'", []byte("content"), 0)

	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{
		Action: "download",
		Params: map[string]any{"remote_path": "/tmp/remote", "local_path": localPath},
	})
	if !resp.OK {
		t.Errorf("download should be OK: %s", resp.Error)
	}
}

func TestHandleDispatchEdit(t *testing.T) {
	mock := newMockRunner()
	result, _ := json.Marshal(protocol.EditResult{Modified: true, Message: "ok"})
	mock.defaultResponse = mockResponse{stdout: result, exitCode: 0}

	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{
		Action: "edit",
		Params: map[string]any{"path": "/tmp/f", "old": "a", "new": "b"},
	})
	if !resp.OK {
		t.Errorf("edit should be OK: %s", resp.Error)
	}
}

func TestHandleDispatchLs(t *testing.T) {
	mock := newMockRunner()
	mock.defaultResponse = mockResponse{stdout: []byte(""), exitCode: 0}
	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{
		Action: "ls",
		Params: map[string]any{"path": "/tmp"},
	})
	if !resp.OK {
		t.Errorf("ls should be OK: %s", resp.Error)
	}
}

func TestHandleDispatchPs(t *testing.T) {
	mock := newMockRunner()
	result, _ := json.Marshal(protocol.ProcessList{Processes: []protocol.ProcessInfo{}})
	mock.defaultResponse = mockResponse{stdout: result, exitCode: 0}

	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{Action: "ps"})
	if !resp.OK {
		t.Errorf("ps should be OK: %s", resp.Error)
	}
}

func TestHandleDispatchSysinfo(t *testing.T) {
	mock := newMockRunner()
	result, _ := json.Marshal(protocol.SystemInfo{Hostname: "test"})
	mock.defaultResponse = mockResponse{stdout: result, exitCode: 0}

	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{Action: "sysinfo"})
	if !resp.OK {
		t.Errorf("sysinfo should be OK: %s", resp.Error)
	}
}

func TestHandleDispatchDisconnect(t *testing.T) {
	done := make(chan struct{})
	oldExit := exitFunc
	exitFunc = func(code int) { close(done) }
	defer func() { exitFunc = oldExit }()

	mock := newMockRunner()
	d := &Daemon{
		runner:     mock,
		remotePath: "/tmp/.remote-agent-test",
		sockPath:   filepath.Join(t.TempDir(), "test.sock"),
		pidPath:    filepath.Join(t.TempDir(), "test.pid"),
	}
	h := &Handler{daemon: d}

	resp := h.Handle(&protocol.DaemonRequest{Action: "disconnect"})
	if !resp.OK {
		t.Errorf("disconnect should be OK: %s", resp.Error)
	}
	<-done
}
