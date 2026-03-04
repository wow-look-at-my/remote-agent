package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/agent"
)

var serveSysinfoCmd = &cobra.Command{
	Use:   "sysinfo",
	Short: "Gather system info",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return agent.ServeSysinfo()
	},
}

func init() {
	serveCmd.AddCommand(serveSysinfoCmd)
}
