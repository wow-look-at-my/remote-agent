//go:build unix

package client

import (
	"os"
	"syscall"
)

// localShellPath returns the shell used for local hook/MCP execution.
func localShellPath() string {
	return "/bin/sh"
}

// runLocal replaces this process with `/bin/sh -c` through exec(2), which keeps the
// process depth, environment, stdio and signals Claude expects for an MCP server it
// manages by pid. A failed exec falls back to a child process.
func runLocal(command string) (int, error) {
	shell := localShellPath()
	// exec(2) only ever returns on failure.
	if err := syscall.Exec(shell, []string{shell, "-c", command}, os.Environ()); err != nil {
		return runLocalPortable(command)
	}
	return 0, nil // unreachable: a successful exec does not return
}
