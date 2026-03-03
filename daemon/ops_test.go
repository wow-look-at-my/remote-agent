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
	"github.com/wow-look-at-my/testify/require"
)

// mockRunner records commands and returns configured responses.
type mockRunner struct {
	calls		[]mockCall
	responses	map[string]mockResponse
	// fallback response for unmatched commands
	defaultResponse	mockResponse
}

type mockCall struct {
	Command	string
	Stdin	[]byte
}

type mockResponse struct {
	stdout		[]byte
	stderr		[]byte
	exitCode	int
	err		error
}

func newMockRunner() *mockRunner {
	return &mockRunner{
		responses:	make(map[string]mockResponse),
		defaultResponse: mockResponse{
			stdout:		[]byte(""),
			exitCode:	0,
		},
	}
}

func (m *mockRunner) Run(command string) (stdout, stderr []byte, exitCode int, err error) {
	m.calls = append(m.calls, mockCall{Command: command})
	if resp, ok := m.responses[command]; ok {
		return resp.stdout, resp.stderr, resp.exitCode, resp.err
	}
	return m.defaultResponse.stdout, m.defaultResponse.stderr, m.defaultResponse.exitCode, m.defaultResponse.err
}

func (m *mockRunner) RunStdin(command string, stdin []byte) (stdout, stderr []byte, exitCode int, err error) {
	m.calls = append(m.calls, mockCall{Command: command, Stdin: stdin})
	if resp, ok := m.responses[command]; ok {
		return resp.stdout, resp.stderr, resp.exitCode, resp.err
	}
	return m.defaultResponse.stdout, m.defaultResponse.stderr, m.defaultResponse.exitCode, m.defaultResponse.err
}

func (m *mockRunner) onCommand(cmd string, stdout []byte, exitCode int) {
	m.responses[cmd] = mockResponse{stdout: stdout, exitCode: exitCode}
}

func (m *mockRunner) onCommandErr(cmd string, stderr []byte, exitCode int) {
	m.responses[cmd] = mockResponse{stderr: stderr, exitCode: exitCode}
}

func (m *mockRunner) onCommandFail(cmd string, err error) {
	m.responses[cmd] = mockResponse{err: err}
}

func newTestHandler() (*Handler, *mockRunner) {
	mock := newMockRunner()
	d := &Daemon{
		runner:		mock,
		remotePath:	"/tmp/.remote-agent-test",
	}
	return &Handler{daemon: d}, mock
}

func TestHandlePing(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("echo pong", []byte("pong\n"), 0)

	resp := h.handlePing()
	assert.True(t, resp.OK)

}

func TestHandlePingFail(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandFail("echo pong", fmt.Errorf("connection lost"))

	resp := h.handlePing()
	assert.False(t, resp.OK)

}

func TestHandleExec(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("ls -la", []byte("total 0\n"), 0)

	resp := h.handleExec(map[string]any{"command": "ls -la"})
	assert.True(t, resp.OK)

}

func TestHandleExecMissingCommand(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleExec(map[string]any{})
	assert.False(t, resp.OK)

}

func TestHandleExecError(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandFail("whoami", fmt.Errorf("ssh error"))

	resp := h.handleExec(map[string]any{"command": "whoami"})
	assert.False(t, resp.OK)

}

func TestHandleExecWithNonZeroExit(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("false", []byte(""), 1)

	resp := h.handleExec(map[string]any{"command": "false"})
	assert.True(t, resp.OK)

}

func TestHandleRead(t *testing.T) {
	h, mock := newTestHandler()
	content := "hello world"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	mock.onCommand("base64 '/etc/hostname'", []byte(encoded+"\n"), 0)

	resp := h.handleRead(map[string]any{"path": "/etc/hostname"})
	assert.True(t, resp.OK)

}

func TestHandleReadMissingPath(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleRead(map[string]any{})
	assert.False(t, resp.OK)

}

func TestHandleReadNotFound(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandErr("base64 '/nonexistent'", []byte("No such file"), 1)

	resp := h.handleRead(map[string]any{"path": "/nonexistent"})
	assert.False(t, resp.OK)

}

func TestHandleReadFail(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandFail("base64 '/test'", fmt.Errorf("connection error"))

	resp := h.handleRead(map[string]any{"path": "/test"})
	assert.False(t, resp.OK)

}

func TestHandleWrite(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{exitCode: 0}

	resp := h.handleWrite(map[string]any{"path": "/tmp/test.txt", "content": "hello"})
	assert.True(t, resp.OK)
	assert.GreaterOrEqual(t,// Verify audit was called
	len(mock.calls), 2)

}

func TestHandleWriteMissingPath(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleWrite(map[string]any{})
	assert.False(t, resp.OK)

}

func TestHandleWriteError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stderr: []byte("permission denied"), exitCode: 1}

	resp := h.handleWrite(map[string]any{"path": "/root/test.txt", "content": "x"})
	assert.False(t, resp.OK)

}

func TestHandleUpload(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{exitCode: 0}

	dir := t.TempDir()
	localFile := filepath.Join(dir, "upload.txt")
	os.WriteFile(localFile, []byte("upload content"), 0644)

	resp := h.handleUpload(map[string]any{"local_path": localFile, "remote_path": "/tmp/remote.txt"})
	assert.True(t, resp.OK)

}

func TestHandleUploadMissingParams(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleUpload(map[string]any{})
	assert.False(t, resp.OK)

}

func TestHandleUploadLocalNotFound(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleUpload(map[string]any{"local_path": "/nonexistent", "remote_path": "/tmp/x"})
	assert.False(t, resp.OK)

}

func TestHandleUploadSSHError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{err: fmt.Errorf("ssh error")}

	dir := t.TempDir()
	f := filepath.Join(dir, "test.txt")
	os.WriteFile(f, []byte("data"), 0644)

	resp := h.handleUpload(map[string]any{"local_path": f, "remote_path": "/tmp/x"})
	assert.False(t, resp.OK)

}

func TestHandleDownload(t *testing.T) {
	h, mock := newTestHandler()

	dir := t.TempDir()
	localPath := filepath.Join(dir, "downloaded.txt")

	mock.onCommand("cat '/tmp/remote.txt'", []byte("file content"), 0)

	resp := h.handleDownload(map[string]any{"remote_path": "/tmp/remote.txt", "local_path": localPath})
	assert.True(t, resp.OK)

	data, _ := os.ReadFile(localPath)
	assert.Equal(t, "file content", string(data))

}

func TestHandleDownloadMissingParams(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleDownload(map[string]any{})
	assert.False(t, resp.OK)

}

func TestHandleDownloadError(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandErr("cat '/tmp/missing'", []byte("not found"), 1)

	resp := h.handleDownload(map[string]any{"remote_path": "/tmp/missing", "local_path": "/tmp/out"})
	assert.False(t, resp.OK)

}

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
	assert.GreaterOrEqual(t,// Verify audit command was called before actual command
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
