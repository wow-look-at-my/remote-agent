package cmd

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:           "remote-agent",
	Short:         "SSH-based remote command execution tool",
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
