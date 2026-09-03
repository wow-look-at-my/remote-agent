package daemon

import (
	"flag"
	"os"
	"testing"
)

// The idle-watchdog tests replace package globals (idleTimeout, exitFunc) and
// measure elapsed time, so a second test running beside them changes both what
// they read and how long they take. Pinning test.parallel keeps a paused test
// on its own.
func TestMain(m *testing.M) {
	if err := flag.Set("test.parallel", "1"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
