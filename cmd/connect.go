package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var connectCmd = &cobra.Command{
	Use:   "connect user@host",
	Short: "Deploy agent and start daemon",
	Long: `Deploy the helper binary and start the daemon for a target.

Commands ride an OpenSSH control master when one is answering on the
ControlPath ssh_config sets for the host -- no second authentication, which is
what makes hosts behind a one-time password or a hardware key usable. Pass
--control-path (or set REMOTE_AGENT_CONTROL_PATH) to name a socket explicitly;
the daemon then fails if that master is not there, rather than opening its own
connection to the host.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		// --control-path is a global flag; client resolves it (flag, then
		// REMOTE_AGENT_CONTROL_PATH) so every entry point agrees.
		return client.Connect(args[0], port, "")
	},
}

func init() {
	rootCmd.AddCommand(connectCmd)
	connectCmd.Flags().Int("port", 22, "SSH port")
}
