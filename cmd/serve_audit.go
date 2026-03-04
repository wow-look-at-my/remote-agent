package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/agent"
)

var serveAuditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Log audit event",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		action, _ := cmd.Flags().GetString("action")
		if action == "" {
			return fmt.Errorf("--action flag is required")
		}
		detail, _ := cmd.Flags().GetString("detail")
		user, _ := cmd.Flags().GetString("user")
		clientIP, _ := cmd.Flags().GetString("client-ip")
		fingerprint, _ := cmd.Flags().GetString("fingerprint")
		return agent.ServeAudit(action, detail, user, clientIP, fingerprint)
	},
}

func init() {
	serveCmd.AddCommand(serveAuditCmd)
	serveAuditCmd.Flags().String("action", "", "action to audit")
	serveAuditCmd.Flags().String("detail", "", "action detail")
	serveAuditCmd.Flags().String("user", "", "connecting user")
	serveAuditCmd.Flags().String("client-ip", "", "client IP address")
	serveAuditCmd.Flags().String("fingerprint", "", "SSH key fingerprint")
}
