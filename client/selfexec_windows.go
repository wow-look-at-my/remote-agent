//go:build windows

package client

// selfCommand names the binary directly. An APE carries a real PE header, so
// Windows loads it with no shell in front, and Windows has no /bin/sh to put
// there anyway.
func selfCommand(self string, args []string) (string, []string) {
	return self, args
}
