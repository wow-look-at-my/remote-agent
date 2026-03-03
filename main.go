package main

import (
	"fmt"
	"os"

	"github.com/wow-look-at-my/remote-agent/agent"
	"github.com/wow-look-at-my/remote-agent/client"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		printUsage()
		return fmt.Errorf("no command specified")
	}

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "connect":
		return client.RunConnect(rest)
	case "disconnect":
		return client.RunDisconnect(rest)
	case "exec":
		return client.RunExec(rest)
	case "upload":
		return client.RunUpload(rest)
	case "download":
		return client.RunDownload(rest)
	case "read":
		return client.RunRead(rest)
	case "write":
		return client.RunWrite(rest)
	case "edit":
		return client.RunEdit(rest)
	case "ls":
		return client.RunLs(rest)
	case "ps":
		return client.RunPs(rest)
	case "sysinfo":
		return client.RunSysinfo(rest)
	case "ping":
		return client.RunPing(rest)
	case "serve":
		return agent.RunServe(rest)
	default:
		printUsage()
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: remote-agent <command> [args]

Commands:
  connect <user@host> [--port 22]          Deploy agent and start daemon
  disconnect                               Cleanup and stop daemon
  exec <command>                           Run shell command on remote
  upload <local-path> <remote-path>        Copy file to remote
  download <remote-path> <local-path>      Copy file from remote
  read <remote-path>                       Read remote file contents
  write <remote-path> [--mode 0644]        Write stdin to remote file
  edit <remote-path> --old '...' --new '...'  Find/replace in remote file
  ls <remote-path> [--recursive]           List remote directory
  ps [--filter name]                       List remote processes
  sysinfo                                  Remote system stats
  ping                                     Health check
  serve <action> [flags]                   (internal: remote helper)
`)
}
