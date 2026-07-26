package cmd

import (
	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var mountCmd = &cobra.Command{
	Use:   "mount <mountpoint> [remote-path]",
	Short: "Mount the remote filesystem locally",
	Long: `Mount the remote host's filesystem at a local directory.

Every local program -- editors, compilers, language servers, agent tools --
then reads and writes remote files through ordinary paths, because the access
goes through the kernel rather than through this CLI. The mount is served over
the daemon's existing SSH connection; no second login, no sshfs.

The mount point must be an empty (or non-existent) directory: mounting over
files would hide them. Mounts live as long as the daemon and are detached when
it stops, so 'disconnect' never leaves a dead mount point behind.

Examples:
  remote-agent mount /mnt/remote            # the whole remote filesystem
  remote-agent mount ~/work/app /srv/app    # just one remote directory`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		remotePath := "/"
		if len(args) > 1 {
			remotePath = args[1]
		}
		allowOther, _ := cmd.Flags().GetBool("allow-other")
		return client.Mount(args[0], remotePath, allowOther)
	},
}

var unmountCmd = &cobra.Command{
	Use:     "unmount <mountpoint>",
	Aliases: []string{"umount"},
	Short:   "Detach a remote filesystem mount",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.Unmount(args[0])
	},
}

var mountsCmd = &cobra.Command{
	Use:   "mounts",
	Short: "List live remote filesystem mounts",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return client.Mounts()
	},
}

func init() {
	rootCmd.AddCommand(mountCmd)
	rootCmd.AddCommand(unmountCmd)
	rootCmd.AddCommand(mountsCmd)
	mountCmd.Flags().Bool("allow-other", false, "let other local users access the mount")
}
