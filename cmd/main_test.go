package cmd

import (
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

// lockCLI gives the test the process to itself. A CLI test drives state the
// process has one of: the shared rootCmd, os.Stdout, the osExit seam, and the
// flag globals in package client. Tests are parallel by default here, and
// t.Serial is what the runner offers for exactly this; a lock of our own would
// deadlock against it, because t.Setenv declares a test serial too and then
// waits for every parallel test -- including one blocked on that lock.
func lockCLI(t *testing.T) {
	t.Helper()
	t.Serial()
}

// resetFlags puts every flag in a command tree back at its default. A cobra
// command is one object for the process, so a flag one test sets stays set for
// the next one: that is how a test asserting "this flag is missing" passed
// only while it ran before the test that supplies the flag.
func resetFlags(c *cobra.Command) {
	c.Flags().VisitAll(func(f *pflag.Flag) {
		f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, sub := range c.Commands() {
		resetFlags(sub)
	}
}

// runRoot runs the CLI with args and returns what Execute returns. It takes the
// CLI lock, clears the flags the previous test left, and sends stdout to a file
// so the command's own output stays out of the test log.
func runRoot(t *testing.T, args ...string) error {
	t.Helper()
	lockCLI(t)
	suppressStdout(t)
	resetFlags(rootCmd)
	rootCmd.SetArgs(args)
	return Execute()
}

// suppressStdout points os.Stdout at a temp file for the rest of the test. The
// caller holds the CLI lock, because os.Stdout is one value for the process.
func suppressStdout(t *testing.T) {
	t.Helper()
	old := os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	os.Stdout = f
	t.Cleanup(func() { os.Stdout = old; f.Close() })
}
