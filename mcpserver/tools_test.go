package mcpserver

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

func call(t *testing.T, backend Backend, name string, args map[string]any) ([]contentBlock, error) {
	t.Helper()
	s := New(backend, "test", testTarget, "")
	tool := s.lookup(name)
	require.NotNil(t, tool, "tool %s should exist", name)
	return tool.handler(args)
}

// callText runs a tool and returns its single text block.
func callText(t *testing.T, backend Backend, name string, args map[string]any) string {
	t.Helper()
	blocks, err := call(t, backend, name, args)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	require.Equal(t, "text", blocks[0].Type)
	return blocks[0].Text
}

func TestReadFileNumbersLines(t *testing.T) {
	backend := newFakeBackend()
	backend.results["read"] = protocol.FileInfo{Content: "alpha\nbeta\ngamma\n"}

	out := callText(t, backend, "read_file", map[string]any{"path": "/srv/f.txt"})
	assert.Contains(t, out, "     1\talpha")
	assert.Contains(t, out, "     3\tgamma")
}

func TestReadFileWindow(t *testing.T) {
	backend := newFakeBackend()
	backend.results["read"] = protocol.FileInfo{Content: "a\nb\nc\nd\ne\n"}

	out := callText(t, backend, "read_file", map[string]any{
		"path": "/srv/f.txt", "offset": float64(2), "limit": float64(2),
	})
	assert.Contains(t, out, "     2\tb")
	assert.Contains(t, out, "     3\tc")
	assert.NotContains(t, out, "     4\td")
	// The window is reported so the reader knows more remains.
	assert.Contains(t, out, "offset=4")
}

func TestReadFileOffsetPastEnd(t *testing.T) {
	backend := newFakeBackend()
	backend.results["read"] = protocol.FileInfo{Content: "only one line\n"}

	out := callText(t, backend, "read_file", map[string]any{"path": "/x", "offset": float64(99)})
	assert.Contains(t, out, "past the end")
}

func TestReadFileEmpty(t *testing.T) {
	backend := newFakeBackend()
	backend.results["read"] = protocol.FileInfo{Content: ""}

	out := callText(t, backend, "read_file", map[string]any{"path": "/srv/empty"})
	assert.Contains(t, out, "empty")
}

func TestReadFileImage(t *testing.T) {
	backend := newFakeBackend()
	pixel := []byte{0x89, 'P', 'N', 'G', 0x00, 0x01}
	backend.results["read"] = protocol.FileInfo{ContentB64: base64.StdEncoding.EncodeToString(pixel)}

	blocks, err := call(t, backend, "read_file", map[string]any{"path": "/srv/shot.png"})
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	assert.Equal(t, "image", blocks[0].Type)
	assert.Equal(t, "image/png", blocks[0].MimeType)
	assert.Equal(t, base64.StdEncoding.EncodeToString(pixel), blocks[0].Data)
}

func TestReadFileImageTooLarge(t *testing.T) {
	backend := newFakeBackend()
	backend.results["read"] = protocol.FileInfo{
		ContentB64: base64.StdEncoding.EncodeToString(make([]byte, maxImageBytes+1)),
	}

	_, err := call(t, backend, "read_file", map[string]any{"path": "/srv/huge.jpg"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too large")
}

func TestReadFileBinaryRejected(t *testing.T) {
	backend := newFakeBackend()
	backend.results["read"] = protocol.FileInfo{
		ContentB64: base64.StdEncoding.EncodeToString([]byte{0xff, 0xfe, 0x00}),
	}

	_, err := call(t, backend, "read_file", map[string]any{"path": "/srv/a.bin"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binary")
}

func TestReadFileTruncatesLongLines(t *testing.T) {
	backend := newFakeBackend()
	backend.results["read"] = protocol.FileInfo{Content: strings.Repeat("x", maxReadLine+50)}

	out := callText(t, backend, "read_file", map[string]any{"path": "/srv/min.js"})
	assert.Contains(t, out, "[line truncated]")
}

func TestReadFileUndecodableContent(t *testing.T) {
	backend := newFakeBackend()
	backend.results["read"] = protocol.FileInfo{ContentB64: "!!!not base64!!!"}

	_, err := call(t, backend, "read_file", map[string]any{"path": "/srv/f"})
	assert.Error(t, err)
}

func TestWriteFile(t *testing.T) {
	backend := newFakeBackend()
	backend.results["write"] = protocol.WriteResult{BytesWritten: 12}

	out := callText(t, backend, "write_file", map[string]any{
		"path": "/srv/f.txt", "content": "hello world\n", "mode": "0600",
	})
	assert.Contains(t, out, "12 bytes")

	call := backend.lastCall(t)
	assert.Equal(t, "write", call.Action)
	assert.Equal(t, "hello world\n", call.Params["content"])
	assert.Equal(t, "0600", call.Params["mode"])
}

func TestWriteFileOmitsUnsetMode(t *testing.T) {
	backend := newFakeBackend()
	backend.results["write"] = protocol.WriteResult{BytesWritten: 1}

	callText(t, backend, "write_file", map[string]any{"path": "/srv/f", "content": "x"})
	assert.NotContains(t, backend.lastCall(t).Params, "mode")
}

func TestEditFile(t *testing.T) {
	backend := newFakeBackend()
	backend.results["edit"] = protocol.EditResult{Modified: true, Replacements: 3}

	out := callText(t, backend, "edit_file", map[string]any{
		"path": "/srv/f.go", "old_string": "a", "new_string": "b", "replace_all": true,
	})
	assert.Contains(t, out, "3 occurrence")

	call := backend.lastCall(t)
	assert.Equal(t, "edit", call.Action)
	assert.Equal(t, "a", call.Params["old"])
	assert.Equal(t, "b", call.Params["new"])
	assert.Equal(t, true, call.Params["replace_all"])
}

func TestEditFileAllowsEmptyReplacement(t *testing.T) {
	backend := newFakeBackend()
	backend.results["edit"] = protocol.EditResult{Modified: true, Replacements: 1}

	// Deleting text means new_string is "" -- present but empty, which must not
	// be mistaken for a missing argument.
	out := callText(t, backend, "edit_file", map[string]any{
		"path": "/srv/f.go", "old_string": "drop me", "new_string": "",
	})
	assert.Contains(t, out, "1 occurrence")
}

func TestEditFileRequiresArguments(t *testing.T) {
	backend := newFakeBackend()

	_, err := call(t, backend, "edit_file", map[string]any{"path": "/srv/f.go", "old_string": "a"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "new_string")

	_, err = call(t, backend, "edit_file", map[string]any{"path": "/srv/f.go", "new_string": "b"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "old_string")
}

func TestListDir(t *testing.T) {
	backend := newFakeBackend()
	backend.results["ls"] = protocol.DirListing{
		Path: "/srv",
		Entries: []protocol.DirEntry{
			{Name: "/srv/app", IsDir: true},
			{Name: "/srv/f.txt", Size: 42},
			{Name: "/srv/link", IsLink: true, Target: "/srv/f.txt"},
		},
	}

	out := callText(t, backend, "list_dir", map[string]any{"path": "/srv", "recursive": true})
	assert.Contains(t, out, "d /srv/app")
	assert.Contains(t, out, "f /srv/f.txt (42 bytes)")
	assert.Contains(t, out, "l /srv/link -> /srv/f.txt")
	assert.Equal(t, true, backend.lastCall(t).Params["recursive"])
}

func TestListDirEmpty(t *testing.T) {
	backend := newFakeBackend()
	backend.results["ls"] = protocol.DirListing{Path: "/srv/empty"}

	out := callText(t, backend, "list_dir", map[string]any{"path": "/srv/empty"})
	assert.Contains(t, out, "empty")
}

func TestGlobTool(t *testing.T) {
	backend := newFakeBackend()
	backend.results["glob"] = protocol.GlobResult{
		Pattern: "**/*.go", Path: "/srv", Files: []string{"/srv/a.go", "/srv/b.go"}, Truncated: true,
	}

	out := callText(t, backend, "glob", map[string]any{"pattern": "**/*.go", "path": "/srv"})
	assert.Contains(t, out, "/srv/a.go")
	assert.Contains(t, out, "truncated")
}

func TestGlobToolNoMatches(t *testing.T) {
	backend := newFakeBackend()
	backend.results["glob"] = protocol.GlobResult{Pattern: "*.rs", Path: "/srv"}

	out := callText(t, backend, "glob", map[string]any{"pattern": "*.rs"})
	assert.Contains(t, out, "No files matching")
}

func TestGrepToolModes(t *testing.T) {
	backend := newFakeBackend()
	backend.results["grep"] = protocol.GrepResult{
		Mode: protocol.GrepModeContent,
		Matches: []protocol.GrepMatch{
			{Path: "/srv/a.go", Line: 2, Text: "before", IsContext: true},
			{Path: "/srv/a.go", Line: 3, Text: "todo: fix"},
		},
	}
	out := callText(t, backend, "grep", map[string]any{"pattern": "todo", "context_lines": float64(1)})
	assert.Contains(t, out, "/srv/a.go-2-before")
	assert.Contains(t, out, "/srv/a.go:3:todo: fix")
	assert.Equal(t, 1, backend.lastCall(t).Params["context"])

	backend.results["grep"] = protocol.GrepResult{Mode: protocol.GrepModeFiles, Files: []string{"/srv/a.go"}}
	out = callText(t, backend, "grep", map[string]any{"pattern": "todo", "output_mode": protocol.GrepModeFiles})
	assert.Equal(t, "/srv/a.go\n", out)
	assert.Equal(t, protocol.GrepModeFiles, backend.lastCall(t).Params["mode"])

	backend.results["grep"] = protocol.GrepResult{
		Mode: protocol.GrepModeCount, Counts: []protocol.GrepFileCount{{Path: "/srv/a.go", Count: 4}},
	}
	out = callText(t, backend, "grep", map[string]any{"pattern": "todo", "output_mode": protocol.GrepModeCount})
	assert.Equal(t, "/srv/a.go:4\n", out)
}

func TestGrepToolNoMatchesAndTruncation(t *testing.T) {
	backend := newFakeBackend()
	backend.results["grep"] = protocol.GrepResult{Pattern: "nope", Mode: protocol.GrepModeContent, FilesScanned: 9}
	out := callText(t, backend, "grep", map[string]any{"pattern": "nope"})
	assert.Contains(t, out, "No matches")
	assert.Contains(t, out, "9 files")

	backend.results["grep"] = protocol.GrepResult{
		Mode:      protocol.GrepModeFiles,
		Files:     []string{"/srv/a.go"},
		Truncated: true,
	}
	out = callText(t, backend, "grep", map[string]any{"pattern": "x", "output_mode": protocol.GrepModeFiles})
	assert.Contains(t, out, "truncated")
}

func TestTransferTools(t *testing.T) {
	backend := newFakeBackend()
	backend.results["upload"] = protocol.WriteResult{BytesWritten: 7}
	out := callText(t, backend, "upload_file", map[string]any{"local_path": "/tmp/a", "remote_path": "/srv/a"})
	assert.Contains(t, out, "Uploaded 7 bytes")
	assert.Equal(t, "upload", backend.lastCall(t).Action)

	backend.results["download"] = protocol.WriteResult{BytesWritten: 9}
	out = callText(t, backend, "download_file", map[string]any{"remote_path": "/srv/a", "local_path": "/tmp/a"})
	assert.Contains(t, out, "Downloaded 9 bytes")
	assert.Equal(t, "download", backend.lastCall(t).Action)
}

func TestToolsRequireTheirArguments(t *testing.T) {
	backend := newFakeBackend()
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"read_file", map[string]any{}},
		{"write_file", map[string]any{"path": "/x"}},
		{"write_file", map[string]any{"content": "x"}},
		{"edit_file", map[string]any{"old_string": "a", "new_string": "b"}},
		{"glob", map[string]any{}},
		{"grep", map[string]any{}},
		{"upload_file", map[string]any{"local_path": "/x"}},
		{"upload_file", map[string]any{"remote_path": "/x"}},
		{"download_file", map[string]any{"remote_path": "/x"}},
		{"download_file", map[string]any{"local_path": "/x"}},
	}
	for _, c := range cases {
		_, err := call(t, backend, c.tool, c.args)
		assert.Error(t, err, "%s%v should require its arguments", c.tool, c.args)
	}
}

func TestToolsPropagateBackendErrors(t *testing.T) {
	backend := newFakeBackend()
	backend.err = fmt.Errorf("daemon is gone")

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"read_file", map[string]any{"path": "/x"}},
		{"write_file", map[string]any{"path": "/x", "content": "y"}},
		{"edit_file", map[string]any{"path": "/x", "old_string": "a", "new_string": "b"}},
		{"list_dir", map[string]any{"path": "/x"}},
		{"glob", map[string]any{"pattern": "*"}},
		{"grep", map[string]any{"pattern": "x"}},
		{"upload_file", map[string]any{"local_path": "/a", "remote_path": "/b"}},
		{"download_file", map[string]any{"remote_path": "/a", "local_path": "/b"}},
	} {
		_, err := call(t, backend, tc.tool, tc.args)
		assert.ErrorContains(t, err, "daemon is gone", "tool %s", tc.tool)
	}
}

func TestImageMIME(t *testing.T) {
	assert.Equal(t, "image/png", imageMIME("/a/B.PNG"))
	assert.Equal(t, "image/jpeg", imageMIME("/a/b.jpg"))
	assert.Equal(t, "image/jpeg", imageMIME("/a/b.jpeg"))
	assert.Equal(t, "image/gif", imageMIME("/a/b.gif"))
	assert.Equal(t, "image/webp", imageMIME("/a/b.webp"))
	assert.Equal(t, "", imageMIME("/a/b.txt"))
}

func TestArgHelpers(t *testing.T) {
	args := map[string]any{"s": "v", "b": true, "i": float64(3), "n": 4}
	assert.Equal(t, "v", stringArg(args, "s"))
	assert.Equal(t, "", stringArg(args, "missing"))
	assert.True(t, boolArg(args, "b"))
	assert.False(t, boolArg(args, "missing"))
	assert.Equal(t, 3, intArg(args, "i"))
	assert.Equal(t, 4, intArg(args, "n"))
	assert.Equal(t, 0, intArg(args, "s"))
}

func TestToolCallUsesDefaultTarget(t *testing.T) {
	backend := newFakeBackend()
	backend.results["read"] = protocol.FileInfo{Content: "x\n"}

	callText(t, backend, "read_file", map[string]any{"path": "/srv/f"})
	assert.Equal(t, testTarget, backend.lastCall(t).Route.Target)
}

func TestToolCallTargetArgumentWins(t *testing.T) {
	backend := newFakeBackend()
	backend.results["read"] = protocol.FileInfo{Content: "x\n"}

	callText(t, backend, "read_file", map[string]any{"path": "/srv/f", "target": "root@other"})
	assert.Equal(t, "root@other", backend.lastCall(t).Route.Target)
}

func TestEveryToolPassesItsTarget(t *testing.T) {
	args := map[string]map[string]any{
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
			toolArgs["target"] = "root@" + name
			_, err := call(t, backend, name, toolArgs)
			require.NoError(t, err)
			assert.Equal(t, "root@"+name, backend.lastCall(t).Route.Target)
		})
	}
}

// Without a default target a call cannot be routed anywhere, so it must fail
// saying so rather than reaching for whatever daemon happens to be running.
func TestToolCallWithoutTargetOrDefault(t *testing.T) {
	backend := newFakeBackend()
	s := New(backend, "test", "", "")

	_, err := s.lookup("read_file").handler(map[string]any{"path": "/srv/f"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target")
	assert.Empty(t, backend.calls, "an unrouted call must not reach the daemon")
}

func TestTargetIsRequiredOnlyWithoutADefault(t *testing.T) {
	for _, tool := range New(newFakeBackend(), "test", "", "").tools {
		schema := tool.InputSchema
		assert.Contains(t, schema["properties"].(map[string]any), "target", "%s needs a target argument", tool.Name)
		assert.Contains(t, schema["required"].([]string), "target", "%s must require target", tool.Name)
	}
	for _, tool := range New(newFakeBackend(), "test", testTarget, "").tools {
		schema := tool.InputSchema
		desc := schema["properties"].(map[string]any)["target"].(map[string]any)["description"].(string)
		assert.Contains(t, desc, testTarget, "%s should document the default target", tool.Name)
		required, _ := schema["required"].([]string)
		assert.NotContains(t, required, "target", "%s must not require target when a default exists", tool.Name)
	}
}
