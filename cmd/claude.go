package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var claudeCmd = &cobra.Command{
	Use:   "claude [user@host[:port]] [-- claude-args...]",
	Short: "Launch Claude Code with its shell wired to run on the remote host",
	Long: `Launch Claude Code so that every Bash tool command and every file operation
it performs executes on a remote host through the remote-agent daemon, instead
of locally.

It starts (or reuses) a daemon for the target, then runs claude with
CLAUDE_CODE_SHELL_PREFIX pointed at a shim that forwards each command to the
remote. The model never has to think about SSH -- it just runs ordinary shell
commands and they land on the remote machine.

The remote working directory is mounted locally at the same absolute path, so
Claude's own tools -- Read, Write, Edit, Glob, Grep -- and any other program
that opens a file, including third-party MCP servers and tools that did not
exist when this was written, operate on the remote host without knowing it.
Claude runs with that directory as its working directory.

Pass --dir to choose the remote directory, --mount-at to put the mount
somewhere else locally, or --no-mount where FUSE is unavailable (then only
remote-agent's own MCP tools reach the remote).

The optional positional argument is the SSH target (user@host, or
user@host:2222 for a non-standard SSH port -- each port gets its own daemon).
If omitted, a
running daemon is reused -- and if none is running, a daemon is started for the
target of the last one that did. Anything after "--" is passed straight
through to claude. A daemon started by this command is stopped again when claude
exits (use --keep-daemon to leave it running).

Examples:
  remote-agent claude root@build-box
  remote-agent claude root@build-box:2222        # non-standard SSH port
  remote-agent claude root@build-box --port 2222 -- --model opus
  remote-agent claude                       # reuse the one running daemon`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		keep, _ := cmd.Flags().GetBool("keep-daemon")
		claudeBin, _ := cmd.Flags().GetString("claude-bin")
		localTools, _ := cmd.Flags().GetBool("local-tools")
		remoteDir, _ := cmd.Flags().GetString("dir")
		mountAt, _ := cmd.Flags().GetString("mount-at")
		noMount, _ := cmd.Flags().GetBool("no-mount")

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
			// No positional target: fall back to --target, then to a running
			// daemon (or the last one that ran, which is restarted).
			target = client.TargetOverride
		case 1:
			target = pre[0]
		default:
			return fmt.Errorf("unexpected arguments %v; pass claude flags after '--'", pre[1:])
		}

		return client.LaunchClaude(client.LaunchOptions{
			Target:      target,
			Port:        port,
			ClaudeBin:   claudeBin,
			ClaudeArgs:  claudeArgs,
			KeepDaemon:  keep,
			LocalTools:  localTools,
			RemoteDir:   remoteDir,
			MountAt:     mountAt,
			NoMount:     noMount,
			ControlPath: client.ControlPathOverride,
		})
	},
}

func init() {
	rootCmd.AddCommand(claudeCmd)
	claudeCmd.Flags().Int("port", 0, "SSH port used when starting a fresh daemon (default: the port in the target, else the ssh_config port, else 22)")
	claudeCmd.Flags().Bool("keep-daemon", false, "keep the daemon running after claude exits (only if this command started it)")
	claudeCmd.Flags().String("claude-bin", "claude", "path or name of the claude executable")
	claudeCmd.Flags().String("dir", "", "remote directory to work in (default: the remote home directory)")
	claudeCmd.Flags().String("mount-at", "", "local mount point (default: the same path as --dir)")
	claudeCmd.Flags().Bool("no-mount", false, "do not mount; expose the remote filesystem as MCP tools instead (no FUSE required)")
	claudeCmd.Flags().Bool("local-tools", false, "with --no-mount, keep Claude's built-in file tools so only Bash runs remotely")
}
