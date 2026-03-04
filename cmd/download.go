package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var downloadCmd = &cobra.Command{
	Use:   "download remote-path local-path",
	Short: "Copy file from remote",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.Download(args[0], args[1])
	},
}

func init() {
	rootCmd.AddCommand(downloadCmd)
}
