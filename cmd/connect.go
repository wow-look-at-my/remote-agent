package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var connectCmd = &cobra.Command{
	Use:   "connect user@host[:port]",
	Short: "Deploy agent and start daemon",
	Long: `Deploy the helper binary and start the daemon for a target.

The port is part of the target. Write it into the target (user@host:2201) or
pass --port; either way one daemon serves one endpoint, so several hosts
reached as root@127.0.0.1 on different ports stay separate.

Commands ride an OpenSSH control master when one is answering on the
ControlPath ssh_config sets for the host -- no second authentication, which is
what makes hosts behind a one-time password or a hardware key usable. Pass
--control-path (or set REMOTE_AGENT_CONTROL_PATH) to name a socket explicitly;
the daemon then fails if that master is not there, rather than opening its own
connection to the host.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		// The client resolves --control-path itself, so every entry point agrees.
		return client.Connect(args[0], port, "")
	},
}

func init() {
	rootCmd.AddCommand(connectCmd)
	connectCmd.Flags().Int("port", 0, "SSH port (default: the port in the target, else the ssh_config port, else 22)")
}
