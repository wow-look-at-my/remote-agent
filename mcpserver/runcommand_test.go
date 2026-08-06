package mcpserver

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

func TestRunCommand(t *testing.T) {
	backend := newFakeBackend()
	backend.results["exec"] = protocol.ExecResult{Stdout: "hello\n"}

	out := callText(t, backend, "run_command", map[string]any{"command": "echo hello"})
	assert.Equal(t, "hello", out)

	call := backend.lastCall(t)
	assert.Equal(t, "exec", call.Action)
	assert.Equal(t, "echo hello", call.Params["command"])
	assert.Equal(t, testTarget, call.Target)
}

func TestRunCommandInDirectory(t *testing.T) {
	backend := newFakeBackend()
	backend.results["exec"] = protocol.ExecResult{Stdout: "ok\n"}

	callText(t, backend, "run_command", map[string]any{"command": "make build", "cwd": "/srv/it's mine"})
	// The directory is quoted as one word, so a space or a quote in it cannot
	// split the command or escape into it.
	assert.Equal(t, `cd '/srv/it'\''s mine' && make build`, backend.lastCall(t).Params["command"])
}

// A non-zero exit must reach the caller as a failure, carrying the output that
// explains it -- reporting it as a successful result reads as "it worked".
func TestRunCommandNonZeroExitIsAnError(t *testing.T) {
	backend := newFakeBackend()
	backend.results["exec"] = protocol.ExecResult{Stderr: "no such file\n", ExitCode: 2}

	_, err := call(t, backend, "run_command", map[string]any{"command": "cat /nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exited 2")
	assert.Contains(t, err.Error(), "no such file")
}

func TestRunCommandSilentFailureStillReports(t *testing.T) {
	backend := newFakeBackend()
	backend.results["exec"] = protocol.ExecResult{ExitCode: 1}

	_, err := call(t, backend, "run_command", map[string]any{"command": "false"})
	assert.ErrorContains(t, err, "exited 1 with no output")
}

func TestRunCommandKeepsStderrSeparate(t *testing.T) {
	backend := newFakeBackend()
	backend.results["exec"] = protocol.ExecResult{Stdout: "out\n", Stderr: "warn\n"}

	out := callText(t, backend, "run_command", map[string]any{"command": "build"})
	assert.Equal(t, "out\n[stderr]\nwarn", out)
}

func TestRunCommandNoOutput(t *testing.T) {
	backend := newFakeBackend()
	backend.results["exec"] = protocol.ExecResult{}

	assert.Contains(t, callText(t, backend, "run_command", map[string]any{"command": "true"}), "no output")
}

// The daemon answers a plain `ls <path>` with a directory listing instead of a
// command result. Decoding only the command shape would render that as an
// empty success.
func TestRunCommandHandlesListingReply(t *testing.T) {
	backend := newFakeBackend()
	backend.results["exec"] = protocol.DirListing{
		Path:    "/srv",
		Entries: []protocol.DirEntry{{Name: "/srv/app", IsDir: true}, {Name: "/srv/f.txt", Size: 12}},
	}

	out := callText(t, backend, "run_command", map[string]any{"command": "ls /srv"})
	assert.Contains(t, out, "d /srv/app")
	assert.Contains(t, out, "f /srv/f.txt (12 bytes)")
}

func TestRunCommandTruncatesHugeOutput(t *testing.T) {
	backend := newFakeBackend()
	backend.results["exec"] = protocol.ExecResult{Stdout: strings.Repeat("x", maxOutputBytes+5000)}

	out := callText(t, backend, "run_command", map[string]any{"command": "cat big.log"})
	assert.Less(t, len(out), maxOutputBytes+500)
	assert.Contains(t, out, "5000 bytes of stdout dropped")
}

func TestRunCommandRequiresCommand(t *testing.T) {
	_, err := call(t, newFakeBackend(), "run_command", map[string]any{})
	assert.ErrorContains(t, err, "command")
}
