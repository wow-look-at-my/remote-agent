package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// suppressStdout redirects stdout for the duration of fn to avoid polluting test output.
func suppressStdout(t *testing.T, fn func()) {
	t.Helper()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old; f.Close() }()
	fn()
}

func TestExecute(t *testing.T) {
	err := Execute()
	assert.Nil(t, err) // Cobra shows help without error
}

func TestExecuteNoArgs(t *testing.T) {
	rootCmd.SetArgs([]string{})
	err := rootCmd.Execute()
	assert.Nil(t, err) // Cobra shows help without error
}

func TestExecuteUnknownCommand(t *testing.T) {
	rootCmd.SetArgs([]string{"nonexistent"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteExecNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	rootCmd.SetArgs([]string{"exec", "ls"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteClaudeTooManyArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"claude", "user@host", "extra"})
	err := rootCmd.Execute()
	assert.NotNil(t, err) // second positional is rejected; claude flags go after --
}

func TestExecuteClaudeNoTargetNoDaemon(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	rootCmd.SetArgs([]string{"claude"})
	err := rootCmd.Execute()
	assert.NotNil(t, err) // no target given and no daemon running
}

func TestExecuteDisconnectNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	rootCmd.SetArgs([]string{"disconnect"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteReadNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	rootCmd.SetArgs([]string{"read", "/some/file"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecutePingNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	rootCmd.SetArgs([]string{"ping"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteSysinfoNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	rootCmd.SetArgs([]string{"sysinfo"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteUploadNoArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"upload"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteUploadNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	rootCmd.SetArgs([]string{"upload", "/tmp/local", "/tmp/remote"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteDownloadNoArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"download"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteDownloadNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	rootCmd.SetArgs([]string{"download", "/tmp/remote", "/tmp/local"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteLsNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	rootCmd.SetArgs([]string{"ls"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecutePsNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	rootCmd.SetArgs([]string{"ps"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteEditMissingOld(t *testing.T) {
	rootCmd.SetArgs([]string{"edit", "/some/file"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteEditNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	rootCmd.SetArgs([]string{"edit", "/some/file", "--old", "foo"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteConnectNoTarget(t *testing.T) {
	rootCmd.SetArgs([]string{"connect"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteConnectBadHost(t *testing.T) {
	rootCmd.SetArgs([]string{"connect", "user@invalid-host-that-does-not-exist", "--port", "9999"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteWriteNoArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"write"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteServeNoSubcommand(t *testing.T) {
	rootCmd.SetArgs([]string{"serve"})
	err := rootCmd.Execute()
	// Cobra shows help for serve without error
	assert.Nil(t, err)
}

func TestExecuteExecNoArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"exec"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteLsWithPathNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	rootCmd.SetArgs([]string{"ls", "/some/path"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteWriteNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	// stdin is /dev/null in tests, so ReadAll returns empty bytes immediately
	rootCmd.SetArgs([]string{"write", "/some/file"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteServeEditMissingFlags(t *testing.T) {
	rootCmd.SetArgs([]string{"serve", "edit"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteServeEditSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	suppressStdout(t, func() {
		rootCmd.SetArgs([]string{"serve", "edit", "--path", path, "--old", "hello", "--new", "bye"})
		err := rootCmd.Execute()
		assert.Nil(t, err)
	})
}

func TestExecuteServeAuditMissingAction(t *testing.T) {
	rootCmd.SetArgs([]string{"serve", "audit"})
	err := rootCmd.Execute()
	assert.NotNil(t, err)
}

func TestExecuteServeAuditSuccess(t *testing.T) {
	suppressStdout(t, func() {
		rootCmd.SetArgs([]string{"serve", "audit", "--action", "test"})
		err := rootCmd.Execute()
		assert.Nil(t, err)
	})
}

func TestExecuteServeSysinfo(t *testing.T) {
	suppressStdout(t, func() {
		rootCmd.SetArgs([]string{"serve", "sysinfo"})
		err := rootCmd.Execute()
		assert.Nil(t, err)
	})
}

func TestExecuteServePs(t *testing.T) {
	suppressStdout(t, func() {
		rootCmd.SetArgs([]string{"serve", "ps"})
		err := rootCmd.Execute()
		assert.Nil(t, err)
	})
}
