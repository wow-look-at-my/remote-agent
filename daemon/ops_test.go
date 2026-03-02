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

// mockRunner records commands and returns configured responses.
type mockRunner struct {
	calls     []mockCall
	responses map[string]mockResponse
	// fallback response for unmatched commands
	defaultResponse mockResponse
}

type mockCall struct {
	Command string
	Stdin   []byte
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
		runner:     mock,
		remotePath: "/tmp/.remote-agent-test",
	}
	return &Handler{daemon: d}, mock
}

func TestHandlePing(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("echo pong", []byte("pong\n"), 0)

	resp := h.handlePing()
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}
}

func TestHandlePingFail(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandFail("echo pong", fmt.Errorf("connection lost"))

	resp := h.handlePing()
	if resp.OK {
		t.Error("expected error")
	}
}

func TestHandleExec(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("ls -la", []byte("total 0\n"), 0)

	resp := h.handleExec(map[string]any{"command": "ls -la"})
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}
}

func TestHandleExecMissingCommand(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleExec(map[string]any{})
	if resp.OK {
		t.Error("expected error for missing command")
	}
}

func TestHandleExecError(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandFail("whoami", fmt.Errorf("ssh error"))

	resp := h.handleExec(map[string]any{"command": "whoami"})
	if resp.OK {
		t.Error("expected error")
	}
}

func TestHandleExecWithNonZeroExit(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("false", []byte(""), 1)

	resp := h.handleExec(map[string]any{"command": "false"})
	if !resp.OK {
		t.Errorf("non-zero exit should still be OK (exit code in data): %s", resp.Error)
	}
}

func TestHandleRead(t *testing.T) {
	h, mock := newTestHandler()
	content := "hello world"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	mock.onCommand("base64 '/etc/hostname'", []byte(encoded+"\n"), 0)

	resp := h.handleRead(map[string]any{"path": "/etc/hostname"})
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}
}

func TestHandleReadMissingPath(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleRead(map[string]any{})
	if resp.OK {
		t.Error("expected error for missing path")
	}
}

func TestHandleReadNotFound(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandErr("base64 '/nonexistent'", []byte("No such file"), 1)

	resp := h.handleRead(map[string]any{"path": "/nonexistent"})
	if resp.OK {
		t.Error("expected error for nonexistent file")
	}
}

func TestHandleReadFail(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandFail("base64 '/test'", fmt.Errorf("connection error"))

	resp := h.handleRead(map[string]any{"path": "/test"})
	if resp.OK {
		t.Error("expected error")
	}
}

func TestHandleWrite(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{exitCode: 0}

	resp := h.handleWrite(map[string]any{"path": "/tmp/test.txt", "content": "hello"})
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}

	// Verify audit was called
	if len(mock.calls) < 2 {
		t.Error("expected at least 2 calls (audit + write)")
	}
}

func TestHandleWriteMissingPath(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleWrite(map[string]any{})
	if resp.OK {
		t.Error("expected error for missing path")
	}
}

func TestHandleWriteError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stderr: []byte("permission denied"), exitCode: 1}

	resp := h.handleWrite(map[string]any{"path": "/root/test.txt", "content": "x"})
	if resp.OK {
		t.Error("expected error for permission denied")
	}
}

func TestHandleUpload(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{exitCode: 0}

	dir := t.TempDir()
	localFile := filepath.Join(dir, "upload.txt")
	os.WriteFile(localFile, []byte("upload content"), 0644)

	resp := h.handleUpload(map[string]any{"local_path": localFile, "remote_path": "/tmp/remote.txt"})
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}
}

func TestHandleUploadMissingParams(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleUpload(map[string]any{})
	if resp.OK {
		t.Error("expected error for missing params")
	}
}

func TestHandleUploadLocalNotFound(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleUpload(map[string]any{"local_path": "/nonexistent", "remote_path": "/tmp/x"})
	if resp.OK {
		t.Error("expected error for nonexistent local file")
	}
}

func TestHandleUploadSSHError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{err: fmt.Errorf("ssh error")}

	dir := t.TempDir()
	f := filepath.Join(dir, "test.txt")
	os.WriteFile(f, []byte("data"), 0644)

	resp := h.handleUpload(map[string]any{"local_path": f, "remote_path": "/tmp/x"})
	if resp.OK {
		t.Error("expected error")
	}
}

func TestHandleDownload(t *testing.T) {
	h, mock := newTestHandler()

	dir := t.TempDir()
	localPath := filepath.Join(dir, "downloaded.txt")

	mock.onCommand("cat '/tmp/remote.txt'", []byte("file content"), 0)

	resp := h.handleDownload(map[string]any{"remote_path": "/tmp/remote.txt", "local_path": localPath})
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}

	data, _ := os.ReadFile(localPath)
	if string(data) != "file content" {
		t.Errorf("downloaded content = %q", string(data))
	}
}

func TestHandleDownloadMissingParams(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleDownload(map[string]any{})
	if resp.OK {
		t.Error("expected error for missing params")
	}
}

func TestHandleDownloadError(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandErr("cat '/tmp/missing'", []byte("not found"), 1)

	resp := h.handleDownload(map[string]any{"remote_path": "/tmp/missing", "local_path": "/tmp/out"})
	if resp.OK {
		t.Error("expected error")
	}
}

func TestHandleEdit(t *testing.T) {
	h, mock := newTestHandler()
	result, _ := json.Marshal(protocol.EditResult{Modified: true, Message: "done"})
	mock.defaultResponse = mockResponse{stdout: result, exitCode: 0}

	resp := h.handleEdit(map[string]any{"path": "/tmp/file.txt", "old": "hello", "new": "world"})
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}
}

func TestHandleEditMissingParams(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleEdit(map[string]any{})
	if resp.OK {
		t.Error("expected error for missing params")
	}
}

func TestHandleEditErrorResponse(t *testing.T) {
	h, mock := newTestHandler()
	errResp, _ := json.Marshal(map[string]string{"error": "text not found"})
	mock.defaultResponse = mockResponse{stdout: errResp, exitCode: 0}

	resp := h.handleEdit(map[string]any{"path": "/tmp/file.txt", "old": "missing", "new": "x"})
	if resp.OK {
		t.Error("expected error for text not found")
	}
}

func TestHandleLs(t *testing.T) {
	h, mock := newTestHandler()
	output := "regular file\t100\t644\t1709300000\t/tmp/a.txt\nd\t4096\t755\t1709300000\t/tmp/subdir\n"
	mock.defaultResponse = mockResponse{stdout: []byte(output), exitCode: 0}

	resp := h.handleLs(map[string]any{"path": "/tmp"})
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}
}

func TestHandleLsDefaultPath(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stdout: []byte(""), exitCode: 0}

	resp := h.handleLs(map[string]any{})
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}
}

func TestHandleLsRecursive(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stdout: []byte("d\t4096\t755\t1709300000\t/tmp/dir\n"), exitCode: 0}

	resp := h.handleLs(map[string]any{"path": "/tmp", "recursive": true})
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}
}

func TestHandlePs(t *testing.T) {
	h, mock := newTestHandler()
	result, _ := json.Marshal(protocol.ProcessList{
		Processes: []protocol.ProcessInfo{{PID: 1, Command: "init"}},
	})
	mock.defaultResponse = mockResponse{stdout: result, exitCode: 0}

	resp := h.handlePs(map[string]any{})
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}
}

func TestHandlePsWithFilter(t *testing.T) {
	h, mock := newTestHandler()
	result, _ := json.Marshal(protocol.ProcessList{Processes: []protocol.ProcessInfo{}})
	mock.defaultResponse = mockResponse{stdout: result, exitCode: 0}

	resp := h.handlePs(map[string]any{"filter": "nginx"})
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}
}

func TestHandleSysinfo(t *testing.T) {
	h, mock := newTestHandler()
	result, _ := json.Marshal(protocol.SystemInfo{
		Hostname: "test", OS: "Ubuntu", Arch: "amd64",
	})
	mock.defaultResponse = mockResponse{stdout: result, exitCode: 0}

	resp := h.handleSysinfo()
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}
}

func TestHandleSysinfoError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stderr: []byte("error"), exitCode: 1}

	resp := h.handleSysinfo()
	if resp.OK {
		t.Error("expected error")
	}
}

func TestParseLsOutput(t *testing.T) {
	output := "d\t4096\t755\t1709300000\t/tmp/subdir\nregular file\t100\t644\t1709300000\t/tmp/test.txt\n"
	entries := parseLsOutput(output)

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if !entries[0].IsDir {
		t.Error("first entry should be a directory")
	}
	if entries[1].IsDir {
		t.Error("second entry should not be a directory")
	}
	if entries[1].Size != 100 {
		t.Errorf("second entry size = %d, want 100", entries[1].Size)
	}
}

func TestParseLsOutputSkipsDotEntries(t *testing.T) {
	output := "d\t4096\t755\t1709300000\t.\nd\t4096\t755\t1709300000\t..\nd\t4096\t755\t1709300000\t/tmp/real\n"
	entries := parseLsOutput(output)
	if len(entries) != 1 {
		t.Errorf("expected 1 entry (skipping . and ..), got %d", len(entries))
	}
}

func TestParseLsOutputEmpty(t *testing.T) {
	entries := parseLsOutput("")
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestParseLsOutputMalformed(t *testing.T) {
	output := "not enough fields\n"
	entries := parseLsOutput(output)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for malformed input, got %d", len(entries))
	}
}

func TestParseInt64(t *testing.T) {
	tests := []struct {
		input string
		want  int64
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
		if got != tt.want {
			t.Errorf("parseInt64(%q) = %d, want %d", tt.input, got, tt.want)
		}
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
		runner:     mock,
		remotePath: "/tmp/.remote-agent-test",
		sockPath:   filepath.Join(t.TempDir(), "test.sock"),
		pidPath:    filepath.Join(t.TempDir(), "test.pid"),
	}
	h := &Handler{daemon: d}

	resp := h.handleDisconnect()
	if !resp.OK {
		t.Errorf("disconnect should be OK: %s", resp.Error)
	}

	// Wait for the goroutine to complete before restoring exitFunc
	<-done
}

func TestHandleReadBadBase64(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("base64 '/test'", []byte("not-valid-base64!!!"), 0)

	resp := h.handleRead(map[string]any{"path": "/test"})
	if resp.OK {
		t.Error("expected error for bad base64")
	}
}

func TestHandleWriteDefaultMode(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{exitCode: 0}

	// Write without specifying mode (should default to 0644)
	resp := h.handleWrite(map[string]any{"path": "/tmp/test.txt", "content": "hello", "mode": ""})
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}
}

func TestHandleWriteSSHError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{err: fmt.Errorf("ssh connection lost")}

	resp := h.handleWrite(map[string]any{"path": "/tmp/test.txt", "content": "hello"})
	if resp.OK {
		t.Error("expected error")
	}
}

func TestHandleUploadExitNonZero(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stderr: []byte("disk full"), exitCode: 1}

	dir := t.TempDir()
	f := filepath.Join(dir, "test.txt")
	os.WriteFile(f, []byte("data"), 0644)

	resp := h.handleUpload(map[string]any{"local_path": f, "remote_path": "/tmp/x"})
	if resp.OK {
		t.Error("expected error")
	}
}

func TestHandleDownloadSSHError(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandFail("cat '/tmp/missing'", fmt.Errorf("ssh error"))

	resp := h.handleDownload(map[string]any{"remote_path": "/tmp/missing", "local_path": "/tmp/out"})
	if resp.OK {
		t.Error("expected error")
	}
}

func TestHandleDownloadWriteLocalFail(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("cat '/tmp/remote'", []byte("data"), 0)

	// Try to write to a directory that doesn't exist
	resp := h.handleDownload(map[string]any{
		"remote_path": "/tmp/remote",
		"local_path":  "/nonexistent/dir/file.txt",
	})
	if resp.OK {
		t.Error("expected error writing to nonexistent directory")
	}
}

func TestHandleEditSSHError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{err: fmt.Errorf("ssh error")}

	resp := h.handleEdit(map[string]any{"path": "/tmp/f", "old": "a", "new": "b"})
	if resp.OK {
		t.Error("expected error")
	}
}

func TestHandleEditNonZeroExit(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stderr: []byte("failed"), exitCode: 1}

	resp := h.handleEdit(map[string]any{"path": "/tmp/f", "old": "a", "new": "b"})
	if resp.OK {
		t.Error("expected error for non-zero exit")
	}
}

func TestHandleEditBadJSON(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stdout: []byte("not json"), exitCode: 0}

	resp := h.handleEdit(map[string]any{"path": "/tmp/f", "old": "a", "new": "b"})
	if resp.OK {
		t.Error("expected error for bad JSON")
	}
}

func TestHandleLsError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{err: fmt.Errorf("ssh error")}

	resp := h.handleLs(map[string]any{"path": "/tmp"})
	if resp.OK {
		t.Error("expected error")
	}
}

func TestHandlePsError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{err: fmt.Errorf("ssh error")}

	resp := h.handlePs(map[string]any{})
	if resp.OK {
		t.Error("expected error")
	}
}

func TestHandlePsNonZeroExit(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stderr: []byte("fail"), exitCode: 1}

	resp := h.handlePs(map[string]any{})
	if resp.OK {
		t.Error("expected error for non-zero exit")
	}
}

func TestHandlePsBadJSON(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stdout: []byte("not json"), exitCode: 0}

	resp := h.handlePs(map[string]any{})
	if resp.OK {
		t.Error("expected error for bad JSON")
	}
}

func TestHandleSysinfoSSHError(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{err: fmt.Errorf("ssh error")}

	resp := h.handleSysinfo()
	if resp.OK {
		t.Error("expected error")
	}
}

func TestHandleSysinfoBadJSON(t *testing.T) {
	h, mock := newTestHandler()
	mock.defaultResponse = mockResponse{stdout: []byte("not json"), exitCode: 0}

	resp := h.handleSysinfo()
	if resp.OK {
		t.Error("expected error for bad JSON")
	}
}

func TestHandleExecAudit(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("whoami", []byte("root\n"), 0)

	resp := h.handleExec(map[string]any{"command": "whoami"})
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}

	// Verify audit command was called before actual command
	if len(mock.calls) < 2 {
		t.Errorf("expected at least 2 calls (audit + exec), got %d", len(mock.calls))
	}
}

func TestParseLsOutputFractionalTime(t *testing.T) {
	output := "d\t4096\t755\t1709300000.123456\t/tmp/dir\n"
	entries := parseLsOutput(output)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ModTime != 1709300000 {
		t.Errorf("modtime = %d, want 1709300000", entries[0].ModTime)
	}
}

func TestParseLsOutputSkipsDotInPath(t *testing.T) {
	output := "d\t4096\t755\t1709300000\t/path/to/.\nd\t4096\t755\t1709300000\t/path/to/..\nf\t100\t644\t1709300000\t/path/to/file\n"
	entries := parseLsOutput(output)
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}
