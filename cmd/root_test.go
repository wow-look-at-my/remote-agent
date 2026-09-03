package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExecute(t *testing.T) {
	assert.Nil(t, runRoot(t)) // Cobra shows help without error
}

func TestExecuteUnknownCommand(t *testing.T) {
	assert.NotNil(t, runRoot(t, "nonexistent"))
}

func TestExecuteExecNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	assert.NotNil(t, runRoot(t, "exec", "ls"))
}

func TestExecuteDisconnectNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	assert.NotNil(t, runRoot(t, "disconnect"))
}

func TestExecuteReadNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	assert.NotNil(t, runRoot(t, "read", "/some/file"))
}

func TestExecutePingNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	assert.NotNil(t, runRoot(t, "ping"))
}

func TestExecuteSysinfoNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	assert.NotNil(t, runRoot(t, "sysinfo"))
}

func TestExecuteUploadNoArgs(t *testing.T) {
	assert.NotNil(t, runRoot(t, "upload"))
}

func TestExecuteUploadNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	assert.NotNil(t, runRoot(t, "upload", "/tmp/local", "/tmp/remote"))
}

func TestExecuteDownloadNoArgs(t *testing.T) {
	assert.NotNil(t, runRoot(t, "download"))
}

func TestExecuteDownloadNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	assert.NotNil(t, runRoot(t, "download", "/tmp/remote", "/tmp/local"))
}

func TestExecuteLsNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	assert.NotNil(t, runRoot(t, "ls"))
}

func TestExecutePsNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	assert.NotNil(t, runRoot(t, "ps"))
}

func TestExecuteEditMissingOld(t *testing.T) {
	assert.NotNil(t, runRoot(t, "edit", "/some/file"))
}

func TestExecuteEditNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	assert.NotNil(t, runRoot(t, "edit", "/some/file", "--old", "foo"))
}

func TestExecuteConnectNoTarget(t *testing.T) {
	assert.NotNil(t, runRoot(t, "connect"))
}

func TestExecuteConnectBadHost(t *testing.T) {
	assert.NotNil(t, runRoot(t, "connect", "user@invalid-host-that-does-not-exist", "--port", "9999"))
}

func TestExecuteWriteNoArgs(t *testing.T) {
	assert.NotNil(t, runRoot(t, "write"))
}

func TestExecuteServeNoSubcommand(t *testing.T) {
	// Cobra shows help for serve without error
	assert.Nil(t, runRoot(t, "serve"))
}

func TestExecuteExecNoArgs(t *testing.T) {
	assert.NotNil(t, runRoot(t, "exec"))
}

func TestExecuteLsWithPathNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	assert.NotNil(t, runRoot(t, "ls", "/some/path"))
}

func TestExecuteWriteNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	// stdin is /dev/null in tests, so ReadAll returns empty bytes immediately
	assert.NotNil(t, runRoot(t, "write", "/some/file"))
}

func TestExecuteServeEditMissingFlags(t *testing.T) {
	assert.NotNil(t, runRoot(t, "serve", "edit"))
}

func TestExecuteServeEditSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	assert.Nil(t, runRoot(t, "serve", "edit", "--path", path, "--old", "hello", "--new", "bye"))
}

func TestExecuteServeAuditMissingAction(t *testing.T) {
	assert.NotNil(t, runRoot(t, "serve", "audit"))
}

func TestExecuteServeAuditSuccess(t *testing.T) {
	assert.Nil(t, runRoot(t, "serve", "audit", "--action", "test"))
}

func TestExecuteServeSysinfo(t *testing.T) {
	assert.Nil(t, runRoot(t, "serve", "sysinfo"))
}

func TestExecuteServePs(t *testing.T) {
	assert.Nil(t, runRoot(t, "serve", "ps"))
}
