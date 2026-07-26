package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/agent"
)

var serveGlobCmd = &cobra.Command{
	Use:   "glob",
	Short: "Match files against a glob pattern",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern, _ := cmd.Flags().GetString("pattern")
		path, _ := cmd.Flags().GetString("path")
		limit, _ := cmd.Flags().GetInt("limit")
		if pattern == "" {
			return fmt.Errorf("--pattern flag is required")
		}
		return agent.ServeGlob(agent.GlobOptions{Pattern: pattern, Path: path, Limit: limit})
	},
}

func init() {
	serveCmd.AddCommand(serveGlobCmd)
	serveGlobCmd.Flags().String("pattern", "", "glob pattern to match")
	serveGlobCmd.Flags().String("path", ".", "directory to search")
	serveGlobCmd.Flags().Int("limit", 0, "maximum paths to return")
}
