package main

import (
	"fmt"
	"os"

	"github.com/wow-look-at-my/remote-agent/agent"
	"github.com/wow-look-at-my/remote-agent/client"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "connect":
		err = client.RunConnect(os.Args[2:])
	case "disconnect":
		err = client.RunDisconnect(os.Args[2:])
	case "exec":
		err = client.RunExec(os.Args[2:])
	case "upload":
		err = client.RunUpload(os.Args[2:])
	case "download":
		err = client.RunDownload(os.Args[2:])
	case "read":
		err = client.RunRead(os.Args[2:])
	case "write":
		err = client.RunWrite(os.Args[2:])
	case "edit":
		err = client.RunEdit(os.Args[2:])
	case "ls":
		err = client.RunLs(os.Args[2:])
	case "ps":
		err = client.RunPs(os.Args[2:])
	case "sysinfo":
		err = client.RunSysinfo(os.Args[2:])
	case "ping":
		err = client.RunPing(os.Args[2:])
	case "serve":
		err = agent.RunServe(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
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
