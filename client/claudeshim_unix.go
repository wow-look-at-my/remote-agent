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

// runLocal executes command locally via `/bin/sh -c`, replacing this process
// with exec(2). That reproduces exactly what Claude Code did before v2.1.185
// wrapped hooks with the shell prefix (a bare `/bin/sh -c '<cmd>'` spawn):
// same process depth, same inherited env and stdio, same signal delivery --
// which matters for long-lived MCP stdio servers that Claude manages by pid.
// On success it never returns; if exec(2) itself fails it falls back to
// running the command as a child process.
func runLocal(command string) (int, error) {
	shell := localShellPath()
	// exec(2) only ever returns on failure.
	if err := syscall.Exec(shell, []string{shell, "-c", command}, os.Environ()); err != nil {
		return runLocalPortable(command)
	}
	return 0, nil // unreachable: a successful exec does not return
}
