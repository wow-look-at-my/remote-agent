package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var connectCmd = &cobra.Command{
	Use:   "connect user@host",
	Short: "Deploy agent and start daemon",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		return client.Connect(args[0], port)
	},
}

func init() {
	rootCmd.AddCommand(connectCmd)
	connectCmd.Flags().Int("port", 22, "SSH port")
}
