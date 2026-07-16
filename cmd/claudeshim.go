package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

// claudeShimCmd is the entry point the CLAUDE_CODE_SHELL_PREFIX shim execs
// (see client/shellprefix.sh). Claude Code passes the entire wrapped command
// line as a single argument. Bash tool commands are laundered and forwarded to
// the remote host; hook and MCP stdio server command lines run locally, where
// the env vars and files Claude set up for them actually exist. The
// classification and laundering logic lives in client/claudeshim.go.
var claudeShimCmd = &cobra.Command{
	Use:                "claude-shim <command>",
	Short:              "Internal: CLAUDE_CODE_SHELL_PREFIX entry for 'remote-agent claude'",
	Hidden:             true,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("usage: remote-agent claude-shim <command>")
		}
		// Claude always passes exactly one argument (its prefix wrapper quotes
		// the whole command line as one word); joining is defensive for manual
		// invocations.
		code, err := client.RunClaudeShim(strings.Join(args, " "))
		if err != nil {
			return err
		}
		// Mirror the command's exit code as our own so Claude sees real
		// success/failure (same contract as `remote-agent exec`).
		if code != 0 {
			osExit(code)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(claudeShimCmd)
}
