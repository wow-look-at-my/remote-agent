package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/agent"
)

var serveGrepCmd = &cobra.Command{
	Use:   "grep",
	Short: "Search files for a regular expression",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern, _ := cmd.Flags().GetString("pattern")
		path, _ := cmd.Flags().GetString("path")
		include, _ := cmd.Flags().GetString("include")
		mode, _ := cmd.Flags().GetString("mode")
		insensitive, _ := cmd.Flags().GetBool("ignore-case")
		context, _ := cmd.Flags().GetInt("context")
		limit, _ := cmd.Flags().GetInt("limit")
		if pattern == "" {
			return fmt.Errorf("--pattern flag is required")
		}
		return agent.ServeGrep(agent.GrepOptions{
			Pattern:         pattern,
			Path:            path,
			Include:         include,
			Mode:            mode,
			CaseInsensitive: insensitive,
			ContextLines:    context,
			Limit:           limit,
		})
	},
}

func init() {
	serveCmd.AddCommand(serveGrepCmd)
	serveGrepCmd.Flags().String("pattern", "", "regular expression to search for")
	serveGrepCmd.Flags().String("path", ".", "file or directory to search")
	serveGrepCmd.Flags().String("include", "", "glob limiting which files are searched")
	serveGrepCmd.Flags().String("mode", "content", "output mode: content, files_with_matches or count")
	serveGrepCmd.Flags().Bool("ignore-case", false, "case-insensitive search")
	serveGrepCmd.Flags().Int("context", 0, "lines of context around each match")
	serveGrepCmd.Flags().Int("limit", 0, "maximum results to return")
}
