package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/wow-look-at-my/remote-agent/daemon"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// Supervising the daemon process a session runs on: starting one, waiting for
// it to answer, and asking it to stop. Kept apart from the claude launcher in
// launch.go, which is about wiring claude up rather than about the daemon.

// startDaemonProcess launches `remote-agent connect` as a detached background
// process, sending its output to logPath.
func startDaemonProcess(self string, rec daemon.TargetRecord, logPath string) (*os.Process, error) {
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, err
	}
	defer logf.Close()

	// The target carries its own port, so --port is passed only when the record
	// names one. A port the daemon resolves from ssh_config must not be passed
	// back in: it would change the target, and with it the socket the caller
	// is waiting on.
	args := []string{"connect", rec.Target}
	if rec.Port > 0 {
		args = append(args, "--port", strconv.Itoa(rec.Port))
	}
	if rec.ControlPath != "" {
		args = append(args, "--control-path", rec.ControlPath)
	}
	cmd := exec.Command(self, args...)
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
