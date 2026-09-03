package client

import (
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

// Supervising the daemon a command runs on: starting one, waiting for it to
// answer, and asking it to stop.

// Test seams, and how long a fresh daemon has to answer before a command gives up.
var (
	startDaemonFunc = startDaemonProcess
	daemonReadyWait = 30 * time.Second
)

// startDaemonProcess launches `remote-agent connect` as a detached background
// process, sending its output to logPath.
func startDaemonProcess(self string, rec daemon.TargetRecord, logPath string) (*os.Process, error) {
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, err
	}
	defer logf.Close()

	// The target carries its port, so --port goes only where the record names one.
	args := []string{"connect", rec.Target}
	if rec.Port > 0 {
		args = append(args, "--port", strconv.Itoa(rec.Port))
	}
	if rec.ControlPath != "" {
		args = append(args, "--control-path", rec.ControlPath)
	}
	// Through a shell: a release is an APE, which os/exec cannot execve. see docs/ape.md
	name, argv := SelfCommand(self, args...)
	cmd := exec.Command(name, argv...)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = daemonSysProcAttr()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
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
