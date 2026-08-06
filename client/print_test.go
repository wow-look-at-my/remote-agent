package client

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// capturePrinted runs fn with os.Stdout redirected to a temp file and returns
// what it wrote. os.Stderr is redirected too, so truncation notices (which go
// to stderr so they never pollute a piped listing) do not reach the test log.
func capturePrinted(t *testing.T, fn func()) string {
	t.Helper()
	dir := t.TempDir()
	out, err := os.CreateTemp(dir, "stdout")
	require.NoError(t, err)
	errFile, err := os.CreateTemp(dir, "stderr")
	require.NoError(t, err)

	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = out, errFile
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()

	fn()
	out.Close()
	errFile.Close()

	data, err := os.ReadFile(out.Name())
	require.NoError(t, err)
	return string(data)
}

// asMap round-trips a typed payload through JSON, matching what the printers
// actually receive from the daemon.
func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

func TestPrintGlobText(t *testing.T) {
	payload := asMap(t, protocol.GlobResult{
		Files:     []string{"/srv/a.go", "/srv/b.go"},
		Truncated: true,
	})

	out := capturePrinted(t, func() { require.NoError(t, printGlobText(payload)) })
	assert.Equal(t, "/srv/a.go\n/srv/b.go\n", out)
}

func TestPrintGrepTextContent(t *testing.T) {
	payload := asMap(t, protocol.GrepResult{
		Mode: protocol.GrepModeContent,
		Matches: []protocol.GrepMatch{
			{Path: "/srv/a.go", Line: 2, Text: "context line", IsContext: true},
			{Path: "/srv/a.go", Line: 3, Text: "match line"},
		},
	})

	out := capturePrinted(t, func() { require.NoError(t, printGrepText(payload)) })
	assert.Equal(t, "/srv/a.go-2-context line\n/srv/a.go:3:match line\n", out)
}

func TestPrintGrepTextFilesAndCounts(t *testing.T) {
	files := asMap(t, protocol.GrepResult{Mode: protocol.GrepModeFiles, Files: []string{"/srv/a.go"}})
	out := capturePrinted(t, func() { require.NoError(t, printGrepText(files)) })
	assert.Equal(t, "/srv/a.go\n", out)

	counts := asMap(t, protocol.GrepResult{
		Mode:   protocol.GrepModeCount,
		Counts: []protocol.GrepFileCount{{Path: "/srv/a.go", Count: 3}},
	})
	out = capturePrinted(t, func() { require.NoError(t, printGrepText(counts)) })
	assert.Equal(t, "/srv/a.go:3\n", out)
}

func TestPrintGrepTextEmpty(t *testing.T) {
	out := capturePrinted(t, func() {
		require.NoError(t, printGrepText(map[string]any{"mode": protocol.GrepModeContent}))
	})
	assert.Empty(t, out)
}

func TestGlobAndGrepClients(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()

	withSuppressedStdout(t, func() {
		assert.NoError(t, Glob("**/*.go", "/srv", 10))
		assert.NoError(t, Grep("todo", "/srv", "**/*.go", protocol.GrepModeContent, true, 2, 50))
	})
}

func TestGlobAndGrepWithoutDaemon(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	assert.Error(t, Glob("*", ".", 0))
	assert.Error(t, Grep("x", ".", "", "", false, 0, 0))
}

func TestCallDecodesTypedResults(t *testing.T) {
	cleanup := startMockDaemon(t)
	defer cleanup()

	// The mock daemon echoes an OK response with no payload for unknown
	// actions; Call must still succeed, with and without an out parameter.
	var listing protocol.DirListing
	assert.NoError(t, Call("ls", map[string]any{"path": "/srv"}, &listing))
	assert.NoError(t, Call("ls", map[string]any{"path": "/srv"}, nil))
}

func TestCallReportsDaemonErrors(t *testing.T) {
	cleanup := startErrorDaemon(t)
	defer cleanup()

	err := Call("ls", map[string]any{"path": "/srv"}, nil)
	assert.Error(t, err)
}

func TestCallWithoutDaemon(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	assert.Error(t, Call("ls", nil, nil))
}

func TestDaemonBackendDelegatesToCall(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("REMOTE_AGENT_NO_AUTOSTART", "1")
	// No daemon running for the named target: the adapter must surface the
	// failure rather than answering from some other daemon.
	assert.Error(t, DaemonBackend{}.Call("root@host", "read", map[string]any{"path": "/x"}, nil))
}
