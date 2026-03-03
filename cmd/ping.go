package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var pingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Health check",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.Ping()
	},
}

func init() {
	rootCmd.AddCommand(pingCmd)
}
