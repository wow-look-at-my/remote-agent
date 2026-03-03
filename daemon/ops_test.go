package daemon

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
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
	assert.GreaterOrEqual(t, len(mock.calls), 2) // Verify audit was called
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
