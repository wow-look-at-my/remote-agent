package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/remote-agent/client"
)

var rootCmd = &cobra.Command{
	Use:           "remote-agent",
	Short:         "SSH-based remote command execution tool",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().Bool("json", false, "output as JSON instead of compact text")
	rootCmd.PersistentFlags().StringP("target", "t", "", "SSH target user@host[:port] (starts a daemon for it if none is running)")
	rootCmd.PersistentFlags().String("control-path", "", "OpenSSH control-master socket to run through (required once given)")
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		jsonFlag, _ := cmd.Flags().GetBool("json")
		client.OutputJSON = jsonFlag
		target, _ := cmd.Flags().GetString("target")
		client.TargetOverride = target
		controlPath, _ := cmd.Flags().GetString("control-path")
		client.ControlPathOverride = controlPath
	}
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

// applyGlobalFlags consumes the global flags from the front of the arguments
// of a command that parses its own flags, applies them, and returns what is
// left. Cobra hands such a command every token it did not recognize as the
// command name -- the global flags typed before it included -- and never
// parses them, so `remote-agent --target host exec ls` would otherwise run on
// whichever daemon socket discovery happened to find and paste "--target
// host" into the remote command string.
func applyGlobalFlags(args []string) ([]string, error) {
	for len(args) > 0 {
		switch arg := args[0]; {
		case arg == "--":
			return args[1:], nil
		case arg == "--json":
			client.OutputJSON = true
		case strings.HasPrefix(arg, "--json="):
			v, err := strconv.ParseBool(strings.TrimPrefix(arg, "--json="))
			if err != nil {
				return nil, fmt.Errorf("--json takes a boolean: %w", err)
			}
			client.OutputJSON = v
		case arg == "--target" || arg == "-t":
			if len(args) < 2 {
				return nil, fmt.Errorf("--target needs a value (user@host[:port])")
			}
			client.TargetOverride = args[1]
			args = args[1:]
		case strings.HasPrefix(arg, "--target="):
			client.TargetOverride = strings.TrimPrefix(arg, "--target=")
		case arg == "--control-path":
			if len(args) < 2 {
				return nil, fmt.Errorf("--control-path needs a value (a control-master socket path)")
			}
			client.ControlPathOverride = args[1]
			args = args[1:]
		case strings.HasPrefix(arg, "--control-path="):
			client.ControlPathOverride = strings.TrimPrefix(arg, "--control-path=")
		default:
			return args, nil
		}
		args = args[1:]
	}
	return args, nil
}
