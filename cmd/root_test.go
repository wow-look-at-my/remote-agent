package cmd

import (
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

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

func TestExecuteDownloadNoArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"download"})
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

func TestExecuteConnectNoTarget(t *testing.T) {
	rootCmd.SetArgs([]string{"connect"})
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
