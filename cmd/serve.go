package cmd

import "github.com/spf13/cobra"

var serveCmd = &cobra.Command{
	Use:    "serve",
	Short:  "Internal: remote helper",
	Hidden: true,
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
