package client

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"

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

// Connect starts the daemon with the given target and SSH port.
func Connect(target string, port int) error {
	return daemon.Start(target, port)
}

// Disconnect stops the daemon.
func Disconnect() error {
	resp, err := sendRequest(&protocol.DaemonRequest{Action: "disconnect"})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// Exec runs a command on the remote.
func Exec(command string) error {
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "exec",
		Params: map[string]any{"command": command},
	})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// Upload copies a local file to the remote.
func Upload(localPath, remotePath string) error {
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "upload",
		Params: map[string]any{"local_path": localPath, "remote_path": remotePath},
	})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// Download copies a remote file to local.
func Download(remotePath, localPath string) error {
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "download",
		Params: map[string]any{"remote_path": remotePath, "local_path": localPath},
	})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// Read reads a remote file.
func Read(path string) error {
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "read",
		Params: map[string]any{"path": path},
	})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// Write writes content to a remote file with the given mode.
func Write(path, mode string, content []byte) error {
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "write",
		Params: map[string]any{
			"path":    path,
			"content": string(content),
			"mode":    mode,
		},
	})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// Edit performs a find/replace in a remote file.
func Edit(path, oldText, newText string) error {
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "edit",
		Params: map[string]any{
			"path": path,
			"old":  oldText,
			"new":  newText,
		},
	})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// Ls lists a remote directory.
func Ls(path string, recursive bool) error {
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "ls",
		Params: map[string]any{"path": path, "recursive": recursive},
	})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// Ps lists remote processes, optionally filtered by name.
func Ps(filter string) error {
	params := map[string]any{}
	if filter != "" {
		params["filter"] = filter
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

// Sysinfo gets remote system info.
func Sysinfo() error {
	resp, err := sendRequest(&protocol.DaemonRequest{Action: "sysinfo"})
	if err != nil {
		return err
	}
	return printResponse(resp)
}

// Ping checks if the daemon and remote are healthy.
func Ping() error {
	resp, err := sendRequest(&protocol.DaemonRequest{Action: "ping"})
	if err != nil {
		return err
	}
	return printResponse(resp)
}
