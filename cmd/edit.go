package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var editCmd = &cobra.Command{
	Use:   "edit remote-path",
	Short: "Find/replace in remote file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		oldText, _ := cmd.Flags().GetString("old")
		newText, _ := cmd.Flags().GetString("new")
		replaceAll, _ := cmd.Flags().GetBool("replace-all")
		return client.Edit(args[0], oldText, newText, replaceAll)
	},
}

func init() {
	rootCmd.AddCommand(editCmd)
	editCmd.Flags().String("old", "", "text to find (must be unique unless --replace-all)")
	editCmd.Flags().String("new", "", "replacement text")
	editCmd.Flags().Bool("replace-all", false, "replace every occurrence instead of requiring a unique match")
	editCmd.MarkFlagRequired("old")
}
