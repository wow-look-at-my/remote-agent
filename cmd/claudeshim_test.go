package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bashToolGlobMarker mirrors the machine-generated glob-setup clause Claude
// Code v2.1.185 embeds in every Bash tool wrapper when CLAUDE_CODE_SHELL_PREFIX
// is set (the claude-shim classification marker).
const bashToolGlobMarker = "{ shopt -u extglob || setopt NO_EXTENDED_GLOB NO_BARE_GLOB_QUAL; } >/dev/null 2>&1 || true"

// NOTE: cmd-level claude-shim tests must only use Bash-tool-wrapper command
// strings (containing bashToolGlobMarker). A non-wrapper string would take the
// local-execution path, which replaces the test process via exec(2).

func TestClaudeShimForwardsWrapperAndWritesCwdFile(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "shim.sock")
	cleanup := startLocalExecDaemon(t, sock)
	defer cleanup()
	t.Setenv("REMOTE_AGENT_SOCKET", sock)

	var exited bool
	old := osExit
	osExit = func(c int) { exited = true }
	defer func() { osExit = old }()

	cwdFile := filepath.Join(dir, "claude-1a2b-cwd")
	// The exact v2.1.185 wrapper shape: local snapshot source, glob marker,
	// eval, cwd tail. The mock daemon executes what it receives via `sh -c`;
	// the un-laundered wrapper would fail on the missing snapshot under a
	// non-bash shell, and would clobber cwdFile from the "remote" side.
	wrapper := "source /nonexistent/shell-snapshots/snapshot-bash-1-a.sh 2>/dev/null || true && " +
		bashToolGlobMarker + " && eval 'true' < /dev/null && pwd -P >| " + cwdFile

	suppressStdout(t, func() {
		require.NoError(t, claudeShimCmd.RunE(claudeShimCmd, []string{wrapper}))
	})

	assert.False(t, exited, "successful command must not call osExit")
	data, err := os.ReadFile(cwdFile)
	require.NoError(t, err, "cwd file must be written locally after a successful command")
	assert.NotEmpty(t, data)
}

func TestClaudeShimMirrorsRemoteExitCode(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "shim.sock")
	cleanup := startLocalExecDaemon(t, sock)
	defer cleanup()
	t.Setenv("REMOTE_AGENT_SOCKET", sock)

	var gotCode int
	var called bool
	old := osExit
	osExit = func(c int) { called, gotCode = true, c }
	defer func() { osExit = old }()

	cwdFile := filepath.Join(dir, "claude-dead-cwd")
	wrapper := bashToolGlobMarker + " && eval 'exit 7' < /dev/null && pwd -P >| " + cwdFile

	suppressStdout(t, func() {
		require.NoError(t, claudeShimCmd.RunE(claudeShimCmd, []string{wrapper}))
	})

	assert.True(t, called, "non-zero remote exit must be mirrored via osExit")
	assert.Equal(t, 7, gotCode)
	_, statErr := os.Stat(cwdFile)
	assert.True(t, os.IsNotExist(statErr), "cwd file must not be written when the command failed")
}

func TestClaudeShimNoArgs(t *testing.T) {
	err := claudeShimCmd.RunE(claudeShimCmd, nil)
	assert.Error(t, err)
}
