package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var writeCmd = &cobra.Command{
	Use:   "write remote-path",
	Short: "Write stdin to remote file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mode, _ := cmd.Flags().GetString("mode")
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		return client.Write(args[0], mode, data)
	},
}

func init() {
	rootCmd.AddCommand(writeCmd)
	writeCmd.Flags().String("mode", "0644", "file mode")
}
