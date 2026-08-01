package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var rootCmd = &cobra.Command{
	Use:           "remote-agent",
	Short:         "SSH-based remote command execution tool",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().Bool("json", false, "output as JSON instead of compact text")
	rootCmd.PersistentFlags().StringP("target", "t", "", "SSH target user@host (starts a daemon for it if none is running)")
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		jsonFlag, _ := cmd.Flags().GetBool("json")
		client.OutputJSON = jsonFlag
		target, _ := cmd.Flags().GetString("target")
		client.TargetOverride = target
	}
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
