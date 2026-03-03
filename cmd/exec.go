package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var execCmd = &cobra.Command{
	Use:                "exec command [args...]",
	Short:              "Run shell command on remote",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("usage: remote-agent exec <command>")
		}
		return client.Exec(strings.Join(args, " "))
	},
}

func init() {
	rootCmd.AddCommand(execCmd)
}
