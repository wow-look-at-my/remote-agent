package agent

import (
	"flag"
	"os"
	"testing"
)

// The audit tests write to one syslog connection for the process. Pinning
// test.parallel keeps a paused test from sharing it with another.
func TestMain(m *testing.M) {
	if err := flag.Set("test.parallel", "1"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
