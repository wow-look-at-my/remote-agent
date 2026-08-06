package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

// osExit is a seam so tests can intercept process exit.
var osExit = os.Exit

var execCmd = &cobra.Command{
	Use:                "exec command [args...]",
	Short:              "Run shell command on remote",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Flag parsing is off so that `exec ls -la` passes -la to the remote
		// rather than erroring on an unknown flag. The cost is that cobra
		// hands over the global flags unparsed too, and a "--" separator it
		// would normally have consumed -- neither belongs in the remote
		// command string (`sh -c '-- cmd'` errors on every shell).
		args, err := applyGlobalFlags(args)
		if err != nil {
			return err
		}
		if len(args) == 0 {
			return fmt.Errorf("usage: remote-agent exec <command>")
		}
		code, err := client.Exec(strings.Join(args, " "))
		if err != nil {
			return err
		}
		// Mirror the remote command's exit code as our own so callers (including
		// Claude Code's Bash tool) see real success/failure.
		if code != 0 {
			osExit(code)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(execCmd)
}
