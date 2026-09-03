package client

// shellExecScript runs the program in $0 with the remaining arguments,
const shellExecScript = `exec "$0" "$@"`

// SelfCommand returns the program and arguments that run this binary again
// with args -- for a child process started here, or for the command
func SelfCommand(self string, args ...string) (name string, argv []string) {
	return selfCommand(self, args)
}
