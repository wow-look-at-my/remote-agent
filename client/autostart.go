package client

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/remote-agent/daemon"
)

// TargetOverride, when set, names the SSH target commands act on (the --target
// flag). It selects the daemon socket and, when no daemon is running, is the
// target a fresh one is started for.
var TargetOverride string

// resolvedSocket is the socket of a daemon this process started; it wins over
// discovery for the remainder of the process.
var resolvedSocket string

// errNoDaemon marks the failures auto-start can repair: no socket at all, or a
// socket nothing is listening on. Request-level failures do not match it, so a
// broken command never causes a reconnect.
var errNoDaemon = errors.New("no daemon running")

// autoStartExempt are actions that must not bring a daemon up. Connecting in
// order to disconnect is absurd, and ping is how callers ask whether a daemon
// is there at all.
var autoStartExempt = map[string]bool{"disconnect": true, "ping": true}

// autoStartEnabled reports whether this process may start daemons.
// REMOTE_AGENT_NO_AUTOSTART=1 turns it off.
func autoStartEnabled(action string) bool {
	if autoStartExempt[action] {
		return false
	}
	return os.Getenv("REMOTE_AGENT_NO_AUTOSTART") == ""
}

// resolveTarget determines which target a fresh daemon should be started for:
// the --target flag, then REMOTE_AGENT_TARGET, then the recorded target of a
// daemon that ran earlier. Several remembered targets are ambiguous, so the
// caller is told to pick one rather than being connected somewhere arbitrary.
func resolveTarget() (daemon.TargetRecord, error) {
	explicit := TargetOverride
	if explicit == "" {
		explicit = os.Getenv("REMOTE_AGENT_TARGET")
	}
	if explicit != "" {
		rec := daemon.TargetRecord{Target: explicit, Port: defaultPort}
		// A record for this exact target carries the port it was reached on;
		// REMOTE_AGENT_PORT overrides both.
		if prev, err := daemon.ReadTargetRecord(daemon.TargetPath(explicit)); err == nil && prev.Port != 0 {
			rec.Port = prev.Port
		}
		if p, ok := portFromEnv(); ok {
			rec.Port = p
		}
		return rec, nil
	}

	recs := daemon.ListTargetRecords()
	switch len(recs) {
	case 0:
		return daemon.TargetRecord{}, errors.New("no target known: pass --target user@host or set REMOTE_AGENT_TARGET")
	case 1:
		rec := recs[0]
		if rec.Port == 0 {
			rec.Port = defaultPort
		}
		return rec, nil
	default:
		names := make([]string, 0, len(recs))
		for _, r := range recs {
			names = append(names, r.Target)
		}
		sort.Strings(names)
		return daemon.TargetRecord{}, fmt.Errorf("several targets known (%v): pass --target user@host or set REMOTE_AGENT_TARGET", names)
	}
}

const defaultPort = 22

// autoStartDaemon starts a daemon for rec in the background and waits for it to
// accept connections, returning its socket path. It is what makes `connect` an
// optimization rather than a prerequisite.
func autoStartDaemon(rec daemon.TargetRecord) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate remote-agent binary: %w", err)
	}
	sockPath := daemon.SocketPath(rec.Target)
	logPath := strings.TrimSuffix(sockPath, ".sock") + ".log"

	fmt.Fprintf(os.Stderr, "No daemon for %s; starting one (log: %s)...\n", rec.Target, logPath)
	proc, err := startDaemonFunc(self, rec.Target, rec.Port, logPath)
	if err != nil {
		return "", fmt.Errorf("start daemon for %s: %w", rec.Target, err)
	}
	if err := awaitDaemon(sockPath, proc, logPath, daemonReadyWait); err != nil {
		return "", fmt.Errorf("daemon for %s did not become ready: %w (see %s)", rec.Target, err, logPath)
	}
	// Pin it for the rest of this process, so a long-lived client (the MCP
	// server) does not re-resolve a stale socket path on every later call.
	resolvedSocket = sockPath
	return sockPath, nil
}

// portFromEnv reads an SSH port from REMOTE_AGENT_PORT, reporting whether one
// was set. Host aliases in ~/.ssh/config carry their own port, resolved when
// the daemon connects, so this is only for targets given as bare host names.
func portFromEnv() (int, bool) {
	s := os.Getenv("REMOTE_AGENT_PORT")
	if s == "" {
		return 0, false
	}
	p, err := strconv.Atoi(s)
	if err != nil || p <= 0 {
		return 0, false
	}
	return p, true
}
