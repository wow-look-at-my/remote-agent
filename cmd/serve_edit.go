package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/agent"
)

var serveEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Edit file",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, _ := cmd.Flags().GetString("path")
		oldText, _ := cmd.Flags().GetString("old")
		newText, _ := cmd.Flags().GetString("new")
		if path == "" || oldText == "" {
			return fmt.Errorf("--path and --old flags are required")
		}
		return agent.ServeEdit(path, oldText, newText)
	},
}

func init() {
	serveCmd.AddCommand(serveEditCmd)
	serveEditCmd.Flags().String("path", "", "file path to edit")
	serveEditCmd.Flags().String("old", "", "text to find")
	serveEditCmd.Flags().String("new", "", "replacement text")
}
