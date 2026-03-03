package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var readlinkCmd = &cobra.Command{
	Use:   "readlink remote-path",
	Short: "Resolve symlink target on remote",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.Readlink(args[0])
	},
}

func init() {
	rootCmd.AddCommand(readlinkCmd)
}
