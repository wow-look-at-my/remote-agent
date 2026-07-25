package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/agent"
)

// serveFSCmd is the remote half of a mount. Unlike the other serve
// subcommands it is long-lived: it stays attached to one SSH channel and
// answers filesystem requests until the local mount goes away.
var serveFSCmd = &cobra.Command{
	Use:   "fs",
	Short: "Serve filesystem operations for a remote mount",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		root, _ := cmd.Flags().GetString("root")
		if root == "" {
			root = "/"
		}
		return agent.ServeFS(root, os.Stdin, os.Stdout)
	},
}

func init() {
	serveCmd.AddCommand(serveFSCmd)
	serveFSCmd.Flags().String("root", "/", "directory the mount exposes")
}
