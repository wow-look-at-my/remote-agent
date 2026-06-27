package client

import (
	_ "embed"
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

	sockPath, startedDaemon, err := ensureDaemon(self, opts)
	if err != nil {
		return err
	}

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

	env := buildEnv(os.Environ(), map[string]string{
		"CLAUDE_CODE_SHELL_PREFIX": shimPath,
		"REMOTE_AGENT_BIN":         self,
		"REMOTE_AGENT_SOCKET":      sockPath,
	})

	fmt.Fprintf(os.Stderr, "Launching claude; Bash commands will run on the remote host.\n")
	runErr := runClaudeFunc(resolved, opts.ClaudeArgs, env)

	if startedDaemon && !opts.KeepDaemon {
		fmt.Fprintf(os.Stderr, "Stopping remote-agent daemon...\n")
		if err := disconnectSocket(sockPath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to stop daemon: %v\n", err)
		}
	}
	return runErr
}

// ensureDaemon resolves the daemon socket for the launch, starting a new daemon
// if a target is given and none is already running. It reports whether this call
// started the daemon (so the caller knows whether to tear it down).
func ensureDaemon(self string, opts LaunchOptions) (sockPath string, started bool, err error) {
	if opts.Target == "" {
		// No target: reuse the single running daemon, if exactly one exists.
		s, ferr := findSocket()
		if ferr != nil {
			return "", false, fmt.Errorf("%w\nspecify a target instead: remote-agent claude user@host", ferr)
		}
		fmt.Fprintf(os.Stderr, "Using running daemon at %s.\n", s)
		return s, false, nil
	}

	sockPath = daemon.SocketPath(opts.Target)
	if pingSocket(sockPath) {
		fmt.Fprintf(os.Stderr, "Reusing existing daemon for %s.\n", opts.Target)
		return sockPath, false, nil
	}

	logPath := filepath.Join(tmpDir(opts.ShimDir), "remote-agent-claude-daemon.log")
	fmt.Fprintf(os.Stderr, "Starting remote-agent daemon for %s (log: %s)...\n", opts.Target, logPath)
	if _, err := startDaemonFunc(self, opts.Target, opts.Port, logPath); err != nil {
		return "", false, fmt.Errorf("start daemon: %w", err)
	}
	if err := waitForDaemon(sockPath, daemonReadyWait); err != nil {
		return "", false, fmt.Errorf("daemon did not become ready: %w (see %s)", err, logPath)
	}
	fmt.Fprintf(os.Stderr, "Daemon ready.\n")
	return sockPath, true, nil
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

// runClaudeProcess runs claude with inherited stdio and the given environment.
func runClaudeProcess(bin string, args, env []string) error {
	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	return cmd.Run()
}

// waitForDaemon polls until the daemon at sockPath answers a ping or timeout.
func waitForDaemon(sockPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if pingSocket(sockPath) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timeout waiting for daemon to accept connections")
		}
		time.Sleep(200 * time.Millisecond)
	}
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
