package client

import (
	"encoding/base64"
	"encoding/json"
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

// sendRequest locates the daemon socket and sends a request to it.
func sendRequest(req *protocol.DaemonRequest) (*protocol.DaemonResponse, error) {
	sockPath, err := findSocket()
	if err != nil {
		return nil, err
	}
	return sendRequestTo(sockPath, req)
}

// sendRequestTo sends a request to a specific daemon Unix socket and returns the response.
func sendRequestTo(sockPath string, req *protocol.DaemonRequest) (*protocol.DaemonResponse, error) {
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

// findSocket determines which daemon socket to use. An explicit SocketOverride or
// the REMOTE_AGENT_SOCKET / REMOTE_AGENT_TARGET environment variables take
// precedence; otherwise it discovers a single running daemon by globbing TempDir.
func findSocket() (string, error) {
	if SocketOverride != "" {
		return SocketOverride, nil
	}
	if s := os.Getenv("REMOTE_AGENT_SOCKET"); s != "" {
		return s, nil
	}
	if t := os.Getenv("REMOTE_AGENT_TARGET"); t != "" {
		return daemon.SocketPath(t), nil
	}

	pattern := filepath.Join(os.TempDir(), "remote-agent-*.sock")
	matches, _ := filepath.Glob(pattern)

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no daemon running (no socket found at %s)", pattern)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("multiple daemons running (%d sockets found); set REMOTE_AGENT_TARGET or REMOTE_AGENT_SOCKET to pick one", len(matches))
	}
}

// printResponse outputs the daemon response to stdout.
// When OutputJSON is true, outputs indented JSON. Otherwise outputs compact text.
func printResponse(resp *protocol.DaemonResponse, action string) error {
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}

	if OutputJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp.Data)
	}

	return printTextResponse(resp.Data, action)
}

// printTextResponse formats the response data as compact text based on the action type.
func printTextResponse(data any, action string) error {
	m, _ := data.(map[string]any)
	if m == nil {
		fmt.Fprintln(os.Stdout, data)
		return nil
	}

	switch action {
	case "exec":
		return printExecText(m)
	case "read":
		return printReadText(m)
	case "write", "upload", "download":
		return printWriteText(m)
	case "edit":
		return printEditText(m)
	case "ls":
		return printLsText(m)
	case "readlink":
		return printReadlinkText(m)
	case "ps":
		return printPsText(m)
	case "sysinfo":
		return printSysinfoText(m)
	case "ping":
		return printPingText(m)
	case "disconnect":
		fmt.Fprintln(os.Stdout, "disconnecting")
		return nil
	default:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(data)
	}
}

func printExecText(m map[string]any) error {
	stdout, _ := m["stdout"].(string)
	stderr, _ := m["stderr"].(string)

	// Forward stdout to stdout and stderr to stderr unconditionally so the
	// command behaves like a transparent shell. The remote exit code is
	// propagated by the caller as this process's own exit code (see cmd/exec.go),
	// so no "[exit N]" marker is printed.
	if stdout != "" {
		fmt.Fprint(os.Stdout, stdout)
	}
	if stderr != "" {
		fmt.Fprint(os.Stderr, stderr)
	}
	return nil
}

func printReadText(m map[string]any) error {
	content, _ := m["content"].(string)
	fmt.Fprint(os.Stdout, content)
	return nil
}

func printWriteText(m map[string]any) error {
	bytes, _ := m["bytes_written"].(float64)
	fmt.Fprintf(os.Stdout, "%d bytes written\n", int64(bytes))
	return nil
}

func printEditText(m map[string]any) error {
	modified, _ := m["modified"].(bool)
	msg, _ := m["message"].(string)
	if modified {
		fmt.Fprint(os.Stdout, "modified")
	} else {
		fmt.Fprint(os.Stdout, "not modified")
	}
	if msg != "" {
		fmt.Fprintf(os.Stdout, ": %s", msg)
	}
	fmt.Fprintln(os.Stdout)
	return nil
}

func printLsText(m map[string]any) error {
	entries, _ := m["entries"].([]any)
	if entries == nil {
		return nil
	}

	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		size, _ := entry["size"].(float64)
		mode, _ := entry["mode"].(string)
		isDir, _ := entry["is_dir"].(bool)
		isLink, _ := entry["is_link"].(bool)
		target, _ := entry["target"].(string)

		typeChar := "-"
		if isDir {
			typeChar = "d"
		}
		if isLink {
			typeChar = "l"
		}
		if isLink && target != "" {
			fmt.Fprintf(os.Stdout, "%s\t%s\t%d\t%s -> %s\n", typeChar, mode, int64(size), name, target)
		} else {
			fmt.Fprintf(os.Stdout, "%s\t%s\t%d\t%s\n", typeChar, mode, int64(size), name)
		}
	}
	return nil
}

func printReadlinkText(m map[string]any) error {
	target, _ := m["target"].(string)
	fmt.Fprintln(os.Stdout, target)
	return nil
}

func printPsText(m map[string]any) error {
	processes, _ := m["processes"].([]any)
	if processes == nil {
		return nil
	}

	fmt.Fprintf(os.Stdout, "PID\tUSER\tSTATE\tRSS\tCOMMAND\n")
	for _, p := range processes {
		proc, ok := p.(map[string]any)
		if !ok {
			continue
		}
		pid, _ := proc["pid"].(float64)
		user, _ := proc["user"].(string)
		state, _ := proc["state"].(string)
		rss, _ := proc["rss_bytes"].(float64)
		cmd, _ := proc["command"].(string)
		fmt.Fprintf(os.Stdout, "%d\t%s\t%s\t%d\t%s\n", int(pid), user, state, int64(rss), cmd)
	}
	return nil
}

func printSysinfoText(m map[string]any) error {
	hostname, _ := m["hostname"].(string)
	osName, _ := m["os"].(string)
	arch, _ := m["arch"].(string)
	uptime, _ := m["uptime"].(string)

	fmt.Fprintf(os.Stdout, "%s %s %s up %s\n", hostname, osName, arch, uptime)

	if cpu, ok := m["cpu"].(map[string]any); ok {
		model, _ := cpu["model"].(string)
		cores, _ := cpu["cores"].(float64)
		threads, _ := cpu["threads"].(float64)
		mhz, _ := cpu["mhz"].(float64)
		fmt.Fprintf(os.Stdout, "cpu: %s %dc/%dt %.0fMHz\n", model, int(cores), int(threads), mhz)
	}

	if mem, ok := m["memory"].(map[string]any); ok {
		total, _ := mem["total_bytes"].(float64)
		avail, _ := mem["available_bytes"].(float64)
		fmt.Fprintf(os.Stdout, "mem: %.1fG total %.1fG avail\n", total/1e9, avail/1e9)
	}

	if disks, ok := m["disk"].([]any); ok {
		for _, d := range disks {
			disk, ok := d.(map[string]any)
			if !ok {
				continue
			}
			mount, _ := disk["mount_point"].(string)
			total, _ := disk["total_bytes"].(float64)
			pct, _ := disk["use_pct"].(float64)
			fmt.Fprintf(os.Stdout, "disk: %s %.1fG %.0f%%\n", mount, total/1e9, pct)
		}
	}

	return nil
}

func printPingText(m map[string]any) error {
	pong, _ := m["pong"].(bool)
	if pong {
		fmt.Fprintln(os.Stdout, "pong")
	} else {
		fmt.Fprintln(os.Stdout, "fail")
	}
	return nil
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
	return printResponse(resp, "edit")
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
