package daemon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/remote-agent/protocol"
	"github.com/wow-look-at-my/testify/assert"
)

func TestHandlerUnknownAction(t *testing.T) {
	h := &Handler{daemon: &Daemon{}}
	resp := h.Handle(&protocol.DaemonRequest{Action: "nonexistent"})
	assert.False(t, resp.OK)
	assert.NotEqual(t, "", resp.Error)

}

func TestOkResponse(t *testing.T) {
	resp := okResponse("test")
	assert.True(t, resp.OK)
	assert.Equal(t, "test", resp.Data)

}

func TestErrResponse(t *testing.T) {
	resp := errResponse(fmt.Errorf("test error"))
	assert.False(t, resp.OK)
	assert.Equal(t, "test error", resp.Error)

}

// TestHandleDispatch tests that Handle routes each action correctly.
func TestHandleDispatchPing(t *testing.T) {
	mock := newMockRunner()
	mock.onCommand("echo pong", []byte("pong\n"), 0)
	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{Action: "ping"})
	assert.True(t, resp.OK)

}

func TestHandleDispatchExec(t *testing.T) {
	mock := newMockRunner()
	mock.onCommand("ls", []byte("files\n"), 0)
	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{
		Action:	"exec",
		Params:	map[string]any{"command": "ls"},
	})
	assert.True(t, resp.OK)

}

func TestHandleDispatchRead(t *testing.T) {
	mock := newMockRunner()
	encoded := base64.StdEncoding.EncodeToString([]byte("content"))
	mock.onCommand("base64 '/etc/hostname'", []byte(encoded), 0)
	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{
		Action:	"read",
		Params:	map[string]any{"path": "/etc/hostname"},
	})
	assert.True(t, resp.OK)

}

func TestHandleDispatchWrite(t *testing.T) {
	mock := newMockRunner()
	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{
		Action:	"write",
		Params:	map[string]any{"path": "/tmp/test", "content": "hello"},
	})
	assert.True(t, resp.OK)

}

func TestHandleDispatchUpload(t *testing.T) {
	mock := newMockRunner()
	dir := t.TempDir()
	localFile := filepath.Join(dir, "test.txt")
	os.WriteFile(localFile, []byte("data"), 0644)

	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{
		Action:	"upload",
		Params:	map[string]any{"local_path": localFile, "remote_path": "/tmp/remote"},
	})
	assert.True(t, resp.OK)

}

func TestHandleDispatchDownload(t *testing.T) {
	mock := newMockRunner()
	dir := t.TempDir()
	localPath := filepath.Join(dir, "out.txt")
	mock.onCommand("cat '/tmp/remote'", []byte("content"), 0)

	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{
		Action:	"download",
		Params:	map[string]any{"remote_path": "/tmp/remote", "local_path": localPath},
	})
	assert.True(t, resp.OK)

}

func TestHandleDispatchEdit(t *testing.T) {
	mock := newMockRunner()
	result, _ := json.Marshal(protocol.EditResult{Modified: true, Message: "ok"})
	mock.defaultResponse = mockResponse{stdout: result, exitCode: 0}

	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{
		Action:	"edit",
		Params:	map[string]any{"path": "/tmp/f", "old": "a", "new": "b"},
	})
	assert.True(t, resp.OK)

}

func TestHandleDispatchLs(t *testing.T) {
	mock := newMockRunner()
	mock.defaultResponse = mockResponse{stdout: []byte(""), exitCode: 0}
	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{
		Action:	"ls",
		Params:	map[string]any{"path": "/tmp"},
	})
	assert.True(t, resp.OK)

}

func TestHandleDispatchPs(t *testing.T) {
	mock := newMockRunner()
	result, _ := json.Marshal(protocol.ProcessList{Processes: []protocol.ProcessInfo{}})
	mock.defaultResponse = mockResponse{stdout: result, exitCode: 0}

	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{Action: "ps"})
	assert.True(t, resp.OK)

}

func TestHandleDispatchSysinfo(t *testing.T) {
	mock := newMockRunner()
	result, _ := json.Marshal(protocol.SystemInfo{Hostname: "test"})
	mock.defaultResponse = mockResponse{stdout: result, exitCode: 0}

	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{Action: "sysinfo"})
	assert.True(t, resp.OK)

}

func TestHandleDispatchDisconnect(t *testing.T) {
	done := make(chan struct{})
	oldExit := exitFunc
	exitFunc = func(code int) { close(done) }
	defer func() { exitFunc = oldExit }()

	mock := newMockRunner()
	d := &Daemon{
		runner:		mock,
		remotePath:	"/tmp/.remote-agent-test",
		sockPath:	filepath.Join(t.TempDir(), "test.sock"),
		pidPath:	filepath.Join(t.TempDir(), "test.pid"),
	}
	h := &Handler{daemon: d}

	resp := h.Handle(&protocol.DaemonRequest{Action: "disconnect"})
	assert.True(t, resp.OK)

	<-done
}
