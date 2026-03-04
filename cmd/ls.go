package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var lsCmd = &cobra.Command{
	Use:   "ls [remote-path]",
	Short: "List remote directory",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "."
		if len(args) > 0 {
			path = args[0]
		}
		recursive, _ := cmd.Flags().GetBool("recursive")
		return client.Ls(path, recursive)
	},
}

func init() {
	rootCmd.AddCommand(lsCmd)
	lsCmd.Flags().Bool("recursive", false, "recursive listing")
}
