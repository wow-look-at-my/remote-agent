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
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		jsonFlag, _ := cmd.Flags().GetBool("json")
		client.OutputJSON = jsonFlag
	}
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
