package main

import (
	"os"
	"testing"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestPrintUsage(t *testing.T) {
	old := os.Stderr
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	require.Nil(t, err)

	os.Stderr = f
	defer func() { os.Stderr = old }()

	printUsage()

	f.Seek(0, 0)
	data, _ := os.ReadFile(f.Name())
	assert.NotEqual(t, 0, len(data))

}

func TestRunNoArgs(t *testing.T) {
	old := os.Stderr
	f, _ := os.CreateTemp(t.TempDir(), "stderr")
	os.Stderr = f
	defer func() { os.Stderr = old }()

	err := run(nil)
	assert.NotNil(t, err)

}

func TestRunUnknownCommand(t *testing.T) {
	old := os.Stderr
	f, _ := os.CreateTemp(t.TempDir(), "stderr")
	os.Stderr = f
	defer func() { os.Stderr = old }()

	err := run([]string{"nonexistent"})
	assert.NotNil(t, err)

}

func TestRunExecNoSocket(t *testing.T) {
	// Set TMPDIR to empty dir so no daemon socket is found
	t.Setenv("TMPDIR", t.TempDir())

	err := run([]string{"exec", "ls"})
	assert.NotNil(t, err)

}

func TestRunDisconnectNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	err := run([]string{"disconnect"})
	assert.NotNil(t, err)

}

func TestRunReadNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	err := run([]string{"read", "/some/file"})
	assert.NotNil(t, err)

}

func TestRunPingNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	err := run([]string{"ping"})
	assert.NotNil(t, err)

}

func TestRunSysinfoNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	err := run([]string{"sysinfo"})
	assert.NotNil(t, err)

}

func TestRunServeNoAction(t *testing.T) {
	err := run([]string{"serve"})
	assert.NotNil(t, err)

}

func TestRunUploadNoArgs(t *testing.T) {
	err := run([]string{"upload"})
	assert.NotNil(t, err)

}

func TestRunDownloadNoArgs(t *testing.T) {
	err := run([]string{"download"})
	assert.NotNil(t, err)

}

func TestRunLsNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	err := run([]string{"ls"})
	assert.NotNil(t, err)

}

func TestRunPsNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	err := run([]string{"ps"})
	assert.NotNil(t, err)

}

func TestRunEditNoArgs(t *testing.T) {
	err := run([]string{"edit"})
	assert.NotNil(t, err)

}

func TestRunConnectNoTarget(t *testing.T) {
	err := run([]string{"connect"})
	assert.NotNil(t, err)

}

func TestRunWriteNoArgs(t *testing.T) {
	err := run([]string{"write"})
	assert.NotNil(t, err)

}
