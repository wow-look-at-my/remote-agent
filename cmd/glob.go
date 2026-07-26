package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var globCmd = &cobra.Command{
	Use:   "glob pattern [remote-path]",
	Short: "List remote files matching a glob pattern",
	Long: `List remote files matching a glob pattern, most recently modified first.

"**" matches any number of directories, and brace alternatives expand, so
patterns like "**/*.{go,mod}" work. A pattern without a slash matches the file
name at any depth.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "."
		if len(args) > 1 {
			path = args[1]
		}
		limit, _ := cmd.Flags().GetInt("limit")
		return client.Glob(args[0], path, limit)
	},
}

func init() {
	rootCmd.AddCommand(globCmd)
	globCmd.Flags().Int("limit", 0, "maximum paths to return (0 = server default)")
}
