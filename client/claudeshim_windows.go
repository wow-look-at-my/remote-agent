//go:build windows

package client

// localShellPath returns the shell used for local hook/MCP execution. Windows
// has no /bin/sh; resolve `sh` from PATH (the claude-shim flow itself is only
// reachable through the POSIX shell shim, so this exists for compilation and
// exotic setups such as MSYS).
func localShellPath() string {
	return "sh"
}

// runLocal executes command locally as a child process. Windows has no
// exec(2), so the portable spawn-and-wait implementation is the whole story.
func runLocal(command string) (int, error) {
	return runLocalPortable(command)
}
