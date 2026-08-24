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

// Forces every request to one socket, bypassing discovery. REMOTE_AGENT_SOCKET does the same.
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
	// Pinned, so a long-lived client stops re-resolving on every later untargeted call.
	resolvedSocket = sockPath
	return sendRequestTo(sockPath, req)
}

// sendRequestFor sends a request to the daemon for an explicit target, starting
// one when none answers. Socket discovery and the process-wide --target are
// bypassed entirely: a caller that names its target (every MCP tool call does)
// must reach that host and no other, whatever else this process has been
// pointed at.
func sendRequestFor(route protocol.Route, req *protocol.DaemonRequest) (*protocol.DaemonResponse, error) {
	if route.Target == "" {
		return sendRequest(req)
	}
	// Two ports on one host are two daemons, so resolve the key once and key every lookup on it.
	key, err := TargetKey(route.Target)
	if err != nil {
		return nil, err
	}
	route.Target = key
	if err := checkControlPath(route); err != nil {
		return nil, err
	}
	resp, err := sendRequestTo(daemon.SocketPath(route.Target), req)
	if err == nil || !errors.Is(err, errNoDaemon) {
		return resp, err
	}
	if !autoStartEnabled(req.Action) {
		return nil, err
	}
	rec, recErr := recordFor(route)
	if recErr != nil {
		return nil, recErr
	}
	sockPath, startErr := autoStartDaemon(rec)
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
		key, err := TargetKey(TargetOverride)
		if err != nil {
			return "", err
		}
		return daemon.SocketPath(key), nil
	}
	if t := os.Getenv("REMOTE_AGENT_TARGET"); t != "" {
		key, err := TargetKey(t)
		if err != nil {
			return "", err
		}
		return daemon.SocketPath(key), nil
	}

	pattern := filepath.Join(os.TempDir(), "remote-agent-*.sock")
	matches, _ := filepath.Glob(pattern)

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%w (no socket found at %s)", errNoDaemon, pattern)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("multiple daemons running (%d sockets found); pass --target user@host[:port], or set REMOTE_AGENT_TARGET or REMOTE_AGENT_SOCKET to pick one", len(matches))
	}
}

// Call decodes the daemon's payload into out, which may be nil. It is what the MCP
// server uses: a structured result rather than text on stdout.
func Call(action string, params map[string]any, out any) error {
	return CallRoute(protocol.Route{}, action, params, out)
}

// CallTarget is Call against a named SSH target, starting a daemon for that
// target when none is running. An empty target falls back to Call's discovery.
func CallTarget(target, action string, params map[string]any, out any) error {
	return CallRoute(protocol.Route{Target: target}, action, params, out)
}

// CallRoute is Call against a named target reached a named way -- through a
// control master, when the route says so.
func CallRoute(route protocol.Route, action string, params map[string]any, out any) error {
	resp, err := sendRequestFor(route, &protocol.DaemonRequest{Action: action, Params: params})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	if out == nil {
		return nil
	}
	// Round-trip the decoded JSON into the caller's struct, rather than assert every field.
	data, err := json.Marshal(resp.Data)
	if err != nil {
		return fmt.Errorf("encode %s result: %w", action, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s result: %w", action, err)
	}
	return nil
}

// DaemonBackend satisfies the MCP server's Backend, so neither package imports the other.
type DaemonBackend struct{}

// Call implements the MCP server's Backend interface.
func (DaemonBackend) Call(route protocol.Route, action string, params map[string]any, out any) error {
	return CallRoute(route, action, params, out)
}

// Connect starts a target's daemon. A port folds into the target, and a controlPath
// names a control master the daemon must run its commands through.
func Connect(target string, port int, controlPath string) error {
	return daemon.Start(daemon.StartOptions{
		Target:      target,
		Port:        port,
		ControlPath: ControlPathFor(protocol.Route{ControlPath: controlPath}),
	})
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
	// Other bytes must be base64: encoding/json replaces invalid UTF-8 with U+FFFD.
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
