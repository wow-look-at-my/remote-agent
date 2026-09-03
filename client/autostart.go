package client

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/remote-agent/daemon"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// TargetOverride is the --target flag: it selects the socket, and names the
// target a fresh daemon starts for.
var TargetOverride string

// ControlPathOverride is the --control-path flag: the master a daemon this
// process starts must run through.
var ControlPathOverride string

// Socket of a daemon this process started. It beats discovery, but never a
// call that names its own target.
var resolvedSocket string

// A request-level failure never matches, so it never reconnects.
var errNoDaemon = errors.New("no daemon running")

var autoStartExempt = set.Of("disconnect", "ping")

// autoStartEnabled reports whether this process may start daemons.
func autoStartEnabled(action string) bool {
	if autoStartExempt.Contains(action) {
		return false
	}
	return os.Getenv("REMOTE_AGENT_NO_AUTOSTART") == ""
}

// resolveTarget determines which target a fresh daemon should be started for:
// the --target flag, then REMOTE_AGENT_TARGET, then the recorded target of a
// daemon that ran earlier.
func resolveTarget() (daemon.TargetRecord, error) {
	explicit := TargetOverride
	if explicit == "" {
		explicit = os.Getenv("REMOTE_AGENT_TARGET")
	}
	if explicit != "" {
		return recordFor(protocol.Route{Target: explicit})
	}

	recs := daemon.ListTargetRecords()
	switch len(recs) {
	case 0:
		return daemon.TargetRecord{}, errors.New("no target known: pass --target user@host[:port] or set REMOTE_AGENT_TARGET")
	case 1:
		return recordFor(protocol.Route{Target: recs[0].Target})
	default:
		names := make([]string, 0, len(recs))
		for _, r := range recs {
			names = append(names, r.Target)
		}
		sort.Strings(names)
		return daemon.TargetRecord{}, fmt.Errorf("several targets known (%v): pass --target user@host[:port] or set REMOTE_AGENT_TARGET", names)
	}
}

// TargetKey folds REMOTE_AGENT_PORT into a target. see
// docs/daemon/lifecycle.md
func TargetKey(target string) (string, error) {
	return targetKey(target, 0)
}

// targetKey folds a port given separately, then REMOTE_AGENT_PORT, into a
// target. An empty target stays empty: it names no endpoint to key on.
func targetKey(target string, port int) (string, error) {
	if target == "" {
		return "", nil
	}
	if port == 0 {
		if p, ok := portFromEnv(); ok {
			port = p
		}
	}
	return daemon.CanonicalTarget(target, port)
}

// DefaultTarget reports the target this process was pointed at: --target,
// REMOTE_AGENT_TARGET, or the record for a pinned socket.
func DefaultTarget() (string, error) {
	if TargetOverride != "" {
		return TargetKey(TargetOverride)
	}
	if t := os.Getenv("REMOTE_AGENT_TARGET"); t != "" {
		return TargetKey(t)
	}
	if sock := os.Getenv("REMOTE_AGENT_SOCKET"); sock != "" {
		if rec, err := daemon.TargetForSocket(sock); err == nil {
			return rec.Target, nil
		}
	}
	return "", nil
}

// recordFor describes the daemon to start for a route. The record is named
// from the canonical target, so the socket it waits on is the socket the
// daemon opens. A record for that target carries the control master it rode,
// if any; the route's own control path wins.
func recordFor(route protocol.Route) (daemon.TargetRecord, error) {
	key, err := targetKey(route.Target, 0)
	if err != nil {
		return daemon.TargetRecord{}, err
	}
	prev, prevErr := daemon.ReadTargetRecord(daemon.TargetPath(key))
	ep, err := daemon.ParseTarget(key)
	if err != nil {
		return daemon.TargetRecord{}, err
	}
	// An older record keeps its port in a field of its own.
	if ep.Port == 0 && prevErr == nil && prev.Port > 0 {
		if key, err = daemon.CanonicalTarget(key, prev.Port); err != nil {
			return daemon.TargetRecord{}, err
		}
		ep.Port = prev.Port
	}

	rec := daemon.TargetRecord{Target: key, Port: ep.Port}
	if prevErr == nil {
		rec.ControlPath = prev.ControlPath
	}
	if cp := ControlPathFor(route); cp != "" {
		rec.ControlPath = cp
	}
	return rec, nil
}

// ControlPathFor resolves a route's control master: the call's own, else
// --control-path, else REMOTE_AGENT_CONTROL_PATH. Empty leaves it to
// ssh_config, where it is optional.
func ControlPathFor(route protocol.Route) string {
	if route.ControlPath != "" {
		return route.ControlPath
	}
	if ControlPathOverride != "" {
		return ControlPathOverride
	}
	return os.Getenv("REMOTE_AGENT_CONTROL_PATH")
}

// checkControlPath refuses to run a call through a daemon that is not using
// the control master the caller asked for.
func checkControlPath(route protocol.Route) error {
	want := ControlPathFor(route)
	if want == "" {
		return nil
	}
	prev, err := daemon.ReadTargetRecord(daemon.TargetPath(route.Target))
	if err != nil || prev.ControlPath == want || !socketAnswers(daemon.SocketPath(route.Target)) {
		return nil
	}
	via := "no control master"
	if prev.ControlPath != "" {
		via = "control master " + prev.ControlPath
	}
	return fmt.Errorf("a daemon for %s is already running through %s, not %s; stop it first with `remote-agent disconnect --target %s`",
		route.Target, via, want, route.Target)
}

// Only connects: a request runs a remote command, which is far too much for a
// liveness check.
func socketAnswers(sockPath string) bool {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

var startMu sync.Mutex

// autoStartDaemon starts a daemon for rec in the background and waits for it
// to accept connections, returning its socket path. It is what makes
// `connect` an optimization rather than a prerequisite.
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
		return sockPath, nil
	}

	fmt.Fprintf(os.Stderr, "No daemon for %s; starting one (log: %s)...\n", rec.Target, logPath)
	proc, err := startDaemonFunc(self, rec, logPath)
	if err != nil {
		return "", fmt.Errorf("start daemon for %s: %w", rec.Target, err)
	}
	if err := awaitDaemon(sockPath, proc, logPath, daemonReadyWait); err != nil {
		return "", fmt.Errorf("daemon for %s did not become ready: %w (see %s)", rec.Target, err, logPath)
	}
	return sockPath, nil
}

// portFromEnv reads REMOTE_AGENT_PORT. A ~/.ssh/config alias carries its own
// port, resolved when the daemon connects, so this is only for a bare host
// name.
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
