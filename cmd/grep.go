package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var grepCmd = &cobra.Command{
	Use:   "grep pattern [remote-path]",
	Short: "Search remote files for a regular expression",
	Long: `Search remote files for a regular expression (RE2 syntax).

Binary files and generated directories (.git, node_modules) are skipped.
Output mode "content" prints matching lines, "files_with_matches" prints only
paths, and "count" prints per-file match counts.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "."
		if len(args) > 1 {
			path = args[1]
		}
		include, _ := cmd.Flags().GetString("include")
		mode, _ := cmd.Flags().GetString("mode")
		ignoreCase, _ := cmd.Flags().GetBool("ignore-case")
		context, _ := cmd.Flags().GetInt("context")
		limit, _ := cmd.Flags().GetInt("limit")
		return client.Grep(args[0], path, include, mode, ignoreCase, context, limit)
	},
}

func init() {
	rootCmd.AddCommand(grepCmd)
	grepCmd.Flags().String("include", "", "glob limiting which files are searched (e.g. '**/*.go')")
	grepCmd.Flags().String("mode", "content", "output mode: content, files_with_matches or count")
	grepCmd.Flags().BoolP("ignore-case", "i", false, "case-insensitive search")
	grepCmd.Flags().IntP("context", "C", 0, "lines of context around each match")
	grepCmd.Flags().Int("limit", 0, "maximum results to return (0 = server default)")
}
