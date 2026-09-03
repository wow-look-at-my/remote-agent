package daemon

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

func TestHandleGlob(t *testing.T) {
	h, runner := newTestHandler()
	runner.defaultResponse = mockResponse{
		stdout: []byte(`{"pattern":"**/*.go","path":"/srv","files":["/srv/main.go"],"truncated":true}`),
	}

	resp := h.handleGlob(map[string]any{"pattern": "**/*.go", "path": "/srv", "limit": float64(10)})
	require.True(t, resp.OK, resp.Error)

	result, ok := resp.Data.(protocol.GlobResult)
	require.True(t, ok)
	assert.Equal(t, []string{"/srv/main.go"}, result.Files)
	assert.True(t, result.Truncated)

	cmd := runner.lastCommand(t)
	assert.Contains(t, cmd, "serve glob")
	assert.Contains(t, cmd, "--pattern '**/*.go'")
	assert.Contains(t, cmd, "--path '/srv'")
	assert.Contains(t, cmd, "--limit 10")
}

func TestHandleGlobDefaultsPath(t *testing.T) {
	h, runner := newTestHandler()
	runner.defaultResponse = mockResponse{stdout: []byte(`{"files":[]}`)}

	require.True(t, h.handleGlob(map[string]any{"pattern": "*.go"}).OK)
	assert.Contains(t, runner.lastCommand(t), "--path '.'")
}

func TestHandleGlobRequiresPattern(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleGlob(map[string]any{"path": "/srv"})
	assert.False(t, resp.OK)
	assert.Contains(t, resp.Error, "pattern")
}

func TestHandleGrep(t *testing.T) {
	h, runner := newTestHandler()
	runner.defaultResponse = mockResponse{
		stdout: []byte(`{"pattern":"todo","mode":"content","matches":[{"path":"/srv/a.go","line":3,"text":"// todo"}],"files_scanned":2}`),
	}

	resp := h.handleGrep(map[string]any{
		"pattern":     "todo",
		"path":        "/srv",
		"include":     "**/*.go",
		"mode":        protocol.GrepModeContent,
		"ignore_case": true,
		"context":     float64(2),
		"limit":       float64(50),
	})
	require.True(t, resp.OK, resp.Error)

	result, ok := resp.Data.(protocol.GrepResult)
	require.True(t, ok)
	require.Len(t, result.Matches, 1)
	assert.Equal(t, 3, result.Matches[0].Line)
	assert.Equal(t, 2, result.FilesScanned)

	cmd := runner.lastCommand(t)
	assert.Contains(t, cmd, "serve grep")
	assert.Contains(t, cmd, "--pattern 'todo'")
	assert.Contains(t, cmd, "--include '**/*.go'")
	assert.Contains(t, cmd, "--mode 'content'")
	assert.Contains(t, cmd, "--ignore-case")
	assert.Contains(t, cmd, "--context 2")
	assert.Contains(t, cmd, "--limit 50")
}

func TestHandleGrepOmitsUnsetOptions(t *testing.T) {
	h, runner := newTestHandler()
	runner.defaultResponse = mockResponse{stdout: []byte(`{"mode":"content"}`)}

	require.True(t, h.handleGrep(map[string]any{"pattern": "x"}).OK)

	cmd := runner.lastCommand(t)
	assert.NotContains(t, cmd, "--include")
	assert.NotContains(t, cmd, "--ignore-case")
	assert.NotContains(t, cmd, "--context")
	assert.NotContains(t, cmd, "--limit")
}

func TestHandleGrepRequiresPattern(t *testing.T) {
	h, _ := newTestHandler()
	resp := h.handleGrep(map[string]any{"path": "/srv"})
	assert.False(t, resp.OK)
	assert.Contains(t, resp.Error, "pattern")
}

func TestSearchSurfacesRemoteErrors(t *testing.T) {
	// The helper reports operational failures as a JSON error body...
	h, runner := newTestHandler()
	runner.defaultResponse = mockResponse{stdout: []byte(`{"error":"glob /nope: no such file or directory"}`)}
	resp := h.handleGlob(map[string]any{"pattern": "*", "path": "/nope"})
	assert.False(t, resp.OK)
	assert.Contains(t, resp.Error, "no such file")

	h, runner = newTestHandler()
	runner.defaultResponse = mockResponse{stderr: []byte("helper missing"), exitCode: 127}
	resp = h.handleGrep(map[string]any{"pattern": "x"})
	assert.False(t, resp.OK)
	assert.Contains(t, resp.Error, "helper missing")

	// ...and unparseable output is reported rather than silently empty.
	h, runner = newTestHandler()
	runner.defaultResponse = mockResponse{stdout: []byte("not json")}
	resp = h.handleGlob(map[string]any{"pattern": "*"})
	assert.False(t, resp.OK)
	assert.Contains(t, resp.Error, "parse glob result")
}

func TestIntParam(t *testing.T) {
	assert.Equal(t, 7, intParam(map[string]any{"n": float64(7)}, "n"))
	assert.Equal(t, 7, intParam(map[string]any{"n": 7}, "n"))
	assert.Equal(t, 0, intParam(map[string]any{"n": "7"}, "n"))
	assert.Equal(t, 0, intParam(map[string]any{}, "n"))
}

// lastCommand returns the most recent command the mock runner saw.
func (m *mockRunner) lastCommand(t *testing.T) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	require.NotEmpty(t, m.calls)
	return strings.TrimSpace(m.calls[len(m.calls)-1].Command)
}
