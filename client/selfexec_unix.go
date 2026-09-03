//go:build unix

package client

// selfCommand routes the invocation through /bin/sh, the one loader present on
// every Unix host that starts an APE as well as an ordinary ELF binary.
func selfCommand(self string, args []string) (string, []string) {
	argv := make([]string, 0, len(args)+3)
	argv = append(argv, "-c", shellExecScript, self)
	return "/bin/sh", append(argv, args...)
}
