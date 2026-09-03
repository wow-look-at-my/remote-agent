package daemon

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// mockRunner records commands and returns configured responses. It is safe
// for concurrent use: audit commands run on their own goroutines.
type mockRunner struct {
	mu        sync.Mutex
	calls     []mockCall
	responses map[string]mockResponse
	// fallback response for unmatched commands
	defaultResponse mockResponse
}

type mockCall struct {
	Command string
	Stdin   []byte
	Timeout time.Duration
}

type mockResponse struct {
	stdout   []byte
	stderr   []byte
	exitCode int
	err      error
}

func newMockRunner() *mockRunner {
	return &mockRunner{
		responses: make(map[string]mockResponse),
		defaultResponse: mockResponse{
			stdout:   []byte(""),
			exitCode: 0,
		},
	}
}

func (m *mockRunner) Run(command string) (stdout, stderr []byte, exitCode int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockCall{Command: command})
	if resp, ok := m.responses[command]; ok {
		return resp.stdout, resp.stderr, resp.exitCode, resp.err
	}
	return m.defaultResponse.stdout, m.defaultResponse.stderr, m.defaultResponse.exitCode, m.defaultResponse.err
}

func (m *mockRunner) RunTimeout(command string, d time.Duration) (stdout, stderr []byte, exitCode int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockCall{Command: command, Timeout: d})
	if resp, ok := m.responses[command]; ok {
		return resp.stdout, resp.stderr, resp.exitCode, resp.err
	}
	return m.defaultResponse.stdout, m.defaultResponse.stderr, m.defaultResponse.exitCode, m.defaultResponse.err
}

func (m *mockRunner) RunStdin(command string, stdin []byte) (stdout, stderr []byte, exitCode int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockCall{Command: command, Stdin: stdin})
	if resp, ok := m.responses[command]; ok {
		return resp.stdout, resp.stderr, resp.exitCode, resp.err
	}
	return m.defaultResponse.stdout, m.defaultResponse.stderr, m.defaultResponse.exitCode, m.defaultResponse.err
}

// snapshotCalls returns a copy of the recorded calls, safe against concurrent
// audit goroutines.
func (m *mockRunner) snapshotCalls() []mockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mockCall(nil), m.calls...)
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
		runner:     mock,
		remotePath: "/tmp/.remote-agent-test",
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
	mock.onCommand("cat '/etc/hostname'", []byte("hello world"), 0)

	resp := h.handleRead(map[string]any{"path": "/etc/hostname"})
	assert.True(t, resp.OK)

	info, ok := resp.Data.(protocol.FileInfo)
	assert.True(t, ok, "expected FileInfo, got %T", resp.Data)
	assert.Equal(t, "hello world", info.Content)
	assert.Equal(t, "", info.ContentB64, "text content must not be base64-framed")
	assert.Equal(t, int64(11), info.Size)
}

func TestHandleReadBinaryContent(t *testing.T) {
	h, mock := newTestHandler()
	binary := []byte{0x7f, 'E', 'L', 'F', 0x00, 0xff, 0x80} // not valid UTF-8
	mock.onCommand("cat '/bin/blob'", binary, 0)

	resp := h.handleRead(map[string]any{"path": "/bin/blob"})
	assert.True(t, resp.OK)

	info, ok := resp.Data.(protocol.FileInfo)
	assert.True(t, ok, "expected FileInfo, got %T", resp.Data)
	assert.Equal(t, "", info.Content)
	decoded, err := base64.StdEncoding.DecodeString(info.ContentB64)
	assert.Nil(t, err)
	assert.Equal(t, binary, decoded, "binary bytes must survive the JSON hop exactly")
	assert.Equal(t, int64(len(binary)), info.Size)
}

func TestHandleReadMissingPath(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleRead(map[string]any{})
	assert.False(t, resp.OK)
}

func TestHandleReadNotFound(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandErr("cat '/nonexistent'", []byte("No such file"), 1)

	resp := h.handleRead(map[string]any{"path": "/nonexistent"})
	assert.False(t, resp.OK)
}

func TestHandleReadFail(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandFail("cat '/test'", fmt.Errorf("connection error"))

	resp := h.handleRead(map[string]any{"path": "/test"})
	assert.False(t, resp.OK)
}

func TestHandleWrite(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{exitCode: 0}

	resp := h.handleWrite(map[string]any{"path": "/tmp/test.txt", "content": "hello"})
	assert.True(t, resp.OK)
	h.daemon.auditWG.Wait()                                // audits run async; drain before asserting
	assert.GreaterOrEqual(t, len(mock.snapshotCalls()), 2) // Verify audit was called
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

func TestHandleWriteStreamsViaStdin(t *testing.T) {
	h, mock := newTestHandler()
	// 300 KiB, which is past the kernel's 128 KiB per-argument cap.
	content := strings.Repeat("x", 300*1024)

	resp := h.handleWrite(map[string]any{"path": "/tmp/big.bin", "content": content})
	assert.True(t, resp.OK)

	found := false
	for _, c := range mock.snapshotCalls() {
		if strings.HasPrefix(c.Command, "cat > '/tmp/big.bin'") {
			found = true
			assert.Equal(t, []byte(content), c.Stdin, "content must stream via stdin")
			assert.Less(t, len(c.Command), 1024, "content must not be embedded in the command line")
		}
	}
	assert.True(t, found, "expected a cat > write command")
}

func TestHandleWriteContentB64Binary(t *testing.T) {
	h, mock := newTestHandler()
	binary := []byte{0x00, 0xff, 0xfe, 'a', 0x80, 0x01} // invalid UTF-8
	resp := h.handleWrite(map[string]any{
		"path":        "/tmp/blob",
		"content_b64": base64.StdEncoding.EncodeToString(binary),
	})
	assert.True(t, resp.OK)

	found := false
	for _, c := range mock.snapshotCalls() {
		if strings.HasPrefix(c.Command, "cat > '/tmp/blob'") {
			found = true
			assert.Equal(t, binary, c.Stdin, "binary bytes must reach the remote unmodified")
		}
	}
	assert.True(t, found, "expected a cat > write command")
}

func TestHandleWriteBadContentB64(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleWrite(map[string]any{"path": "/tmp/x", "content_b64": "!!!not-base64!!!"})
	assert.False(t, resp.OK)
}

func TestHandleWriteRejectsNonOctalMode(t *testing.T) {
	h, _ := newTestHandler()
	for _, mode := range []string{"u+x", "rwxr-xr-x", "755; rm -rf /", "99", "07555 ", "0x644"} {
		resp := h.handleWrite(map[string]any{"path": "/tmp/x", "content": "hi", "mode": mode})
		assert.False(t, resp.OK, "mode %q must be rejected", mode)
	}
}

func TestValidChmodMode(t *testing.T) {
	for _, ok := range []string{"644", "0644", "755", "4755", "777"} {
		assert.True(t, validChmodMode(ok), "mode %q should be accepted", ok)
	}
	for _, bad := range []string{"", "6", "64", "07777", "abc", "u+x", "6 4"} {
		assert.False(t, validChmodMode(bad), "mode %q should be rejected", bad)
	}
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
