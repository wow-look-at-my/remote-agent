package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var uploadCmd = &cobra.Command{
	Use:   "upload local-path remote-path",
	Short: "Copy file to remote",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.Upload(args[0], args[1])
	},
}

func init() {
	rootCmd.AddCommand(uploadCmd)
}
