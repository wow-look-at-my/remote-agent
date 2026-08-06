package client

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wow-look-at-my/remote-agent/daemon"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

//go:embed shellprefix.sh
var shellPrefixShim []byte

// LaunchOptions configures a `remote-agent claude` launch.
type LaunchOptions struct {
	Target     string   // SSH target user@host (optional if a daemon is already running)
	Port       int      // SSH port used when starting a fresh daemon
	ClaudeBin  string   // path or name of the claude executable (default "claude")
	ClaudeArgs []string // extra arguments passed through to claude
	KeepDaemon bool     // keep a daemon we started running after claude exits
	ShimDir    string   // directory for the shim + daemon log (default os.TempDir())

	// RemoteDir is the remote directory to work in, mounted locally at the
	// same path. Empty means the remote home directory.
	RemoteDir string
	// MountAt overrides the local mount point. Leaving it empty mounts at the
	// identical path, which is what keeps the two halves of the session
	// coherent: a path means the same thing to a file tool (local, through
	// the mount) and to a shell command (remote, over SSH).
	MountAt string
	// NoMount skips the mount and falls back to serving the remote filesystem
	// as MCP tools -- for platforms where FUSE is unavailable. Only the tools
	// remote-agent itself provides reach the remote in that mode.
	NoMount bool
	// LocalTools, with NoMount, leaves Claude's built-in file tools in place
	// so only Bash runs remotely.
	LocalTools bool
}

// disabledLocalTools are the built-in Claude Code tools that reach the local
// filesystem directly. They have no shell-prefix hook -- they call Node's fs
// against the machine Claude runs on -- so the only way to keep a remote
// session honest is to take them out of the tool set and hand the model the
// remote MCP equivalents instead.
//
// Only names that actually exist are listed: claude warns at startup for each
// deny rule that "matches no known tool", so speculative entries (MultiEdit,
// NotebookRead) are startup noise rather than insurance.
var disabledLocalTools = []string{"Read", "Write", "Edit", "NotebookEdit", "Glob", "Grep"}

// mcpServerName is the MCP server name Claude prefixes onto every remote tool
// (mcp__remote__read_file, ...).
const mcpServerName = "remote"

// preAllowedRemoteTools are the read-only remote tools allowed up front, so
// the swap keeps the permission behaviour of the built-ins it replaces:
// Read/Glob/Grep never prompted, while Write/Edit did. The mutating tools
// (write_file, edit_file, upload_file, download_file) are deliberately absent
// and still go through the normal permission prompt.
var preAllowedRemoteTools = []string{
	"mcp__" + mcpServerName + "__read_file",
	"mcp__" + mcpServerName + "__list_dir",
	"mcp__" + mcpServerName + "__glob",
	"mcp__" + mcpServerName + "__grep",
}

// Test seams so the orchestration can be exercised without a real daemon/claude.
var (
	startDaemonFunc = startDaemonProcess
	runClaudeFunc   = runClaudeProcess
	daemonReadyWait = 30 * time.Second
)

// LaunchClaude starts (or reuses) a remote-agent daemon for the target host and
// runs claude with its Bash tool wired to execute commands on the remote machine
// via CLAUDE_CODE_SHELL_PREFIX.
func LaunchClaude(opts LaunchOptions) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate remote-agent binary: %w", err)
	}

	sockPath, target, startedDaemon, err := ensureDaemon(self, opts)
	if err != nil {
		return err
	}
	opts.Target = target

	shimPath, err := writeShim(tmpDir(opts.ShimDir))
	if err != nil {
		return fmt.Errorf("write shell prefix shim: %w", err)
	}

	claudeBin := opts.ClaudeBin
	if claudeBin == "" {
		claudeBin = "claude"
	}
	resolved, err := exec.LookPath(claudeBin)
	if err != nil {
		return fmt.Errorf("cannot find claude binary %q on PATH: %w (use --claude-bin)", claudeBin, err)
	}

	overrides := map[string]string{
		"CLAUDE_CODE_SHELL_PREFIX": shimPath,
		"REMOTE_AGENT_BIN":         self,
		"REMOTE_AGENT_SOCKET":      sockPath,
	}
	if opts.Target != "" {
		// So a forwarded command or the MCP server can bring the daemon back
		// up by itself if it dies or idles out mid-session.
		overrides["REMOTE_AGENT_TARGET"] = opts.Target
	}
	env := buildEnv(os.Environ(), overrides)

	claudeArgs := opts.ClaudeArgs
	workDir := ""

	if opts.NoMount {
		// Fallback mode: no mount, so only tools remote-agent itself provides
		// can reach the remote host.
		if opts.LocalTools {
			fmt.Fprintf(os.Stderr, "Launching claude; Bash commands will run on the remote host (file tools stay local).\n")
		} else {
			configPath, err := writeMCPConfig(tmpDir(opts.ShimDir), self, sockPath, opts.Target)
			if err != nil {
				return fmt.Errorf("write MCP config: %w", err)
			}
			claudeArgs = append(remoteToolArgs(configPath), claudeArgs...)
			fmt.Fprintf(os.Stderr, "Launching claude without a mount; only remote-agent's own tools reach the remote host.\n")
		}
	} else {
		mountPoint, remoteDir, err := mountForSession(sockPath, opts)
		if err != nil {
			return err
		}
		defer func() {
			if err := unmountSession(sockPath, mountPoint); err != nil {
				fmt.Fprintf(os.Stderr, "warning: %v\n", err)
			}
		}()

		workDir = mountPoint
		if mountPoint == remoteDir {
			// Paths mean the same thing on both sides, so forwarded commands
			// can run in the directory claude is working in rather than the
			// remote home. The shim reads this.
			env = buildEnv(env, map[string]string{"REMOTE_AGENT_MOUNT": mountPoint})
			fmt.Fprintf(os.Stderr, "Mounted %s at the same local path; every tool now works on the remote host.\n", remoteDir)
		} else {
			fmt.Fprintf(os.Stderr, "Mounted %s at %s; every tool now works on the remote host.\n", remoteDir, mountPoint)
			fmt.Fprintf(os.Stderr, "Note: the mount point differs from the remote path, so file paths (%s/...) and "+
				"shell paths (%s/...) are not the same string.\n", mountPoint, remoteDir)
		}
	}

	runErr := runClaudeFunc(resolved, claudeArgs, env, workDir)

	if startedDaemon && !opts.KeepDaemon {
		fmt.Fprintf(os.Stderr, "Stopping remote-agent daemon...\n")
		if err := disconnectSocket(sockPath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to stop daemon: %v\n", err)
		}
	}
	return runErr
}

// ensureDaemon resolves the daemon socket for the launch, starting a new daemon
// if a target is given and none is already running. It returns the target that
// socket serves -- recovered from the daemon's own record when the launch did
// not name one -- and whether this call started the daemon (so the caller knows
// whether to tear it down).
func ensureDaemon(self string, opts LaunchOptions) (sockPath, target string, started bool, err error) {
	if opts.Target == "" {
		// No target: reuse the single running daemon, if exactly one exists.
		if s, ferr := findSocket(); ferr == nil && pingSocket(s) {
			fmt.Fprintf(os.Stderr, "Using running daemon at %s.\n", s)
			// The record beside the socket names the host, so the rest of the
			// session can pass it explicitly instead of relying on discovery.
			rec, _ := daemon.TargetForSocket(s)
			return s, rec.Target, false, nil
		}
		// Nothing listening: fall back to the target a daemon last ran for,
		// so a session that idled out can be resumed without retyping it.
		rec, terr := resolveTarget()
		if terr != nil {
			return "", "", false, fmt.Errorf("%w\nspecify a target instead: remote-agent claude user@host", terr)
		}
		opts.Target = rec.Target
		if opts.Port == 0 {
			opts.Port = rec.Port
		}
	}

	sockPath = daemon.SocketPath(opts.Target)
	if pingSocket(sockPath) {
		fmt.Fprintf(os.Stderr, "Reusing existing daemon for %s.\n", opts.Target)
		return sockPath, opts.Target, false, nil
	}

	logPath := filepath.Join(tmpDir(opts.ShimDir), "remote-agent-claude-daemon.log")
	fmt.Fprintf(os.Stderr, "Starting remote-agent daemon for %s (log: %s)...\n", opts.Target, logPath)
	proc, err := startDaemonFunc(self, opts.Target, opts.Port, logPath)
	if err != nil {
		return "", "", false, fmt.Errorf("start daemon: %w", err)
	}
	if err := awaitDaemon(sockPath, proc, logPath, daemonReadyWait); err != nil {
		return "", "", false, fmt.Errorf("daemon did not become ready: %w (see %s)", err, logPath)
	}
	fmt.Fprintf(os.Stderr, "Daemon ready.\n")
	return sockPath, opts.Target, true, nil
}

// startDaemonProcess launches `remote-agent connect` as a detached background
// process, sending its output to logPath.
func startDaemonProcess(self, target string, port int, logPath string) (*os.Process, error) {
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, err
	}
	defer logf.Close()

	cmd := exec.Command(self, "connect", target, "--port", strconv.Itoa(port))
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = daemonSysProcAttr()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}

// runClaudeProcess runs claude with inherited stdio, the given environment,
// and (when mounted) the mount point as its working directory -- so relative
// paths, project files and CLAUDE.md all come from the remote host.
func runClaudeProcess(bin string, args, env []string, dir string) error {
	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	cmd.Dir = dir
	return cmd.Run()
}

// mountForSession mounts the remote working directory for this launch and
// returns the local mount point and the remote directory it serves.
//
// The mount point defaults to the *same absolute path* as the remote
// directory. That is what makes the session coherent: file tools go through
// the mount locally and shell commands run on the remote, and both mean the
// same thing by "/srv/app/main.go". A different local path would leave the
// model juggling two path spaces for one set of files.
func mountForSession(sockPath string, opts LaunchOptions) (mountPoint, remoteDir string, err error) {
	remoteDir = opts.RemoteDir
	if remoteDir == "" {
		if remoteDir, err = remoteHomeDir(sockPath); err != nil {
			return "", "", err
		}
	}
	mountPoint = opts.MountAt
	if mountPoint == "" {
		mountPoint = remoteDir
	}

	err = callSocket(sockPath, "mount", map[string]any{
		"local_path":  mountPoint,
		"remote_path": remoteDir,
	}, nil)
	if err != nil {
		// The default is the remote home directory, which collides with a
		// local home of the same name (both /root, both /home/alice, ...).
		// Naming a project directory is the usual fix and keeps paths
		// identical on both sides, so it is the first suggestion.
		return "", "", fmt.Errorf("mount %s at %s: %w\n"+
			"Try --dir <remote project directory> (mounted at the same local path), "+
			"--mount-at <empty local directory> to mount somewhere else, "+
			"or --no-mount to fall back to remote-agent's own tools", remoteDir, mountPoint, err)
	}
	return mountPoint, remoteDir, nil
}

// unmountSession detaches the session's mount when claude exits.
func unmountSession(sockPath, mountPoint string) error {
	if err := callSocket(sockPath, "unmount", map[string]any{"local_path": mountPoint}, nil); err != nil {
		return fmt.Errorf("could not unmount %s: %w", mountPoint, err)
	}
	return nil
}

// remoteHomeDir asks the remote what its home directory is, which is where a
// session works unless told otherwise.
func remoteHomeDir(sockPath string) (string, error) {
	var result protocol.ExecResult
	if err := callSocket(sockPath, "exec", map[string]any{"command": "pwd"}, &result); err != nil {
		return "", fmt.Errorf("determine the remote working directory: %w", err)
	}
	dir := strings.TrimSpace(result.Stdout)
	if dir == "" {
		return "", fmt.Errorf("the remote returned no working directory; pass --dir to choose one")
	}
	return dir, nil
}

// callSocket sends one request to a specific daemon socket and decodes the
// payload, mirroring Call for callers that already know their socket.
func callSocket(sockPath, action string, params map[string]any, out any) error {
	resp, err := sendRequestTo(sockPath, &protocol.DaemonRequest{Action: action, Params: params})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	if out == nil {
		return nil
	}
	data, err := json.Marshal(resp.Data)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// waitForDaemon polls until the daemon at sockPath answers a ping or timeout.
func waitForDaemon(sockPath string, timeout time.Duration) error {
	return awaitDaemon(sockPath, nil, "", timeout)
}

// awaitDaemon waits for the daemon to answer. Given the process it was started
// as, it gives up the moment that process exits -- a bad host or a rejected key
// is reported in a second with the reason, instead of after the full timeout.
func awaitDaemon(sockPath string, proc *os.Process, logPath string, timeout time.Duration) error {
	exited := make(chan struct{})
	if proc != nil {
		go func() {
			proc.Wait()
			close(exited)
		}()
	}

	deadline := time.Now().Add(timeout)
	for {
		if pingSocket(sockPath) {
			return nil
		}
		select {
		case <-exited:
			return fmt.Errorf("daemon exited before it was ready%s", logTail(logPath))
		default:
		}
		if time.Now().After(deadline) {
			return errors.New("timeout waiting for daemon to accept connections")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// logTail returns the last few lines of the daemon log, formatted for appending
// to an error message. The daemon reports SSH failures there and nowhere else.
func logTail(logPath string) string {
	const maxLines = 6
	data, err := os.ReadFile(logPath)
	if err != nil || len(data) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return ":\n" + strings.Join(lines, "\n")
}

// pingSocket reports whether a healthy daemon is listening at sockPath.
func pingSocket(sockPath string) bool {
	resp, err := sendRequestTo(sockPath, &protocol.DaemonRequest{Action: "ping"})
	if err != nil {
		return false
	}
	if m, ok := resp.Data.(map[string]any); ok {
		pong, _ := m["pong"].(bool)
		return pong
	}
	return false
}

// disconnectSocket asks the daemon at sockPath to shut down.
func disconnectSocket(sockPath string) error {
	resp, err := sendRequestTo(sockPath, &protocol.DaemonRequest{Action: "disconnect"})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// remoteToolArgs returns the claude flags that swap the built-in local file
// tools for the remote MCP toolset.
//
// Both flags use the "--flag=value" form deliberately: claude declares
// --mcp-config and --disallowedTools as variadic options, so in the
// space-separated form they would swallow every following argument -- including
// the user's own prompt. The "=" form binds exactly one value.
func remoteToolArgs(configPath string) []string {
	return []string{
		"--mcp-config=" + configPath,
		"--disallowedTools=" + strings.Join(disabledLocalTools, ","),
		"--allowedTools=" + strings.Join(preAllowedRemoteTools, ","),
	}
}

// writeMCPConfig writes the MCP server definition that points claude at this
// binary's `mcp` subcommand, and returns its path. The server runs on the
// local machine (claude spawns it through the shell-prefix shim, which routes
// MCP servers locally) and reaches the remote host through the daemon socket.
//
// The target is baked into the argv, not just the socket: a socket is only a
// path, so once this launch's daemon idles out or dies the server needs the
// target to bring one back rather than failing every tool call.
func writeMCPConfig(dir, self, sockPath, target string) (string, error) {
	args := []string{"mcp"}
	env := map[string]string{
		"REMOTE_AGENT_BIN": self,
		// Pinned explicitly so the tools reach this launch's daemon even if the
		// environment is scrubbed or several daemons run.
		"REMOTE_AGENT_SOCKET": sockPath,
	}
	if target != "" {
		args = append(args, target)
		env["REMOTE_AGENT_TARGET"] = target
	}
	config := map[string]any{
		"mcpServers": map[string]any{
			mcpServerName: map[string]any{
				"type":    "stdio",
				"command": self,
				"args":    args,
				"env":     env,
			},
		},
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "remote-agent-claude-mcp.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	return path, nil
}

// writeShim writes the embedded shell-prefix shim into dir and returns its path.
func writeShim(dir string) (string, error) {
	path := filepath.Join(dir, "remote-agent-claude-shim.sh")
	if err := os.WriteFile(path, shellPrefixShim, 0700); err != nil {
		return "", err
	}
	return path, nil
}

// buildEnv returns base with the given keys replaced (or appended if absent).
func buildEnv(base []string, overrides map[string]string) []string {
	out := make([]string, 0, len(base)+len(overrides))
	for _, e := range base {
		key, _, _ := strings.Cut(e, "=")
		if _, ok := overrides[key]; ok {
			continue
		}
		out = append(out, e)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

func tmpDir(override string) string {
	if override != "" {
		return override
	}
	return os.TempDir()
}
