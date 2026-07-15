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
		// With flag parsing disabled cobra does not consume a leading "--"
		// separator, and it must not become part of the remote command string
		// (`sh -c '-- cmd'` is an error on every shell). Drop it.
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
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
