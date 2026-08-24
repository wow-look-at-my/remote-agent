package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
	"github.com/wow-look-at-my/remote-agent/mcpserver"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// mcpCmd serves the remote filesystem toolset over the MCP stdio transport.
// `remote-agent claude` registers this command as an MCP server so Claude's
// file tools operate on the remote host; it can also be wired into any other
// MCP client by hand (see README).
var mcpCmd = &cobra.Command{
	Use:   "mcp [user@host[:port]]",
	Short: "Serve remote command and filesystem tools to an MCP client over stdio",
	Long: `Serve a remote host's shell and filesystem to an MCP client over stdio.

Exposes run_command, read_file, write_file, edit_file, list_dir, glob, grep,
upload_file and download_file. Every tool takes a "target" argument (user@host,
user@host:2222 for a non-standard SSH port, or a Host alias from ~/.ssh/config)
naming the machine it acts on, and the SSH connection to that machine is opened
on demand -- nothing has to be started first, and one server can act on several
hosts in a session. Each port is its own machine: root@127.0.0.1:2201 and
root@127.0.0.1:2202 get separate connections.

Give a target here (or with --target / REMOTE_AGENT_TARGET) to make it the
default for calls that omit one; without it, every call must carry its own.

Every tool also takes an optional "control_path": the path of an OpenSSH
control-master socket to run through, for a host this process cannot
authenticate to on its own. A call that names one uses that master or fails.
--control-path / REMOTE_AGENT_CONTROL_PATH sets a default for calls that
name none.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target, err := client.DefaultTarget()
		if err != nil {
			return err
		}
		if len(args) == 1 {
			if target, err = client.TargetKey(args[0]); err != nil {
				return err
			}
		}
		controlPath := client.ControlPathFor(protocol.Route{})
		return mcpserver.New(client.DaemonBackend{}, mcpServerVersion, target, controlPath).Serve(os.Stdin, os.Stdout)
	},
}

// mcpServerVersion is reported to MCP clients in the initialize handshake.
const mcpServerVersion = "1.0.0"

func init() {
	rootCmd.AddCommand(mcpCmd)
}
