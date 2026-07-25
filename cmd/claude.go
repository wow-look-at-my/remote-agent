package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var claudeCmd = &cobra.Command{
	Use:   "claude [user@host] [-- claude-args...]",
	Short: "Launch Claude Code with its shell wired to run on the remote host",
	Long: `Launch Claude Code so that every Bash tool command and every file operation
it performs executes on a remote host through the remote-agent daemon, instead
of locally.

It starts (or reuses) a daemon for the target, then runs claude with
CLAUDE_CODE_SHELL_PREFIX pointed at a shim that forwards each command to the
remote. The model never has to think about SSH -- it just runs ordinary shell
commands and they land on the remote machine.

Claude's built-in file tools (Read, Write, Edit, Glob, Grep, ...) call the
local filesystem directly and cannot be redirected, so they are switched off
and replaced by an MCP toolset that performs the same operations on the remote
host. Pass --local-tools to keep the built-ins instead (Bash still runs
remotely).

The optional positional argument is the SSH target (user@host). If omitted, a
single already-running daemon is reused. Anything after "--" is passed straight
through to claude. A daemon started by this command is stopped again when claude
exits (use --keep-daemon to leave it running).

Examples:
  remote-agent claude root@build-box
  remote-agent claude root@build-box --port 2222 -- --model opus
  remote-agent claude                       # reuse the one running daemon`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		keep, _ := cmd.Flags().GetBool("keep-daemon")
		claudeBin, _ := cmd.Flags().GetString("claude-bin")
		localTools, _ := cmd.Flags().GetBool("local-tools")

		// Positional args before "--" are the optional target; args after "--"
		// are passed through to claude.
		dash := cmd.ArgsLenAtDash()
		pre := args
		var claudeArgs []string
		if dash >= 0 {
			pre = args[:dash]
			claudeArgs = args[dash:]
		}

		var target string
		switch len(pre) {
		case 0:
			// reuse a running daemon
		case 1:
			target = pre[0]
		default:
			return fmt.Errorf("unexpected arguments %v; pass claude flags after '--'", pre[1:])
		}

		return client.LaunchClaude(client.LaunchOptions{
			Target:     target,
			Port:       port,
			ClaudeBin:  claudeBin,
			ClaudeArgs: claudeArgs,
			KeepDaemon: keep,
			LocalTools: localTools,
		})
	},
}

func init() {
	rootCmd.AddCommand(claudeCmd)
	claudeCmd.Flags().Int("port", 22, "SSH port used when starting a fresh daemon")
	claudeCmd.Flags().Bool("keep-daemon", false, "keep the daemon running after claude exits (only if this command started it)")
	claudeCmd.Flags().String("claude-bin", "claude", "path or name of the claude executable")
	claudeCmd.Flags().Bool("local-tools", false, "keep Claude's built-in file tools (acting on the local machine) instead of the remote MCP toolset")
}
