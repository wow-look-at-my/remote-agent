package sshutil

import (
	"flag"
	"os"
	"testing"
)

// The ssh_config tests replace the package's ssh-command seam, which is one
// variable per process. Pinning test.parallel keeps a paused test from reading
// the seam another test installed.
func TestMain(m *testing.M) {
	if err := flag.Set("test.parallel", "1"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
