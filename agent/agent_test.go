package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/remote-agent/protocol"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestRunServeNoArgs(t *testing.T) {
	err := RunServe(nil)
	assert.NotNil(t, err)

}

func TestRunServeUnknownAction(t *testing.T) {
	err := RunServe([]string{"unknown_action"})
	assert.NotNil(t, err)

}

func TestRunServeEditMissingFlags(t *testing.T) {
	err := RunServe([]string{"edit"})
	assert.NotNil(t, err)

}

func TestRunServeAuditMissingAction(t *testing.T) {
	err := RunServe([]string{"audit"})
	assert.NotNil(t, err)

}

// captureStdout redirects stdout to a temp file and returns the captured output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
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
		RunServe([]string{"sysinfo"})
	})

	var info protocol.SystemInfo
	require.NoError(t, json.Unmarshal([]byte(output), &info))
	assert.NotEqual(t, "", info.Hostname)
	assert.Greater(t, info.CPU.Threads, 0)

}

func TestServePs(t *testing.T) {
	output := captureStdout(t, func() {
		RunServe([]string{"ps"})
	})

	var list protocol.ProcessList
	require.NoError(t, json.Unmarshal([]byte(output), &list))
	assert.NotEqual(t, 0, len(list.Processes))

}

func TestServePsWithFilter(t *testing.T) {
	output := captureStdout(t, func() {
		RunServe([]string{"ps", "--filter", "__nonexistent__"})
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
		RunServe([]string{"edit", "--path", path, "--old", "hello", "--new", "goodbye"})
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
		RunServe([]string{"edit", "--path", path, "--old", "missing", "--new", "x"})
	})

	var result map[string]string
	require.NoError(t, json.Unmarshal([]byte(output), &result))

	_, ok := result["error"]
	assert.True(t, ok)

}

func TestServeAuditStartup(t *testing.T) {
	output := captureStdout(t, func() {
		RunServe([]string{"audit", "--action", "startup", "--user", "test", "--client-ip", "1.2.3.4", "--fingerprint", "SHA256:abc"})
	})

	var result map[string]string
	require.NoError(t, json.Unmarshal([]byte(output), &result))
	assert.Equal(t, "logged", result["status"])

}

func TestServeAuditShutdown(t *testing.T) {
	output := captureStdout(t, func() {
		RunServe([]string{"audit", "--action", "shutdown"})
	})

	var result map[string]string
	require.NoError(t, json.Unmarshal([]byte(output), &result))
	assert.Equal(t, "logged", result["status"])

}

func TestServeAuditGeneric(t *testing.T) {
	output := captureStdout(t, func() {
		RunServe([]string{"audit", "--action", "exec", "--detail", "ls -la"})
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
