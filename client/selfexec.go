package client

// shellExecScript runs the program in $0 with the remaining arguments,
// replacing the shell.
const shellExecScript = `exec "$0" "$@"`

// SelfCommand returns the program and arguments that run this binary again
// with args -- for a child process started here, or for the command another
// program spawns from a config file.
//
// The form matters because a release is a Cosmopolitan APE. A shell starts one
// on any host: execve reports ENOEXEC on the APE header, and the shell then
// reads the file as the script that header is. A raw execve has no such
// fallback, so os/exec and an MCP client's own spawn both fail with "exec
// format error" wherever no APE binfmt entry is registered. see docs/ape.md
func SelfCommand(self string, args ...string) (name string, argv []string) {
	return selfCommand(self, args)
}
