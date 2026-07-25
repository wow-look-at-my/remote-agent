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
	Use:   "mcp",
	Short: "Serve remote filesystem tools to an MCP client over stdio",
	Long: `Serve the remote host's filesystem to an MCP client over stdio.

Exposes read_file, write_file, edit_file, list_dir, glob, grep, upload_file and
download_file, each operating on the remote host through a running remote-agent
daemon (start one with 'remote-agent connect', or let 'remote-agent claude' do
it). The daemon socket is selected exactly as it is for every other command,
via REMOTE_AGENT_SOCKET or REMOTE_AGENT_TARGET.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return mcpserver.New(client.DaemonBackend{}, mcpServerVersion).Serve(os.Stdin, os.Stdout)
	},
}

// mcpServerVersion is reported to MCP clients in the initialize handshake.
const mcpServerVersion = "1.0.0"

func init() {
	rootCmd.AddCommand(mcpCmd)
}
