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
	assert.Equal(t, testTarget, call.Route.Target)
}

func TestRunCommandInDirectory(t *testing.T) {
	backend := newFakeBackend()
	backend.results["exec"] = protocol.ExecResult{Stdout: "ok\n"}

	callText(t, backend, "run_command", map[string]any{"command": "make build", "cwd": "/srv/it's mine"})
	// One quoted word, so a space or a quote in it cannot split or escape the command.
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

// A call that names a control socket must reach the daemon layer with it: that
// is the whole path by which "here is a control file" becomes a connection.
func TestControlPathTravelsWithTheCall(t *testing.T) {
	backend := newFakeBackend()
	backend.results["exec"] = protocol.ExecResult{Stdout: "ok\n"}

	callText(t, backend, "run_command", map[string]any{
		"command":      "uptime",
		"target":       "root@locked-down",
		"control_path": "/tmp/cm-root@locked-down:22",
	})
	assert.Equal(t, protocol.Route{
		Target:      "root@locked-down",
		ControlPath: "/tmp/cm-root@locked-down:22",
	}, backend.lastCall(t).Route)
}

func TestControlPathAppliesToEveryTool(t *testing.T) {
	args := map[string]map[string]any{
		"run_command":   {"command": "true"},
		"read_file":     {"path": "/f"},
		"write_file":    {"path": "/f", "content": "x"},
		"edit_file":     {"path": "/f", "old_string": "a", "new_string": "b"},
		"list_dir":      {"path": "/d"},
		"glob":          {"pattern": "*.go"},
		"grep":          {"pattern": "x"},
		"upload_file":   {"local_path": "/l", "remote_path": "/r"},
		"download_file": {"remote_path": "/r", "local_path": "/l"},
	}
	for name, toolArgs := range args {
		t.Run(name, func(t *testing.T) {
			backend := newFakeBackend()
			toolArgs["control_path"] = "/tmp/cm.sock"
			_, err := call(t, backend, name, toolArgs)
			require.NoError(t, err)
			assert.Equal(t, "/tmp/cm.sock", backend.lastCall(t).Route.ControlPath)
		})
	}
}

// The server-level default covers a client that was started with a control
// socket; a call naming its own still wins.
func TestControlPathDefaultAndOverride(t *testing.T) {
	backend := newFakeBackend()
	backend.results["exec"] = protocol.ExecResult{}
	s := New(backend, "test", testTarget, "/tmp/default.sock")

	_, err := s.lookup("run_command").handler(map[string]any{"command": "true"})
	require.NoError(t, err)
	assert.Equal(t, "/tmp/default.sock", backend.lastCall(t).Route.ControlPath)

	_, err = s.lookup("run_command").handler(map[string]any{"command": "true", "control_path": "/tmp/other.sock"})
	require.NoError(t, err)
	assert.Equal(t, "/tmp/other.sock", backend.lastCall(t).Route.ControlPath)
}

// The argument has to be advertised, or a model has no way to know it exists.
func TestControlPathIsDeclaredOnEveryTool(t *testing.T) {
	for _, tool := range New(newFakeBackend(), "test", testTarget, "").tools {
		props := tool.InputSchema["properties"].(map[string]any)
		require.Contains(t, props, "control_path", "%s must advertise control_path", tool.Name)
		desc := props["control_path"].(map[string]any)["description"].(string)
		assert.Contains(t, desc, "ControlPath", "%s should name what it takes", tool.Name)
		// Optional: most hosts need no master, and ssh_config may name one.
		required, _ := tool.InputSchema["required"].([]string)
		assert.NotContains(t, required, "control_path")
	}
}
