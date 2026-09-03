package cmd

import (
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

// lockCLI gives the test the process to itself.
func lockCLI(t *testing.T) {
	t.Helper()
	t.Serial()
}

// resetFlags puts every flag in a command tree back at its default.
func resetFlags(c *cobra.Command) {
	c.Flags().VisitAll(func(f *pflag.Flag) {
		f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, sub := range c.Commands() {
		resetFlags(sub)
	}
}

// runRoot runs the CLI with args and returns what Execute returns.
func runRoot(t *testing.T, args ...string) error {
	t.Helper()
	lockCLI(t)
	suppressStdout(t)
	resetFlags(rootCmd)
	rootCmd.SetArgs(args)
	return Execute()
}

// suppressStdout points os.Stdout at a temp file for the rest of the test.
// The caller holds the CLI lock, because os.Stdout belongs to the whole
// process.
func suppressStdout(t *testing.T) {
	t.Helper()
	old := os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	os.Stdout = f
	t.Cleanup(func() { os.Stdout = old; f.Close() })
}
