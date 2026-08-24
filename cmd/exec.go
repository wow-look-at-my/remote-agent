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
		// Flag parsing is off, so `exec ls -la` reaches the remote intact. That leaves
		// the global flags and a "--" unparsed here, and neither belongs in the command.
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
