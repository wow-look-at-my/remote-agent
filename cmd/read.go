package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var readCmd = &cobra.Command{
	Use:   "read remote-path",
	Short: "Read remote file contents",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.Read(args[0])
	},
}

func init() {
	rootCmd.AddCommand(readCmd)
}
