package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var sysinfoCmd = &cobra.Command{
	Use:   "sysinfo",
	Short: "Remote system stats",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.Sysinfo()
	},
}

func init() {
	rootCmd.AddCommand(sysinfoCmd)
}
