package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/protocol"
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
	// The 6-field find format: type, size, mode, time, link target, path.
	output := "f\t100\t644\t1709300000\t\t/tmp/a.txt\nd\t4096\t755\t1709300000\t\t/tmp/subdir\n"
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
	// find format has 6 fields: type\tsize\tmode\ttime\tlinktarget\tpath
	mock.defaultResponse = mockResponse{stdout: []byte("d\t4096\t755\t1709300000\t\t/tmp/dir\n"), exitCode: 0}

	resp := h.handleLs(map[string]any{"path": "/tmp", "recursive": true})
	assert.True(t, resp.OK)

}

func TestHandleLsMissingDirErrors(t *testing.T) {
	h, mock := newTestHandler()
	// A nonexistent path makes find fail with no output; this used to be
	// silently reported as an empty directory listing.
	mock.defaultResponse = mockResponse{
		stderr:   []byte("find: '/nope': No such file or directory\n"),
		exitCode: 1,
	}

	resp := h.handleLs(map[string]any{"path": "/nope"})
	assert.False(t, resp.OK)
	assert.Contains(t, resp.Error, "No such file or directory")
}

func TestHandleLsPartialListingStillSucceeds(t *testing.T) {
	h, mock := newTestHandler()
	// Recursive walks can fail on some subtrees (permission denied) while
	// still finding entries; the partial listing must be returned.
	mock.defaultResponse = mockResponse{
		stdout:   []byte("f\t100\t644\t1709300000\t\t/tmp/a.txt\n"),
		stderr:   []byte("find: '/tmp/locked': Permission denied\n"),
		exitCode: 1,
	}

	resp := h.handleLs(map[string]any{"path": "/tmp", "recursive": true})
	assert.True(t, resp.OK)
	listing, ok := resp.Data.(protocol.DirListing)
	require.True(t, ok)
	assert.Equal(t, 1, len(listing.Entries))
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
		Hostname: "test", OS: "Ubuntu", Arch: "amd64",
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
		assert.Equal(t, tt.want, got)

	}
}

func TestHandleDisconnect(t *testing.T) {
	// The daemon carries its own exit, so the test never reaches os.Exit.
	done := make(chan struct{})

	mock := newMockRunner()
	d := &Daemon{
		runner:     mock,
		remotePath: "/tmp/.remote-agent-test",
		sockPath:   filepath.Join(t.TempDir(), "test.sock"),
		pidPath:    filepath.Join(t.TempDir(), "test.pid"),
		exit:       func(int) { close(done) },
	}
	h := &Handler{daemon: d}

	resp := h.handleDisconnect()
	assert.True(t, resp.OK)

	<-done
}

func TestHandleReadEmptyFile(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("cat '/tmp/empty'", []byte{}, 0)

	resp := h.handleRead(map[string]any{"path": "/tmp/empty"})
	assert.True(t, resp.OK)

	info, ok := resp.Data.(protocol.FileInfo)
	assert.True(t, ok)
	assert.Equal(t, int64(0), info.Size)
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
		"remote_path": "/tmp/remote",
		"local_path":  "/nonexistent/dir/file.txt",
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
	h.daemon.auditWG.Wait() // audits run async; drain before asserting
	calls := mock.snapshotCalls()
	assert.GreaterOrEqual(t, len(calls), 2) // audit + actual command both recorded

	var auditSeen bool
	for _, c := range calls {
		if strings.Contains(c.Command, "serve audit --action 'exec'") &&
			strings.Contains(c.Command, "whoami") {
			auditSeen = true
		}
	}
	assert.True(t, auditSeen, "expected an exec audit entry, got %v", calls)
}

// Blocks every non-audit command until release closes, to prove two run concurrently.
type barrierRunner struct {
	arrived chan string
	release chan struct{}
}

func (r *barrierRunner) Run(command string) (stdout, stderr []byte, exitCode int, err error) {
	if strings.Contains(command, "serve audit") {
		return nil, nil, 0, nil
	}
	r.arrived <- command
	<-r.release
	return []byte("done\n"), nil, 0, nil
}

func (r *barrierRunner) RunStdin(command string, stdin []byte) (stdout, stderr []byte, exitCode int, err error) {
	return r.Run(command)
}

func (r *barrierRunner) RunTimeout(command string, _ time.Duration) (stdout, stderr []byte, exitCode int, err error) {
	return r.Run(command)
}

// TestConcurrentExecsDoNotSerialize proves two execs progress simultaneously:
// both must be inside Run before either is released. Under the old global
// mutex the second exec could not start until the first finished, and this
// test would fail at the two-arrivals barrier.
func TestConcurrentExecsDoNotSerialize(t *testing.T) {
	runner := &barrierRunner{
		arrived: make(chan string, 2),
		release: make(chan struct{}),
	}
	d := &Daemon{runner: runner, remotePath: "/tmp/.remote-agent-test"}
	h := &Handler{daemon: d}

	results := make(chan *protocol.DaemonResponse, 2)
	go func() { results <- h.handleExec(map[string]any{"command": "first"}) }()
	go func() { results <- h.handleExec(map[string]any{"command": "second"}) }()

	for i := range 2 {
		select {
		case <-runner.arrived:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of 2 execs started; operations are serialized", i)
		}
	}
	close(runner.release)

	for range 2 {
		resp := <-results
		assert.True(t, resp.OK)
	}
	d.auditWG.Wait()
}

// --- New tests for exec ls rewriting, 2>&1 stripping, readlink, symlinks ---

func TestParseLsCommand(t *testing.T) {
	tests := []struct {
		cmd       string
		wantPath  string
		wantRecur bool
		wantOK    bool
	}{
		{"ls", ".", false, true},
		{"ls /tmp", "/tmp", false, true},
		{"ls -R /tmp", "/tmp", true, true},
		{"ls -R", ".", true, true},
		{"ls -la", "", false, false},      // unsupported flags
		{"ls -la /tmp", "", false, false}, // unsupported flags
		{"ls --color /tmp", "", false, false},
		{"cat /etc/passwd", "", false, false}, // not ls
		{"", "", false, false},
		{"lsof", "", false, false}, // not ls
		// A glob falls through to exec: the handler quotes it and would match nothing.
		{"ls *.go", "", false, false},
		{"ls /tmp/*.log", "", false, false},
		{"ls ?", "", false, false},
		{"ls [ab]", "", false, false},
		{"ls ~", "", false, false},
		{"ls $HOME", "", false, false},
		{"ls 'a b'", "", false, false},
		{`ls "a b"`, "", false, false},
		{`ls a\ b`, "", false, false},
		{"ls a b", "", false, false}, // multiple paths: all but the last were dropped
		{"ls /tmp; whoami", "", false, false},
		{"ls `pwd`", "", false, false},
		{"ls /tmp | wc -l", "", false, false},
	}
	for _, tt := range tests {
		path, recursive, ok := parseLsCommand(tt.cmd)
		assert.Equal(t, tt.wantOK, ok, "cmd=%q", tt.cmd)
		if ok {
			assert.Equal(t, tt.wantPath, path, "cmd=%q", tt.cmd)
			assert.Equal(t, tt.wantRecur, recursive, "cmd=%q", tt.cmd)
		}
	}
}

func TestStripTrailingRedirect(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"echo hello 2>&1", "echo hello"},
		{"echo hello", "echo hello"},
		{"echo hello  2>&1", "echo hello"},
		{"echo 2>&1 hello", "echo 2>&1 hello"}, // not trailing
		{"2>&1", ""},
		{"cmd", "cmd"},
		// Another '>' is present, so 2>&1 fills the log file and must stay.
		{"make > build.log 2>&1", "make > build.log 2>&1"},
		{"make >> build.log 2>&1", "make >> build.log 2>&1"},
		{"a 2>&1 | b 2>&1", "a 2>&1 | b 2>&1"}, // inner 2>&1 contains '>'
		{"cmd < input 2>&1", "cmd < input"},    // '<' is unaffected by the strip
	}
	for _, tt := range tests {
		got := stripTrailingRedirect(tt.input)
		assert.Equal(t, tt.want, got, "input=%q", tt.input)
	}
}

func TestHandleExecRewritesLs(t *testing.T) {
	h, mock := newTestHandler()
	// exec "ls /tmp" reaches handleLs, which runs the stat command.
	output := "regular file\t100\t644\t1709300000\t/tmp/a.txt\n"
	mock.defaultResponse = mockResponse{stdout: []byte(output), exitCode: 0}

	resp := h.handleExec(map[string]any{"command": "ls /tmp"})
	assert.True(t, resp.OK)

	// Verify the response is a DirListing (not ExecResult)
	listing, ok := resp.Data.(protocol.DirListing)
	assert.True(t, ok, "expected DirListing, got %T", resp.Data)
	assert.Equal(t, "/tmp", listing.Path)
}

func TestHandleExecDoesNotRewriteComplexLs(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("ls -la /tmp", []byte("total 0\n"), 0)

	resp := h.handleExec(map[string]any{"command": "ls -la /tmp"})
	assert.True(t, resp.OK)

	// Verify the response is an ExecResult (not DirListing)
	result, ok := resp.Data.(protocol.ExecResult)
	assert.True(t, ok, "expected ExecResult, got %T", resp.Data)
	assert.Equal(t, "total 0\n", result.Stdout)
}

func TestHandleExecStrips2Redirect(t *testing.T) {
	h, mock := newTestHandler()
	// The command after stripping 2>&1 should be "whoami"
	mock.onCommand("whoami", []byte("root\n"), 0)

	resp := h.handleExec(map[string]any{"command": "whoami 2>&1"})
	assert.True(t, resp.OK)

	result, ok := resp.Data.(protocol.ExecResult)
	assert.True(t, ok)
	assert.Equal(t, "root\n", result.Stdout)
}

func TestHandleReadlink(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommand("readlink -f '/usr/bin/python'", []byte("/usr/bin/python3.11\n"), 0)

	resp := h.handleReadlink(map[string]any{"path": "/usr/bin/python"})
	assert.True(t, resp.OK)

	result, ok := resp.Data.(protocol.ReadlinkResult)
	assert.True(t, ok)
	assert.Equal(t, "/usr/bin/python", result.Path)
	assert.Equal(t, "/usr/bin/python3.11", result.Target)
}

func TestHandleReadlinkMissingPath(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleReadlink(map[string]any{})
	assert.False(t, resp.OK)
}

func TestHandleReadlinkError(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandErr("readlink -f '/nonexistent'", []byte("No such file"), 1)

	resp := h.handleReadlink(map[string]any{"path": "/nonexistent"})
	assert.False(t, resp.OK)
}

func TestHandleReadlinkSSHError(t *testing.T) {
	h, mock := newTestHandler()
	mock.onCommandFail("readlink -f '/test'", fmt.Errorf("ssh error"))

	resp := h.handleReadlink(map[string]any{"path": "/test"})
	assert.False(t, resp.OK)
}

func TestParseFindOutput(t *testing.T) {
	output := "d\t4096\t755\t1709300000\t\t/tmp/subdir\nf\t100\t644\t1709300000\t\t/tmp/test.txt\nl\t12\t777\t1709300000\t/tmp/real\t/tmp/link\n"
	entries := parseFindOutput(output)
	require.Equal(t, 3, len(entries))

	assert.True(t, entries[0].IsDir)
	assert.False(t, entries[0].IsLink)

	assert.False(t, entries[1].IsDir)
	assert.False(t, entries[1].IsLink)
	assert.Equal(t, int64(100), entries[1].Size)

	assert.True(t, entries[2].IsLink)
	assert.Equal(t, "/tmp/real", entries[2].Target)
	assert.Equal(t, "/tmp/link", entries[2].Name)
}

func TestParseFindOutputSkipsDots(t *testing.T) {
	output := "d\t4096\t755\t1709300000\t\t.\nd\t4096\t755\t1709300000\t\t..\nf\t100\t644\t1709300000\t\t/tmp/real\n"
	entries := parseFindOutput(output)
	assert.Equal(t, 1, len(entries))
}

func TestParseFindOutputEmpty(t *testing.T) {
	entries := parseFindOutput("")
	assert.Equal(t, 0, len(entries))
}

func TestHandleDispatchReadlink(t *testing.T) {
	mock := newMockRunner()
	mock.onCommand("readlink -f '/usr/bin/python'", []byte("/usr/bin/python3\n"), 0)
	h := &Handler{daemon: &Daemon{runner: mock, remotePath: "/tmp/.remote-agent-test"}}

	resp := h.Handle(&protocol.DaemonRequest{
		Action: "readlink",
		Params: map[string]any{"path": "/usr/bin/python"},
	})
	assert.True(t, resp.OK)
}
