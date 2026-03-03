package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var disconnectCmd = &cobra.Command{
	Use:   "disconnect",
	Short: "Cleanup and stop daemon",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.Disconnect()
	},
}

func init() {
	rootCmd.AddCommand(disconnectCmd)
}
