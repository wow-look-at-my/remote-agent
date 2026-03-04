package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/agent"
)

var servePsCmd = &cobra.Command{
	Use:   "ps",
	Short: "List processes",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		filter, _ := cmd.Flags().GetString("filter")
		return agent.ServePs(filter)
	},
}

func init() {
	serveCmd.AddCommand(servePsCmd)
	servePsCmd.Flags().String("filter", "", "filter processes by name")
}
