package client

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	// see docs/claude/launcher.md for what these four change about a session
	RemoteDir   string // remote directory to work in; empty means the remote home
	MountAt     string // local mount point; empty mounts at the identical path
	NoMount     bool   // serve the remote filesystem as MCP tools instead of mounting
	LocalTools  bool   // with NoMount, keep Claude's own file tools; only Bash runs remotely
	ControlPath string // OpenSSH control master the session's daemon must run through
}

// Built-in tools that read local disk directly. Every name here must exist, or claude warns at startup.
var disabledLocalTools = []string{"Read", "Write", "Edit", "NotebookEdit", "Glob", "Grep"}

// Claude prefixes this onto every remote tool: mcp__remote__read_file.
const mcpServerName = "remote"

// Read-only tools, allowed up front because the built-ins they replace never prompted.
// The mutating tools are absent on purpose. see docs/claude/launcher.md
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
		// Lets a forwarded command restart the daemon after an idle-out.
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
			// Both sides agree on the path, so the shim can cd into claude's directory.
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
			// The record names the host, so the session passes it instead of discovering it.
			rec, _ := daemon.TargetForSocket(s)
			return s, rec.Target, false, nil
		}
		// Nothing listening: resume the target a daemon last ran for.
		rec, terr := resolveTarget()
		if terr != nil {
			return "", "", false, fmt.Errorf("%w\nspecify a target instead: remote-agent claude user@host", terr)
		}
		opts.Target = rec.Target
	}

	// The port is part of the target the daemon is keyed on, so --port folds
	// into it here rather than travelling beside it.
	if opts.Target, err = targetKey(opts.Target, opts.Port); err != nil {
		return "", "", false, err
	}

	sockPath = daemon.SocketPath(opts.Target)
	if pingSocket(sockPath) {
		fmt.Fprintf(os.Stderr, "Reusing existing daemon for %s.\n", opts.Target)
		return sockPath, opts.Target, false, nil
	}

	rec, err := recordFor(protocol.Route{Target: opts.Target, ControlPath: opts.ControlPath})
	if err != nil {
		return "", "", false, err
	}
	// Wait on the socket the recorded target opens, not the one asked for.
	sockPath = daemon.SocketPath(rec.Target)

	logPath := filepath.Join(tmpDir(opts.ShimDir), "remote-agent-claude-daemon.log")
	fmt.Fprintf(os.Stderr, "Starting remote-agent daemon for %s (log: %s)...\n", rec.Target, logPath)
	proc, err := startDaemonFunc(self, rec, logPath)
	if err != nil {
		return "", "", false, fmt.Errorf("start daemon: %w", err)
	}
	if err := awaitDaemon(sockPath, proc, logPath, daemonReadyWait); err != nil {
		return "", "", false, fmt.Errorf("daemon did not become ready: %w (see %s)", err, logPath)
	}
	fmt.Fprintf(os.Stderr, "Daemon ready.\n")
	return sockPath, rec.Target, true, nil
}

// remoteToolArgs swaps the local file tools for the remote MCP toolset. Both flags
// must keep the "--flag=value" form: claude declares them variadic, and the
// space-separated form swallows the user's own prompt. see docs/claude/launcher.md
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
		// Pinned, so the tools reach this launch's daemon and not another one.
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
