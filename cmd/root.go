package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/agent"
	"github.com/wow-look-at-my/remote-agent/client"
)

var rootCmd = &cobra.Command{
	Use:           "remote-agent",
	Short:         "SSH-based remote command execution tool",
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

// --- Connect ---

var connectCmd = &cobra.Command{
	Use:   "connect user@host",
	Short: "Deploy agent and start daemon",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		return client.Connect(args[0], port)
	},
}

// --- Disconnect ---

var disconnectCmd = &cobra.Command{
	Use:   "disconnect",
	Short: "Cleanup and stop daemon",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.Disconnect()
	},
}

// --- Exec ---

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

// --- Upload ---

var uploadCmd = &cobra.Command{
	Use:   "upload local-path remote-path",
	Short: "Copy file to remote",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.Upload(args[0], args[1])
	},
}

// --- Download ---

var downloadCmd = &cobra.Command{
	Use:   "download remote-path local-path",
	Short: "Copy file from remote",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.Download(args[0], args[1])
	},
}

// --- Read ---

var readCmd = &cobra.Command{
	Use:   "read remote-path",
	Short: "Read remote file contents",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.Read(args[0])
	},
}

// --- Write ---

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

// --- Edit ---

var editCmd = &cobra.Command{
	Use:   "edit remote-path",
	Short: "Find/replace in remote file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		oldText, _ := cmd.Flags().GetString("old")
		newText, _ := cmd.Flags().GetString("new")
		return client.Edit(args[0], oldText, newText)
	},
}

// --- Ls ---

var lsCmd = &cobra.Command{
	Use:   "ls [remote-path]",
	Short: "List remote directory",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "."
		if len(args) > 0 {
			path = args[0]
		}
		recursive, _ := cmd.Flags().GetBool("recursive")
		return client.Ls(path, recursive)
	},
}

// --- Ps ---

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List remote processes",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		filter, _ := cmd.Flags().GetString("filter")
		return client.Ps(filter)
	},
}

// --- Sysinfo ---

var sysinfoCmd = &cobra.Command{
	Use:   "sysinfo",
	Short: "Remote system stats",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.Sysinfo()
	},
}

// --- Ping ---

var pingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Health check",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.Ping()
	},
}

// --- Serve (internal: remote helper) ---

var serveCmd = &cobra.Command{
	Use:    "serve",
	Short:  "Internal: remote helper",
	Hidden: true,
}

var serveSysinfoCmd = &cobra.Command{
	Use:   "sysinfo",
	Short: "Gather system info",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return agent.ServeSysinfo()
	},
}

var servePsCmd = &cobra.Command{
	Use:   "ps",
	Short: "List processes",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		filter, _ := cmd.Flags().GetString("filter")
		return agent.ServePs(filter)
	},
}

var serveEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Edit file",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, _ := cmd.Flags().GetString("path")
		oldText, _ := cmd.Flags().GetString("old")
		newText, _ := cmd.Flags().GetString("new")
		if path == "" || oldText == "" {
			return fmt.Errorf("--path and --old flags are required")
		}
		return agent.ServeEdit(path, oldText, newText)
	},
}

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
	rootCmd.AddCommand(
		connectCmd,
		disconnectCmd,
		execCmd,
		uploadCmd,
		downloadCmd,
		readCmd,
		writeCmd,
		editCmd,
		lsCmd,
		psCmd,
		sysinfoCmd,
		pingCmd,
		serveCmd,
	)

	// Connect flags
	connectCmd.Flags().Int("port", 22, "SSH port")

	// Write flags
	writeCmd.Flags().String("mode", "0644", "file mode")

	// Edit flags
	editCmd.Flags().String("old", "", "text to find")
	editCmd.Flags().String("new", "", "replacement text")
	editCmd.MarkFlagRequired("old")

	// Ls flags
	lsCmd.Flags().Bool("recursive", false, "recursive listing")

	// Ps flags
	psCmd.Flags().String("filter", "", "filter by process name")

	// Serve subcommands
	serveCmd.AddCommand(serveSysinfoCmd, servePsCmd, serveEditCmd, serveAuditCmd)

	// Serve ps flags
	servePsCmd.Flags().String("filter", "", "filter processes by name")

	// Serve edit flags
	serveEditCmd.Flags().String("path", "", "file path to edit")
	serveEditCmd.Flags().String("old", "", "text to find")
	serveEditCmd.Flags().String("new", "", "replacement text")

	// Serve audit flags
	serveAuditCmd.Flags().String("action", "", "action to audit")
	serveAuditCmd.Flags().String("detail", "", "action detail")
	serveAuditCmd.Flags().String("user", "", "connecting user")
	serveAuditCmd.Flags().String("client-ip", "", "client IP address")
	serveAuditCmd.Flags().String("fingerprint", "", "SSH key fingerprint")
}
