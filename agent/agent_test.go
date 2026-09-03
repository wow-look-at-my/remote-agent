package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// stdoutMu serializes the capture below. os.Stdout is one value for the
// process, so two tests capturing at once write into each other's file and one
// of them reads back nothing.
var stdoutMu sync.Mutex

// captureStdout redirects stdout to a temp file and returns the captured output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	stdoutMu.Lock()
	defer stdoutMu.Unlock()

	old := os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	require.Nil(t, err)

	os.Stdout = f
	fn()
	os.Stdout = old
	f.Seek(0, 0)
	data, _ := os.ReadFile(f.Name())
	return string(data)
}

func TestServeSysinfo(t *testing.T) {
	output := captureStdout(t, func() {
		ServeSysinfo()
	})

	var info protocol.SystemInfo
	require.NoError(t, json.Unmarshal([]byte(output), &info))
	assert.NotEqual(t, "", info.Hostname)
	assert.Greater(t, info.CPU.Threads, 0)
}

func TestServePs(t *testing.T) {
	output := captureStdout(t, func() {
		ServePs("")
	})

	var list protocol.ProcessList
	require.NoError(t, json.Unmarshal([]byte(output), &list))
	assert.NotEqual(t, 0, len(list.Processes))
}

func TestServePsWithFilter(t *testing.T) {
	output := captureStdout(t, func() {
		ServePs("__nonexistent__")
	})

	var list protocol.ProcessList
	require.NoError(t, json.Unmarshal([]byte(output), &list))
	assert.Equal(t, 0, len(list.Processes))
}

func TestServeEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	output := captureStdout(t, func() {
		ServeEdit(path, "hello", "goodbye", false)
	})

	var result protocol.EditResult
	require.NoError(t, json.Unmarshal([]byte(output), &result))
	assert.True(t, result.Modified)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "goodbye world", string(data))
}

func TestServeEditNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello"), 0644)

	output := captureStdout(t, func() {
		ServeEdit(path, "missing", "x", false)
	})

	var result map[string]string
	require.NoError(t, json.Unmarshal([]byte(output), &result))

	_, ok := result["error"]
	assert.True(t, ok)
}

func TestServeAuditStartup(t *testing.T) {
	output := captureStdout(t, func() {
		ServeAudit("startup", "", "test", "1.2.3.4", "SHA256:abc")
	})

	var result map[string]string
	require.NoError(t, json.Unmarshal([]byte(output), &result))
	assert.Equal(t, "logged", result["status"])
}

func TestServeAuditShutdown(t *testing.T) {
	output := captureStdout(t, func() {
		ServeAudit("shutdown", "", "", "", "")
	})

	var result map[string]string
	require.NoError(t, json.Unmarshal([]byte(output), &result))
	assert.Equal(t, "logged", result["status"])
}

func TestServeAuditGeneric(t *testing.T) {
	output := captureStdout(t, func() {
		ServeAudit("exec", "ls -la", "", "", "")
	})

	var result map[string]string
	json.Unmarshal([]byte(output), &result)
	assert.Equal(t, "logged", result["status"])
}

func TestWriteJSON(t *testing.T) {
	output := captureStdout(t, func() {
		writeJSON(map[string]int{"a": 1})
	})
	var result map[string]int
	require.NoError(t, json.Unmarshal([]byte(output), &result))
	assert.Equal(t, 1, result["a"])
}

func TestWriteJSONError(t *testing.T) {
	output := captureStdout(t, func() {
		writeJSONError(fmt.Errorf("test error"))
	})
	var result map[string]string
	json.Unmarshal([]byte(output), &result)
	assert.Equal(t, "test error", result["error"])
}
