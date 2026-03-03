package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List remote processes",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		filter, _ := cmd.Flags().GetString("filter")
		return client.Ps(filter)
	},
}

func init() {
	rootCmd.AddCommand(psCmd)
	psCmd.Flags().String("filter", "", "filter by process name")
}
