package client

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/wow-look-at-my/remote-agent/daemon"
)

// TargetOverride, when set, names the SSH target commands act on (the --target
// flag). It selects the daemon socket and, when no daemon is running, is the
// target a fresh one is started for.
var TargetOverride string

// resolvedSocket is the socket of a daemon this process started for the
// process-wide target; it wins over discovery for the remainder of the process.
// Calls that name their own target never consult it -- see sendRequestFor.
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
		return recordFor(explicit), nil
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

// DefaultTarget reports the target this process was pointed at, if any: the
// --target flag, REMOTE_AGENT_TARGET, or the target recorded for a pinned
// REMOTE_AGENT_SOCKET. Callers that route per request (the MCP server) use it
// as the fallback for requests that name no target of their own; an empty
// result means the request has to carry one.
func DefaultTarget() string {
	if TargetOverride != "" {
		return TargetOverride
	}
	if t := os.Getenv("REMOTE_AGENT_TARGET"); t != "" {
		return t
	}
	if sock := os.Getenv("REMOTE_AGENT_SOCKET"); sock != "" {
		if rec, err := daemon.TargetForSocket(sock); err == nil {
			return rec.Target
		}
	}
	return ""
}

// recordFor describes the daemon to start for a named target. A record for this
// exact target carries the port it was last reached on; REMOTE_AGENT_PORT
// overrides both.
func recordFor(target string) daemon.TargetRecord {
	rec := daemon.TargetRecord{Target: target, Port: defaultPort}
	if prev, err := daemon.ReadTargetRecord(daemon.TargetPath(target)); err == nil && prev.Port != 0 {
		rec.Port = prev.Port
	}
	if p, ok := portFromEnv(); ok {
		rec.Port = p
	}
	return rec
}

// startMu serializes auto-starts within this process. One MCP server can be
// asked for several targets at once, and two `connect` processes racing for the
// same socket leaves one of them dead on "address already in use".
var startMu sync.Mutex

// autoStartDaemon starts a daemon for rec in the background and waits for it to
// accept connections, returning its socket path. It is what makes `connect` an
// optimization rather than a prerequisite.
func autoStartDaemon(rec daemon.TargetRecord) (string, error) {
	startMu.Lock()
	defer startMu.Unlock()

	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate remote-agent binary: %w", err)
	}
	sockPath := daemon.SocketPath(rec.Target)
	logPath := strings.TrimSuffix(sockPath, ".sock") + ".log"
	if pingSocket(sockPath) {
		// Another caller started it while this one waited for the lock.
		return sockPath, nil
	}

	fmt.Fprintf(os.Stderr, "No daemon for %s; starting one (log: %s)...\n", rec.Target, logPath)
	proc, err := startDaemonFunc(self, rec.Target, rec.Port, logPath)
	if err != nil {
		return "", fmt.Errorf("start daemon for %s: %w", rec.Target, err)
	}
	if err := awaitDaemon(sockPath, proc, logPath, daemonReadyWait); err != nil {
		return "", fmt.Errorf("daemon for %s did not become ready: %w (see %s)", rec.Target, err, logPath)
	}
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
