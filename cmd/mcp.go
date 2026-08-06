package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
	"github.com/wow-look-at-my/remote-agent/mcpserver"
)

// mcpCmd serves the remote filesystem toolset over the MCP stdio transport.
// `remote-agent claude` registers this command as an MCP server so Claude's
// file tools operate on the remote host; it can also be wired into any other
// MCP client by hand (see README).
var mcpCmd = &cobra.Command{
	Use:   "mcp [user@host]",
	Short: "Serve remote filesystem tools to an MCP client over stdio",
	Long: `Serve a remote host's filesystem to an MCP client over stdio.

Exposes read_file, write_file, edit_file, list_dir, glob, grep, upload_file and
download_file. Every tool takes a "target" argument (user@host, or a Host alias
from ~/.ssh/config) naming the machine it acts on, and the SSH connection to
that machine is opened on demand -- nothing has to be started first, and one
server can act on several hosts in a session.

Give a target here (or with --target / REMOTE_AGENT_TARGET) to make it the
default for calls that omit one; without it, every call must carry its own.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := client.DefaultTarget()
		if len(args) == 1 {
			target = args[0]
		}
		return mcpserver.New(client.DaemonBackend{}, mcpServerVersion, target).Serve(os.Stdin, os.Stdout)
	},
}

// mcpServerVersion is reported to MCP clients in the initialize handshake.
const mcpServerVersion = "1.0.0"

func init() {
	rootCmd.AddCommand(mcpCmd)
}
