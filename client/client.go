package client

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/wow-look-at-my/remote-agent/daemon"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// OutputJSON controls whether responses are printed as JSON (true) or compact text (false).
var OutputJSON bool

// SocketOverride, when set, forces all requests to a specific daemon socket path,
// bypassing socket discovery. Mainly a programmatic seam; the REMOTE_AGENT_SOCKET
// and REMOTE_AGENT_TARGET environment variables provide the same control.
var SocketOverride string

// sendRequest locates the daemon socket and sends a request to it, starting a
// daemon first when none is running. The daemon is an implementation detail:
// `connect` makes the first command faster, it is not a prerequisite.
func sendRequest(req *protocol.DaemonRequest) (*protocol.DaemonResponse, error) {
	sockPath, err := findSocket()
	if err == nil {
		resp, sendErr := sendRequestTo(sockPath, req)
		if sendErr == nil || !errors.Is(sendErr, errNoDaemon) {
			return resp, sendErr
		}
		err = sendErr // socket present but dead: fall through and restart it
	}
	if !autoStartEnabled(req.Action) {
		return nil, err
	}

	rec, targetErr := resolveTarget()
	if targetErr != nil {
		return nil, fmt.Errorf("%w (%s)", err, targetErr)
	}
	sockPath, startErr := autoStartDaemon(rec)
	if startErr != nil {
		return nil, startErr
	}
	// Pin it for the rest of this process, so a long-lived client does not
	// re-resolve a stale socket path on every later untargeted call.
	resolvedSocket = sockPath
	return sendRequestTo(sockPath, req)
}

// sendRequestFor sends a request to the daemon for an explicit target, starting
// one when none answers. Socket discovery and the process-wide --target are
// bypassed entirely: a caller that names its target (every MCP tool call does)
// must reach that host and no other, whatever else this process has been
// pointed at.
func sendRequestFor(target string, req *protocol.DaemonRequest) (*protocol.DaemonResponse, error) {
	if target == "" {
		return sendRequest(req)
	}
	resp, err := sendRequestTo(daemon.SocketPath(target), req)
	if err == nil || !errors.Is(err, errNoDaemon) {
		return resp, err
	}
	if !autoStartEnabled(req.Action) {
		return nil, err
	}
	sockPath, startErr := autoStartDaemon(recordFor(target))
	if startErr != nil {
		return nil, startErr
	}
	return sendRequestTo(sockPath, req)
}

// sendRequestTo sends a request to a specific daemon Unix socket and returns the response.
func sendRequestTo(sockPath string, req *protocol.DaemonRequest) (*protocol.DaemonResponse, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("%w: connect to daemon at %s: %v", errNoDaemon, sockPath, err)
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

// findSocket determines which daemon socket to use. An explicit SocketOverride,
// the REMOTE_AGENT_SOCKET environment variable, the --target flag or
// REMOTE_AGENT_TARGET take precedence; otherwise it discovers a single running
// daemon by globbing TempDir. A socket path is returned whether or not anything
// is listening on it -- sendRequest starts a daemon when nothing answers.
func findSocket() (string, error) {
	if SocketOverride != "" {
		return SocketOverride, nil
	}
	if resolvedSocket != "" {
		return resolvedSocket, nil
	}
	if s := os.Getenv("REMOTE_AGENT_SOCKET"); s != "" {
		return s, nil
	}
	if TargetOverride != "" {
		return daemon.SocketPath(TargetOverride), nil
	}
	if t := os.Getenv("REMOTE_AGENT_TARGET"); t != "" {
		return daemon.SocketPath(t), nil
	}

	pattern := filepath.Join(os.TempDir(), "remote-agent-*.sock")
	matches, _ := filepath.Glob(pattern)

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%w (no socket found at %s)", errNoDaemon, pattern)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("multiple daemons running (%d sockets found); pass --target user@host, or set REMOTE_AGENT_TARGET or REMOTE_AGENT_SOCKET to pick one", len(matches))
	}
}

// Call sends an action to the daemon and decodes the response payload into
// out (which may be nil when only success matters). It is the programmatic
// counterpart to the printing helpers below: the MCP server needs the
// structured result, not text on stdout.
func Call(action string, params map[string]any, out any) error {
	return CallTarget("", action, params, out)
}

// CallTarget is Call against a named SSH target, starting a daemon for that
// target when none is running. An empty target falls back to Call's discovery.
func CallTarget(target, action string, params map[string]any, out any) error {
	resp, err := sendRequestFor(target, &protocol.DaemonRequest{Action: action, Params: params})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	if out == nil {
		return nil
	}
	// The payload arrives as decoded JSON (map/slice values); round-trip it
	// into the caller's typed struct rather than hand-asserting every field.
	data, err := json.Marshal(resp.Data)
	if err != nil {
		return fmt.Errorf("encode %s result: %w", action, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s result: %w", action, err)
	}
	return nil
}

// DaemonBackend adapts the daemon client to the Backend interface the MCP
// server expects, without either package importing the other.
type DaemonBackend struct{}

// Call implements the MCP server's Backend interface.
func (DaemonBackend) Call(target, action string, params map[string]any, out any) error {
	return CallTarget(target, action, params, out)
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
	return printResponse(resp, "disconnect")
}

// Exec runs a command on the remote and returns the remote command's exit code.
// A non-nil error indicates a transport/daemon failure (distinct from the remote
// command exiting non-zero, which is reported via the returned exit code).
func Exec(command string) (int, error) {
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "exec",
		Params: map[string]any{"command": command},
	})
	if err != nil {
		return 0, err
	}
	if resp.Error != "" {
		return 0, fmt.Errorf("%s", resp.Error)
	}
	// The exec handler may rewrite ls commands to use the native ls handler,
	// so the response could be either an ExecResult or a DirListing.
	// Detect by checking for "entries" key (ls) vs "stdout" key (exec).
	if m, ok := resp.Data.(map[string]any); ok {
		if _, hasEntries := m["entries"]; hasEntries {
			return 0, printResponse(resp, "ls")
		}
		if err := printResponse(resp, "exec"); err != nil {
			return 0, err
		}
		code, _ := m["exit_code"].(float64)
		return int(code), nil
	}
	return 0, printResponse(resp, "exec")
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
	return printResponse(resp, "upload")
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
	return printResponse(resp, "download")
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
	return printResponse(resp, "read")
}

// Write writes content to a remote file with the given mode.
func Write(path, mode string, content []byte) error {
	params := map[string]any{
		"path": path,
		"mode": mode,
	}
	// Valid UTF-8 travels as a plain string (human-readable in --json). Any
	// other bytes are base64-framed: encoding/json silently replaces invalid
	// UTF-8 with U+FFFD, which would corrupt binary payloads on the socket hop.
	if utf8.Valid(content) {
		params["content"] = string(content)
	} else {
		params["content_b64"] = base64.StdEncoding.EncodeToString(content)
	}

	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "write",
		Params: params,
	})
	if err != nil {
		return err
	}
	return printResponse(resp, "write")
}

// Edit performs a find/replace in a remote file. Unless replaceAll is set the
// text must occur exactly once, so an ambiguous edit fails instead of silently
// changing the first match.
func Edit(path, oldText, newText string, replaceAll bool) error {
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "edit",
		Params: map[string]any{
			"path":        path,
			"old":         oldText,
			"new":         newText,
			"replace_all": replaceAll,
		},
	})
	if err != nil {
		return err
	}
	return printResponse(resp, "edit")
}

// Glob lists remote files matching a glob pattern, newest first.
func Glob(pattern, path string, limit int) error {
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "glob",
		Params: map[string]any{"pattern": pattern, "path": path, "limit": limit},
	})
	if err != nil {
		return err
	}
	return printResponse(resp, "glob")
}

// Grep searches remote files for a regular expression.
func Grep(pattern, path, include, mode string, ignoreCase bool, context, limit int) error {
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "grep",
		Params: map[string]any{
			"pattern":     pattern,
			"path":        path,
			"include":     include,
			"mode":        mode,
			"ignore_case": ignoreCase,
			"context":     context,
			"limit":       limit,
		},
	})
	if err != nil {
		return err
	}
	return printResponse(resp, "grep")
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
	return printResponse(resp, "ls")
}

// Readlink resolves a symlink path on the remote.
func Readlink(path string) error {
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "readlink",
		Params: map[string]any{"path": path},
	})
	if err != nil {
		return err
	}
	return printResponse(resp, "readlink")
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
	return printResponse(resp, "ps")
}

// Sysinfo gets remote system info.
func Sysinfo() error {
	resp, err := sendRequest(&protocol.DaemonRequest{Action: "sysinfo"})
	if err != nil {
		return err
	}
	return printResponse(resp, "sysinfo")
}

// Ping checks if the daemon and remote are healthy.
func Ping() error {
	resp, err := sendRequest(&protocol.DaemonRequest{Action: "ping"})
	if err != nil {
		return err
	}
	return printResponse(resp, "ping")
}

// Mount mounts remotePath from the remote host at localPath, so every local
// program reads and writes remote files through ordinary paths.
func Mount(localPath, remotePath string, allowOther bool) error {
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "mount",
		Params: map[string]any{
			"local_path":  localPath,
			"remote_path": remotePath,
			"allow_other": allowOther,
		},
	})
	if err != nil {
		return err
	}
	return printResponse(resp, "mount")
}

// Unmount detaches the mount at localPath.
func Unmount(localPath string) error {
	resp, err := sendRequest(&protocol.DaemonRequest{
		Action: "unmount",
		Params: map[string]any{"local_path": localPath},
	})
	if err != nil {
		return err
	}
	return printResponse(resp, "unmount")
}

// Mounts lists the daemon's live mounts.
func Mounts() error {
	resp, err := sendRequest(&protocol.DaemonRequest{Action: "mounts"})
	if err != nil {
		return err
	}
	return printResponse(resp, "mounts")
}
