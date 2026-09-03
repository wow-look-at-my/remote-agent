package cmd

import (
	"flag"
	"os"
	"testing"
)

// These tests drive process-wide state: the CLI's flag globals in package
// client, and the daemon socket a command discovers. The runner may mark tests
// parallel, and two parallel tests then read each other's values. Pinning
// test.parallel means a paused test resumes on its own, which costs nothing
// here and is what these tests were written against.
func TestMain(m *testing.M) {
	if err := flag.Set("test.parallel", "1"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
