package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/remote-agent/protocol"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestHandleEdit(t *testing.T) {
	h, mock := newTestHandler()
	result, _ := json.Marshal(protocol.EditResult{Modified: true, Message: "done"})
	mock.defaultResponse = mockResponse{stdout: result, exitCode: 0}

	resp := h.handleEdit(map[string]any{"path": "/tmp/file.txt", "old": "hello", "new": "world"})
	assert.True(t, resp.OK)

}

func TestHandleEditMissingParams(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleEdit(map[string]any{})
	assert.False(t, resp.OK)

}

func TestHandleEditErrorResponse(t *testing.T) {
	h, mock := newTestHandler()
	errResp, _ := json.Marshal(map[string]string{"error": "text not found"})
	mock.defaultResponse = mockResponse{stdout: errResp, exitCode: 0}

	resp := h.handleEdit(map[string]any{"path": "/tmp/file.txt", "old": "missing", "new": "x"})
	assert.False(t, resp.OK)

}

func TestHandleLs(t *testing.T) {
	h, mock := newTestHandler()
	output := "regular file\t100\t644\t1709300000\t/tmp/a.txt\nd\t4096\t755\t1709300000\t/tmp/subdir\n"
	mock.defaultResponse = mockResponse{stdout: []byte(output), exitCode: 0}

	resp := h.handleLs(map[string]any{"path": "/tmp"})
	assert.True(t, resp.OK)

}

func TestHandleLsDefaultPath(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stdout: []byte(""), exitCode: 0}

	resp := h.handleLs(map[string]any{})
	assert.True(t, resp.OK)

}

func TestHandleLsRecursive(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stdout: []byte("d\t4096\t755\t1709300000\t/tmp/dir\n"), exitCode: 0}

	resp := h.handleLs(map[string]any{"path": "/tmp", "recursive": true})
	assert.True(t, resp.OK)

}

func TestHandlePs(t *testing.T) {
	h, mock := newTestHandler()
	result, _ := json.Marshal(protocol.ProcessList{
		Processes: []protocol.ProcessInfo{{PID: 1, Command: "init"}},
	})
	mock.defaultResponse = mockResponse{stdout: result, exitCode: 0}

	resp := h.handlePs(map[string]any{})
	assert.True(t, resp.OK)

}

func TestHandlePsWithFilter(t *testing.T) {
	h, mock := newTestHandler()
	result, _ := json.Marshal(protocol.ProcessList{Processes: []protocol.ProcessInfo{}})
	mock.defaultResponse = mockResponse{stdout: result, exitCode: 0}

	resp := h.handlePs(map[string]any{"filter": "nginx"})
	assert.True(t, resp.OK)

}

func TestHandleSysinfo(t *testing.T) {
	h, mock := newTestHandler()
	result, _ := json.Marshal(protocol.SystemInfo{
		Hostname:	"test", OS: "Ubuntu", Arch: "amd64",
	})
	mock.defaultResponse = mockResponse{stdout: result, exitCode: 0}

	resp := h.handleSysinfo()
	assert.True(t, resp.OK)

}

func TestHandleSysinfoError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stderr: []byte("error"), exitCode: 1}

	resp := h.handleSysinfo()
	assert.False(t, resp.OK)

}

func TestParseLsOutput(t *testing.T) {
	output := "d\t4096\t755\t1709300000\t/tmp/subdir\nregular file\t100\t644\t1709300000\t/tmp/test.txt\n"
	entries := parseLsOutput(output)
	require.Equal(t, 2, len(entries))
	assert.True(t, entries[0].IsDir)
	assert.False(t, entries[1].IsDir)
	assert.Equal(t, int64(100), entries[1].Size)

}

func TestParseLsOutputSkipsDotEntries(t *testing.T) {
	output := "d\t4096\t755\t1709300000\t.\nd\t4096\t755\t1709300000\t..\nd\t4096\t755\t1709300000\t/tmp/real\n"
	entries := parseLsOutput(output)
	assert.Equal(t, 1, len(entries))

}

func TestParseLsOutputEmpty(t *testing.T) {
	entries := parseLsOutput("")
	assert.Equal(t, 0, len(entries))

}

func TestParseLsOutputMalformed(t *testing.T) {
	output := "not enough fields\n"
	entries := parseLsOutput(output)
	assert.Equal(t, 0, len(entries))

}

func TestParseInt64(t *testing.T) {
	tests := []struct {
		input	string
		want	int64
	}{
		{"0", 0},
		{"42", 42},
		{"1234567890", 1234567890},
		{"-1", -1},
		{"", 0},
		{"abc", 0},
		{"12.34", 12},
	}
	for _, tt := range tests {
		got := parseInt64(tt.input)
		assert.Equal(t, tt.want, got)

	}
}

func TestHandleDisconnect(t *testing.T) {
	// Override exit function to prevent os.Exit during test
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

	resp := h.handleDisconnect()
	assert.True(t, resp.OK)

	// Wait for the goroutine to complete before restoring exitFunc
	<-done
}

func TestHandleReadBadBase64(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("base64 '/test'", []byte("not-valid-base64!!!"), 0)

	resp := h.handleRead(map[string]any{"path": "/test"})
	assert.False(t, resp.OK)

}

func TestHandleWriteDefaultMode(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{exitCode: 0}

	// Write without specifying mode (should default to 0644)
	resp := h.handleWrite(map[string]any{"path": "/tmp/test.txt", "content": "hello", "mode": ""})
	assert.True(t, resp.OK)

}

func TestHandleWriteSSHError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{err: fmt.Errorf("ssh connection lost")}

	resp := h.handleWrite(map[string]any{"path": "/tmp/test.txt", "content": "hello"})
	assert.False(t, resp.OK)

}

func TestHandleUploadExitNonZero(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stderr: []byte("disk full"), exitCode: 1}

	dir := t.TempDir()
	f := filepath.Join(dir, "test.txt")
	os.WriteFile(f, []byte("data"), 0644)

	resp := h.handleUpload(map[string]any{"local_path": f, "remote_path": "/tmp/x"})
	assert.False(t, resp.OK)

}

func TestHandleDownloadSSHError(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandFail("cat '/tmp/missing'", fmt.Errorf("ssh error"))

	resp := h.handleDownload(map[string]any{"remote_path": "/tmp/missing", "local_path": "/tmp/out"})
	assert.False(t, resp.OK)

}

func TestHandleDownloadWriteLocalFail(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("cat '/tmp/remote'", []byte("data"), 0)

	// Try to write to a directory that doesn't exist
	resp := h.handleDownload(map[string]any{
		"remote_path":	"/tmp/remote",
		"local_path":	"/nonexistent/dir/file.txt",
	})
	assert.False(t, resp.OK)

}

func TestHandleEditSSHError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{err: fmt.Errorf("ssh error")}

	resp := h.handleEdit(map[string]any{"path": "/tmp/f", "old": "a", "new": "b"})
	assert.False(t, resp.OK)

}

func TestHandleEditNonZeroExit(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stderr: []byte("failed"), exitCode: 1}

	resp := h.handleEdit(map[string]any{"path": "/tmp/f", "old": "a", "new": "b"})
	assert.False(t, resp.OK)

}

func TestHandleEditBadJSON(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stdout: []byte("not json"), exitCode: 0}

	resp := h.handleEdit(map[string]any{"path": "/tmp/f", "old": "a", "new": "b"})
	assert.False(t, resp.OK)

}

func TestHandleLsError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{err: fmt.Errorf("ssh error")}

	resp := h.handleLs(map[string]any{"path": "/tmp"})
	assert.False(t, resp.OK)

}

func TestHandlePsError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{err: fmt.Errorf("ssh error")}

	resp := h.handlePs(map[string]any{})
	assert.False(t, resp.OK)

}

func TestHandlePsNonZeroExit(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stderr: []byte("fail"), exitCode: 1}

	resp := h.handlePs(map[string]any{})
	assert.False(t, resp.OK)

}

func TestHandlePsBadJSON(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stdout: []byte("not json"), exitCode: 0}

	resp := h.handlePs(map[string]any{})
	assert.False(t, resp.OK)

}

func TestHandleSysinfoSSHError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{err: fmt.Errorf("ssh error")}

	resp := h.handleSysinfo()
	assert.False(t, resp.OK)

}

func TestHandleSysinfoBadJSON(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stdout: []byte("not json"), exitCode: 0}

	resp := h.handleSysinfo()
	assert.False(t, resp.OK)

}

func TestHandleExecAudit(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("whoami", []byte("root\n"), 0)

	resp := h.handleExec(map[string]any{"command": "whoami"})
	assert.True(t, resp.OK)
	assert.GreaterOrEqual(t, // Verify audit command was called before actual command
		len(mock.calls), 2)

}

func TestParseLsOutputFractionalTime(t *testing.T) {
	output := "d\t4096\t755\t1709300000.123456\t/tmp/dir\n"
	entries := parseLsOutput(output)
	require.Equal(t, 1, len(entries))
	assert.Equal(t, int64(1709300000), entries[0].ModTime)

}

func TestParseLsOutputSkipsDotInPath(t *testing.T) {
	output := "d\t4096\t755\t1709300000\t/path/to/.\nd\t4096\t755\t1709300000\t/path/to/..\nf\t100\t644\t1709300000\t/path/to/file\n"
	entries := parseLsOutput(output)
	assert.Equal(t, 1, len(entries))

}
