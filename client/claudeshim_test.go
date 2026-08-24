package client

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// Real wrapper strings captured byte-for-byte from Claude Code v2.1.185
// invoking the shim (argv logging shim, BusyBox-ash remote repro rig). These
// are the exact shapes that made every Bash command return "(No output)" on a
// BusyBox remote: the leading `source` of a local-only snapshot file is a
// fatal, silenced abort in ash.
const (
	capturedWrapperPwd = "source /root/cc185-config/shell-snapshots/snapshot-bash-1784154477479-hs63nj.sh 2>/dev/null || true && " +
		"{ shopt -u extglob || setopt NO_EXTENDED_GLOB NO_BARE_GLOB_QUAL; } >/dev/null 2>&1 || true && " +
		"eval pwd < /dev/null && pwd -P >| /tmp/claude-c3ce-cwd"
	capturedWrapperLs = "source /root/cc185-config/shell-snapshots/snapshot-bash-1784154477479-hs63nj.sh 2>/dev/null || true && " +
		"{ shopt -u extglob || setopt NO_EXTENDED_GLOB NO_BARE_GLOB_QUAL; } >/dev/null 2>&1 || true && " +
		"eval 'ls -la' < /dev/null && pwd -P >| /tmp/claude-643c-cwd"
	// A real SessionStart hook line from the same rig: bare, with no wrapper.
	capturedHookCommand = `"$CLAUDE_PROJECT_DIR"/hooks/export-session-env.sh`
)

func TestIsBashToolWrapperClassification(t *testing.T) {
	cases := []struct {
		name    string
		command string
		wrapper bool
	}{
		{"captured pwd wrapper", capturedWrapperPwd, true},
		{"captured ls wrapper", capturedWrapperLs, true},
		{"wrapper without source clause", bashToolMarker + " && eval pwd < /dev/null && pwd -P >| /tmp/claude-1-cwd", true},
		{"captured hook command", capturedHookCommand, false},
		{"mcp stdio server command", "node /usr/local/lib/mcp-server/index.js --stdio", false},
		{"mcp python server", "python3 -m my_mcp_server --port 0", false},
		{"plain command", "echo hello", false},
		{"empty", "", false},
		// shopt alone (the non-prefixed local form) is not the marker.
		{"non-prefix glob form", "shopt -u extglob 2>/dev/null || true && eval pwd", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wrapper, IsBashToolWrapper(tc.command))
		})
	}
}

func TestLaunderBashToolWrapperGolden(t *testing.T) {
	glob := bashToolMarker
	cases := []struct {
		name          string
		in            string
		wantLaundered string
		wantCwdFile   string
	}{
		{
			// The BusyBox-fatal shape, captured verbatim (bare unquoted paths).
			name:          "captured pwd wrapper",
			in:            capturedWrapperPwd,
			wantLaundered: glob + " && eval pwd < /dev/null",
			wantCwdFile:   "/tmp/claude-c3ce-cwd",
		},
		{
			name:          "captured ls wrapper",
			in:            capturedWrapperLs,
			wantLaundered: glob + " && eval 'ls -la' < /dev/null",
			wantCwdFile:   "/tmp/claude-643c-cwd",
		},
		{
			// Claude quotes a path outside the safe charset, a space for example.
			name: "quoted snapshot and cwd paths",
			in: "source '/Users/Some User/.claude/shell-snapshots/snapshot-zsh-1784-ab.sh' 2>/dev/null || true && " +
				glob + " && eval 'make test' < /dev/null && pwd -P >| '/tmp/dir with space/claude-1a2b-cwd'",
			wantLaundered: glob + " && eval 'make test' < /dev/null",
			wantCwdFile:   "/tmp/dir with space/claude-1a2b-cwd",
		},
		{
			// Single quotes inside a quoted path use the '"'"' encoding.
			name: "quote inside cwd path",
			in: "source /root/.claude/shell-snapshots/snapshot-bash-1-x.sh 2>/dev/null || true && " +
				glob + ` && eval pwd < /dev/null && pwd -P >| '/tmp/o'"'"'brien/claude-11-cwd'`,
			wantLaundered: glob + " && eval pwd < /dev/null",
			wantCwdFile:   "/tmp/o'brien/claude-11-cwd",
		},
		{
			// A failed local snapshot: Claude spawns a login shell and emits no source clause.
			name:          "no source clause",
			in:            glob + " && eval 'uname -a' < /dev/null && pwd -P >| /tmp/claude-9f-cwd",
			wantLaundered: glob + " && eval 'uname -a' < /dev/null",
			wantCwdFile:   "/tmp/claude-9f-cwd",
		},
		{
			// The preamble is inlined text that can span lines. It goes over verbatim.
			name: "session-env preamble survives",
			in: "source /root/.claude/shell-snapshots/snapshot-bash-2-y.sh 2>/dev/null || true && " +
				"export FOO=bar\nexport BAZ='q q'\n: && " +
				glob + " && eval 'env' < /dev/null && pwd -P >| /tmp/claude-77-cwd",
			wantLaundered: "export FOO=bar\nexport BAZ='q q'\n: && " + glob + " && eval 'env' < /dev/null",
			wantCwdFile:   "/tmp/claude-77-cwd",
		},
		{
			// Only the genuine final tail is stripped, never a lookalike in the eval body.
			name: "lookalike tail inside eval body",
			in: glob + " && eval 'echo hi && pwd -P >| /tmp/user-file' < /dev/null && " +
				"pwd -P >| /tmp/claude-ab-cwd",
			wantLaundered: glob + " && eval 'echo hi && pwd -P >| /tmp/user-file' < /dev/null",
			wantCwdFile:   "/tmp/claude-ab-cwd",
		},
		{
			// Malformed input: a lookalike ends the eval body with no genuine tail after it.
			name:          "lookalike tail not at end of string",
			in:            glob + " && eval 'x && pwd -P >| /tmp/foo' < /dev/null",
			wantLaundered: glob + " && eval 'x && pwd -P >| /tmp/foo' < /dev/null",
			wantCwdFile:   "",
		},
		{
			// A source of anything but a Claude snapshot stays.
			name:          "non-snapshot source clause kept",
			in:            "source /etc/profile 2>/dev/null || true && " + glob + " && eval pwd < /dev/null && pwd -P >| /tmp/claude-cc-cwd",
			wantLaundered: "source /etc/profile 2>/dev/null || true && " + glob + " && eval pwd < /dev/null",
			wantCwdFile:   "/tmp/claude-cc-cwd",
		},
		{
			// No exact silencing suffix, so the clause does not match and stays.
			name:          "source clause without expected suffix kept",
			in:            "source /root/.claude/shell-snapshots/snapshot-bash-3-z.sh || true && " + glob + " && eval pwd < /dev/null && pwd -P >| /tmp/claude-dd-cwd",
			wantLaundered: "source /root/.claude/shell-snapshots/snapshot-bash-3-z.sh || true && " + glob + " && eval pwd < /dev/null",
			wantCwdFile:   "/tmp/claude-dd-cwd",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			laundered, cwdFile := launderBashToolWrapper(tc.in)
			assert.Equal(t, tc.wantLaundered, laundered)
			assert.Equal(t, tc.wantCwdFile, cwdFile)
		})
	}
}

func TestCutShellToken(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		value string
		rest  string
		ok    bool
	}{
		{"bare token", "abc def", "abc", " def", true},
		{"bare token to end", "/tmp/claude-1-cwd", "/tmp/claude-1-cwd", "", true},
		{"quoted token", "'a b' rest", "a b", " rest", true},
		{"quoted with embedded quote", `'a'"'"'b' x`, "a'b", " x", true},
		{"empty quoted", "''x", "", "x", true},
		{"unterminated quote", "'abc", "", "", false},
		{"leading space", " abc", "", "", false},
		{"empty input", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, rest, ok := cutShellToken(tc.in)
			assert.Equal(t, tc.ok, ok)
			if tc.ok {
				assert.Equal(t, tc.value, value)
				assert.Equal(t, tc.rest, rest)
			}
		})
	}
}

// startRecordingExecDaemon stands up a mock daemon that records every exec
// command it receives and answers with a canned exit code.
func startRecordingExecDaemon(t *testing.T, sockPath string, exitCode int) (commands func() []string, cleanup func()) {
	t.Helper()
	l, err := net.Listen("unix", sockPath)
	require.NoError(t, err)

	var mu sync.Mutex
	var recorded []string

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				var req protocol.DaemonRequest
				if json.NewDecoder(c).Decode(&req) != nil {
					return
				}
				command, _ := req.Params["command"].(string)
				mu.Lock()
				recorded = append(recorded, command)
				mu.Unlock()
				json.NewEncoder(c).Encode(protocol.DaemonResponse{
					OK:   true,
					Data: protocol.ExecResult{ExitCode: exitCode},
				})
			}(conn)
		}
	}()

	commands = func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), recorded...)
	}
	return commands, func() { l.Close() }
}

func TestForwardBashToolWrapperSendsLaunderedCommand(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "d.sock")
	commands, cleanup := startRecordingExecDaemon(t, sock, 0)
	defer cleanup()
	old := SocketOverride
	SocketOverride = sock
	defer func() { SocketOverride = old }()

	cwdFile := filepath.Join(dir, "claude-1234-cwd")
	wrapper := "source /root/.claude/shell-snapshots/snapshot-bash-1-a.sh 2>/dev/null || true && " +
		bashToolMarker + " && eval pwd < /dev/null && pwd -P >| " + cwdFile

	code, err := forwardBashToolWrapper(wrapper)
	require.NoError(t, err)
	assert.Equal(t, 0, code)

	got := commands()
	require.Len(t, got, 1)
	assert.Equal(t, bashToolMarker+" && eval pwd < /dev/null", got[0],
		"the daemon must receive exactly the laundered wrapper")

	// Success writes the local cwd file, the way a local `pwd -P >|` would have.
	data, err := os.ReadFile(cwdFile)
	require.NoError(t, err)
	wd, err := os.Getwd()
	require.NoError(t, err)
	if resolved, rerr := filepath.EvalSymlinks(wd); rerr == nil {
		wd = resolved
	}
	assert.Equal(t, wd+"\n", string(data))
}

func TestForwardBashToolWrapperMirrorsExitAndSkipsCwdFile(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "d.sock")
	_, cleanup := startRecordingExecDaemon(t, sock, 7)
	defer cleanup()
	old := SocketOverride
	SocketOverride = sock
	defer func() { SocketOverride = old }()

	cwdFile := filepath.Join(dir, "claude-fail-cwd")
	wrapper := bashToolMarker + " && eval 'exit 7' < /dev/null && pwd -P >| " + cwdFile

	code, err := forwardBashToolWrapper(wrapper)
	require.NoError(t, err)
	assert.Equal(t, 7, code)

	_, statErr := os.Stat(cwdFile)
	assert.True(t, os.IsNotExist(statErr), "cwd file must not be written when the remote command failed")
}

func TestForwardBashToolWrapperTransportError(t *testing.T) {
	old := SocketOverride
	SocketOverride = filepath.Join(t.TempDir(), "nonexistent.sock")
	defer func() { SocketOverride = old }()

	_, err := forwardBashToolWrapper(bashToolMarker + " && eval pwd < /dev/null")
	assert.Error(t, err)
}

func TestRunClaudeShimDispatch(t *testing.T) {
	// Wrapper (marker present) goes to the daemon...
	dir := t.TempDir()
	sock := filepath.Join(dir, "d.sock")
	commands, cleanup := startRecordingExecDaemon(t, sock, 0)
	defer cleanup()
	oldSock := SocketOverride
	SocketOverride = sock
	defer func() { SocketOverride = oldSock }()

	var localRan []string
	oldLocal := runLocalFunc
	runLocalFunc = func(command string) (int, error) {
		localRan = append(localRan, command)
		return 42, nil
	}
	defer func() { runLocalFunc = oldLocal }()

	code, err := RunClaudeShim(capturedWrapperPwd)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Len(t, commands(), 1, "wrapper must be forwarded to the daemon")
	assert.Empty(t, localRan, "wrapper must not run locally")

	// ...while hook and MCP command lines run locally, untouched.
	code, err = RunClaudeShim(capturedHookCommand)
	require.NoError(t, err)
	assert.Equal(t, 42, code)
	require.Len(t, localRan, 1)
	assert.Equal(t, capturedHookCommand, localRan[0], "hook command must be passed through verbatim")
	assert.Len(t, commands(), 1, "hook command must not reach the daemon")

	code, err = RunClaudeShim("node /usr/local/lib/mcp-server/index.js --stdio")
	require.NoError(t, err)
	assert.Equal(t, 42, code)
	assert.Len(t, localRan, 2)
	assert.Len(t, commands(), 1, "MCP command must not reach the daemon")
}

func TestRunLocalPortableStdioAndExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	oldIn, oldOut, oldErr := localStdin, localStdout, localStderr
	localStdin = strings.NewReader("ping\n")
	localStdout = &stdout
	localStderr = &stderr
	defer func() { localStdin, localStdout, localStderr = oldIn, oldOut, oldErr }()

	// stdin passes through, each stream lands on its own fd, and the exit code is exact.
	code, err := runLocalPortable("cat; echo out; echo err >&2; exit 7")
	require.NoError(t, err)
	assert.Equal(t, 7, code)
	assert.Equal(t, "ping\nout\n", stdout.String())
	assert.Equal(t, "err\n", stderr.String())
}

func TestRunLocalPortableSuccess(t *testing.T) {
	var stdout bytes.Buffer
	oldOut := localStdout
	localStdout = &stdout
	defer func() { localStdout = oldOut }()

	code, err := runLocalPortable("echo ok")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "ok\n", stdout.String())
}

func TestWriteCwdFileWarnsOnFailure(t *testing.T) {
	// Writing into a nonexistent directory must warn, not panic or abort.
	writeCwdFile(filepath.Join(t.TempDir(), "no-such-dir", "cwd"))
}

func TestPrefixWorkingDirectory(t *testing.T) {
	mount := t.TempDir()
	sub := filepath.Join(mount, "src")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	t.Run("no mount means no prefix", func(t *testing.T) {
		t.Setenv("REMOTE_AGENT_MOUNT", "")
		assert.Equal(t, "make build", prefixWorkingDirectory("make build"))
	})

	t.Run("inside the mount, the command runs there", func(t *testing.T) {
		t.Setenv("REMOTE_AGENT_MOUNT", mount)
		t.Chdir(sub)
		got := prefixWorkingDirectory("make build")
		// && and not ;: a failed cd must abort, not run the command elsewhere.
		assert.Equal(t, "cd '"+sub+"' && make build", got)
	})

	t.Run("at the mount root", func(t *testing.T) {
		t.Setenv("REMOTE_AGENT_MOUNT", mount)
		t.Chdir(mount)
		assert.Equal(t, "cd '"+mount+"' && ls", prefixWorkingDirectory("ls"))
	})

	t.Run("outside the mount is left alone", func(t *testing.T) {
		// Outside the mount there is no valid remote path, so nothing is prefixed.
		t.Setenv("REMOTE_AGENT_MOUNT", mount)
		t.Chdir(t.TempDir())
		assert.Equal(t, "ls", prefixWorkingDirectory("ls"))
	})

	t.Run("a sibling directory with the same prefix is not inside", func(t *testing.T) {
		sibling := mount + "-other"
		require.NoError(t, os.MkdirAll(sibling, 0o755))
		defer os.RemoveAll(sibling)
		t.Setenv("REMOTE_AGENT_MOUNT", mount)
		t.Chdir(sibling)
		assert.Equal(t, "ls", prefixWorkingDirectory("ls"))
	})
}

func TestShellQuote(t *testing.T) {
	assert.Equal(t, "'/srv/app'", shellQuote("/srv/app"))
	assert.Equal(t, `'/srv/it'"'"'s'`, shellQuote("/srv/it's"))
}
