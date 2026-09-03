//go:build unix

package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelfCommandRunsThroughAShell(t *testing.T) {
	name, argv := SelfCommand("/opt/remote-agent", "connect", "root@host")
	assert.Equal(t, "/bin/sh", name)
	require.FileExists(t, name)
	assert.Equal(t, []string{"-c", shellExecScript, "/opt/remote-agent", "connect", "root@host"}, argv)
}

// The argv has to survive the shell as separate words: a binary path or an
// argument with a space in it must not split, and neither must an empty one.
func TestSelfCommandArgumentsArriveIntact(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "print args")
	require.NoError(t, os.WriteFile(self, []byte("#!/bin/sh\nfor a in \"$@\"; do echo \"[$a]\"; done\n"), 0755))

	name, argv := SelfCommand(self, "two words", "", "a'b")
	out, err := exec.Command(name, argv...).Output()
	require.NoError(t, err)
	assert.Equal(t, "[two words]\n[]\n[a'b]\n", string(out))
}

// The point of the shell: it runs a file the kernel refuses to execve, which is
// how a Cosmopolitan APE starts on a host with no APE binfmt entry. os/exec
// alone reports "exec format error" on the same file. see docs/ape.md
func TestSelfCommandStartsAFileExecveRejects(t *testing.T) {
	dir := t.TempDir()
	// No #! line and no ELF header, so execve(2) answers ENOEXEC -- the same
	// answer it gives for an APE header. A shell reads it as a script instead.
	self := filepath.Join(dir, "noexec-header")
	require.NoError(t, os.WriteFile(self, []byte("echo started \"$1\"\n"), 0755))

	_, directErr := exec.Command(self, "ok").Output()
	require.Error(t, directErr, "execve must reject this file, or the test proves nothing")

	name, argv := SelfCommand(self, "ok")
	out, err := exec.Command(name, argv...).Output()
	require.NoError(t, err)
	assert.Equal(t, "started ok\n", string(out))
}
