//go:build windows

package client

// selfCommand names the binary directly.
func selfCommand(self string, args []string) (string, []string) {
	return self, args
}
