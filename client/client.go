package client

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/remote-agent/daemon"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// sendRequest connects to the daemon Unix socket, sends a request, and returns the response.
func sendRequest(req *protocol.DaemonRequest) (*protocol.DaemonResponse, error) {
	sockPath, err := findSocket()
	if err != nil {
		return nil, err
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w (is the daemon running? use 'remote-agent connect' first)", err)
	}
	defer conn.Close()

	// Send request
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	// Read response
	var resp protocol.DaemonResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return &resp, nil
}

// findSocket looks for a daemon Unix socket.
func findSocket() (string, error) {
	pattern := filepath.Join(os.TempDir(), "remote-agent-*.sock")
	matches, _ := filepath.Glob(pattern)

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no daemon running (no socket found at %s)", pattern)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("multiple daemons running (%d sockets found); specify --host", len(matches))
	}
}

// printResponse outputs the daemon response as JSON to stdout.
func printResponse(resp *protocol.DaemonResponse) error {
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(resp.Data)
}

// RunConnect starts the daemon.
func RunConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	port := fs.Int("port", 22, "SSH port")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: remote-agent connect <user@host> [--port 22]")
	}
	target := fs.Arg(0)
	return daemon.Start(target, *port)
}

// RunDisconnect stops the daemon.
func RunDisconnect(args []string) error {
	resp, err := sendRequest(&protocol.DaemonRequest{Action: "disconnect"})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// RunExec runs a command on the remote.
func RunExec(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: remote-agent exec <command>")
	}
	command := strings.Join(args, " ")
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "exec",
		Params: map[string]any{"command": command},
	})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// RunUpload copies a local file to the remote.
func RunUpload(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: remote-agent upload <local-path> <remote-path>")
	}
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "upload",
		Params: map[string]any{"local_path": args[0], "remote_path": args[1]},
	})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// RunDownload copies a remote file to local.
func RunDownload(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: remote-agent download <remote-path> <local-path>")
	}
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "download",
		Params: map[string]any{"remote_path": args[0], "local_path": args[1]},
	})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// RunRead reads a remote file.
func RunRead(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: remote-agent read <remote-path>")
	}
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "read",
		Params: map[string]any{"path": args[0]},
	})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// RunWrite writes stdin to a remote file.
func RunWrite(args []string) error {
	fs := flag.NewFlagSet("write", flag.ContinueOnError)
	mode := fs.String("mode", "0644", "file mode")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: remote-agent write <remote-path> [--mode 0644]")
	}
	remotePath := fs.Arg(0)

	// Read from stdin
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "write",
		Params: map[string]any{
			"path":    remotePath,
			"content": string(data),
			"mode":    *mode,
		},
	})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// RunEdit edits a remote file with find/replace.
func RunEdit(args []string) error {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	oldText := fs.String("old", "", "text to find")
	newText := fs.String("new", "", "replacement text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 || *oldText == "" {
		return fmt.Errorf("usage: remote-agent edit <remote-path> --old <text> --new <text>")
	}

	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "edit",
		Params: map[string]any{
			"path": fs.Arg(0),
			"old":  *oldText,
			"new":  *newText,
		},
	})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// RunLs lists a remote directory.
func RunLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	recursive := fs.Bool("recursive", false, "recursive listing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := "."
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}

	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "ls",
		Params: map[string]any{"path": path, "recursive": *recursive},
	})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// RunPs lists remote processes.
func RunPs(args []string) error {
	fs := flag.NewFlagSet("ps", flag.ContinueOnError)
	filter := fs.String("filter", "", "filter by process name")
	if err := fs.Parse(args); err != nil {
		return err
	}

	params := map[string]any{}
	if *filter != "" {
		params["filter"] = *filter
	}

	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "ps",
		Params: params,
	})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// RunSysinfo gets remote system info.
func RunSysinfo(args []string) error {
	resp, err := sendRequest(&protocol.DaemonRequest{Action: "sysinfo"})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// RunPing checks if the daemon and remote are healthy.
func RunPing(args []string) error {
	resp, err := sendRequest(&protocol.DaemonRequest{Action: "ping"})
	if err != nil {
		return err
	}
	return printResponse(resp)
}
