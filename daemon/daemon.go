package daemon

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wow-look-at-my/remote-agent/protocol"
	"github.com/wow-look-at-my/remote-agent/sshutil"
)

// Idle-timeout defaults. A daemon carries its own, so the watchdog reads only
// state its own daemon owns.
const (
	defaultIdleTimeout       = 30 * time.Minute
	defaultIdleCheckInterval = 60 * time.Second
)

// Daemon holds a persistent SSH connection and serves requests over a Unix
// socket.
type Daemon struct {
	conn         *sshutil.ConnResult
	runner       Runner // abstraction over SSH command execution
	remotePath   string // path to remote helper binary
	sockPath     string
	pidPath      string
	listener     net.Listener
	keepBinary   bool           // helper lives in the remote cache dir; leave it for the next connect
	mu           sync.Mutex     // guards lastActivity and activeOps
	lastActivity time.Time      // time of the last client request start/finish (guarded by mu)
	activeOps    int            // number of requests currently being handled (guarded by mu)
	auditWG      sync.WaitGroup // tracks in-flight async audit writes so shutdown can drain them
	mounts       mountRegistry  // filesystem mounts this daemon owns
	streamFunc   streamStarter  // test seam for opening a mount's transport
	// Idle tuning and process exit.
	idleTimeout       time.Duration
	idleCheckInterval time.Duration
	exit              func(int)
}

// idleTuning is the watchdog's timeout and tick, defaulted.
func (d *Daemon) idleTuning() (timeout, interval time.Duration) {
	timeout, interval = d.idleTimeout, d.idleCheckInterval
	if timeout == 0 {
		timeout = defaultIdleTimeout
	}
	if interval == 0 {
		interval = defaultIdleCheckInterval
	}
	return timeout, interval
}

// exitProcess ends the process, or calls what a test installed in its place.
func (d *Daemon) exitProcess(code int) {
	if d.exit != nil {
		d.exit(code)
		return
	}
	os.Exit(code)
}

// opStart records that a request began. The watchdog never fires
// mid-operation.
func (d *Daemon) opStart() {
	d.mu.Lock()
	d.activeOps++
	d.lastActivity = time.Now()
	d.mu.Unlock()
}

// opEnd records that a request finished; the idle countdown starts here, not
// at its start.
func (d *Daemon) opEnd() {
	d.mu.Lock()
	d.activeOps--
	d.lastActivity = time.Now()
	d.mu.Unlock()
}

// helper is the command word for the deployed helper.
func (d *Daemon) helper() string {
	return shellEscape(d.remotePath)
}

// auditAsync audits on its own SSH channel, so the operation does not wait
// for it. Failures are ignored; shutdown drains what is in flight. see
// docs/daemon/lifecycle.md
func (d *Daemon) auditAsync(action, detail string) {
	cmd := fmt.Sprintf("%s serve audit --action %s --detail %s",
		d.helper(), shellEscape(action), shellEscape(detail))
	d.auditWG.Add(1)
	go func() {
		defer d.auditWG.Done()
		d.runner.Run(cmd)
	}()
}

func SocketPath(target string) string {
	h := sha256.Sum256([]byte(normalizeTarget(target)))
	return filepath.Join(os.TempDir(), fmt.Sprintf("remote-agent-%x.sock", h[:6]))
}

// PIDPath returns the PID file path for a given target.
func PIDPath(target string) string {
	h := sha256.Sum256([]byte(normalizeTarget(target)))
	return filepath.Join(os.TempDir(), fmt.Sprintf("remote-agent-%x.pid", h[:6]))
}

// StartOptions configures a daemon.
type StartOptions struct {
	Target string // [user@]host[:port], or a ~/.ssh/config Host alias
	// Port joins the identity like a port in the target; disagreeing ports
	// error.
	Port int
	// ControlPath makes that master mandatory.
	ControlPath string
}

// Start connects to the remote, deploys the helper binary, and starts the
// daemon.
func Start(opts StartOptions) error {
	// The port keys the daemon, whether it came in the target or beside it.
	target, err := CanonicalTarget(opts.Target, opts.Port)
	if err != nil {
		return err
	}
	ep, err := ParseTarget(target)
	if err != nil {
		return err
	}
	user, host, port := ep.Login(), ep.Host, ep.Port

	// A daemon already answering for this target is reused, not rebuilt.
	sockPath := SocketPath(target)
	if pingSocket(sockPath) {
		fmt.Fprintf(os.Stderr, "Already connected to %s\n", target)
		return nil
	}

	// ssh -G fills in only what the caller left out. see
	// docs/daemon/lifecycle.md
	controlPath, requireControl := opts.ControlPath, opts.ControlPath != ""
	if cfg := sshutil.ResolveSSHConfig(ep.User, host, port); cfg != nil {
		if cfg.HostName != "" {
			host = cfg.HostName
		}
		if cfg.User != "" && ep.User == "" { // only when user wasn't explicitly given
			user = cfg.User
		}
		if cfg.Port != 0 && port == 0 { // only when the target names no port
			port = cfg.Port
		}
		if controlPath == "" {
			controlPath = cfg.ControlPath
		}
	}
	if port == 0 {
		port = defaultSSHPort
	}

	fmt.Fprintf(os.Stderr, "Connecting to %s@%s...\n", user, net.JoinHostPort(host, strconv.Itoa(port)))

	conn, err := sshutil.Connect(sshutil.ConnectOptions{
		Host:           host,
		Port:           port,
		User:           user,
		ControlPath:    controlPath,
		RequireControl: requireControl,
	})
	if err != nil {
		return fmt.Errorf("ssh connect: %w", err)
	}

	if conn.ControlPath != "" {
		fmt.Fprintf(os.Stderr, "Connected through control master at %s (it owns the connection and checked the host key).\n", conn.ControlPath)
	} else {
		fmt.Fprintf(os.Stderr, "Connected. Key fingerprint: %s\n", conn.Fingerprint)
	}

	runner := conn.Conn

	// Deploy helper binary (or reuse the cached copy from a previous connect)
	remotePath, cached, err := deployBinary(runner)
	if err != nil {
		conn.Conn.Close()
		return fmt.Errorf("deploy: %w", err)
	}

	if cached {
		fmt.Fprintf(os.Stderr, "Reusing cached helper at %s\n", remotePath)
	} else {
		fmt.Fprintf(os.Stderr, "Deployed helper to %s\n", remotePath)
	}

	// Through a master there is no key of ours, so the entry names the master
	// instead.
	auditCmd := fmt.Sprintf("%s serve audit --action startup --user %s --client-ip %s --fingerprint %s --detail %s",
		shellEscape(remotePath), shellEscape(user), shellEscape(conn.Host), shellEscape(conn.Fingerprint), shellEscape(connectionDetail(conn)))
	runner.Run(auditCmd)

	d := &Daemon{
		mounts:       mountRegistry{mounts: map[string]*mountEntry{}},
		conn:         conn,
		runner:       runner,
		remotePath:   remotePath,
		keepBinary:   cachedDeploy(remotePath),
		sockPath:     sockPath,
		pidPath:      PIDPath(target),
		lastActivity: time.Now(),
	}

	// Clean up any stale socket
	os.Remove(d.sockPath)

	// Start Unix socket listener
	d.listener, err = net.Listen("unix", d.sockPath)
	if err != nil {
		d.cleanup()
		return fmt.Errorf("listen on %s: %w", d.sockPath, err)
	}

	// Write PID file
	os.WriteFile(d.pidPath, []byte(strconv.Itoa(os.Getpid())), 0644)

	// Kept on shutdown, and Port holds no ssh_config port.
	WriteTargetRecord(TargetRecord{Target: target, Port: ep.Port, ControlPath: conn.ControlPath})

	fmt.Fprintf(os.Stderr, "Daemon listening on %s\n", d.sockPath)
	fmt.Fprintf(os.Stderr, "Ready.\n")

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		fmt.Fprintf(os.Stderr, "\nShutting down...\n")
		d.shutdown()
		os.Exit(0)
	}()

	// Shut down automatically after a period of inactivity.
	go d.watchIdle()

	// Accept connections
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			// Listener closed
			return nil
		}
		go d.handleClient(conn)
	}
}

// pingSocket reports whether a live daemon is already listening at sockPath.
// It dials the Unix socket (2s timeout), sends a ping request using the same
// JSON framing as the client, and returns true if any valid DaemonResponse
// decodes. A read deadline ensures a present-but-dead socket cannot hang the
// caller.
func pingSocket(sockPath string) bool {
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(2 * time.Second))

	if err := json.NewEncoder(conn).Encode(protocol.DaemonRequest{Action: "ping"}); err != nil {
		return false
	}

	var resp protocol.DaemonResponse
	return json.NewDecoder(conn).Decode(&resp) == nil
}

func (d *Daemon) handleClient(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	var req protocol.DaemonRequest
	if err := decoder.Decode(&req); err != nil {
		encoder.Encode(protocol.DaemonResponse{Error: "invalid request: " + err.Error()})
		return
	}

	// Held so the watchdog neither fires mid-operation nor counts the command as
	d.opStart()
	defer d.opEnd()

	handler := &Handler{daemon: d}
	resp := handler.Handle(&req)
	encoder.Encode(resp)
}

func (d *Daemon) watchIdle() {
	timeout, interval := d.idleTuning()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		d.mu.Lock()
		idle := time.Since(d.lastActivity)
		busy := d.activeOps > 0
		d.mu.Unlock()
		// A live mount holds the daemon open: dropping it breaks every open file
		// under it.
		if !busy && !d.hasMounts() && idle > timeout {
			fmt.Fprintf(os.Stderr, "\nIdle for %s; shutting down...\n", idle.Round(time.Second))
			d.shutdown()
			d.exitProcess(0)
			return
		}
	}
}

func (d *Daemon) shutdown() {
	d.unmountAll()

	// Drain audits so none are lost and the shutdown entry stays last.
	d.auditWG.Wait()

	if d.runner != nil {
		auditCmd := fmt.Sprintf("%s serve audit --action shutdown", d.helper())
		d.runner.Run(auditCmd)

		// A cached helper stays for the next connect to reuse.
		if !d.keepBinary {
			d.runner.Run(fmt.Sprintf("rm -f %s", d.helper()))
		}
	}

	d.cleanup()
}

func (d *Daemon) cleanup() {
	// see docs/daemon/lifecycle.md
	os.Remove(d.sockPath)
	os.Remove(d.pidPath)
	if d.listener != nil {
		d.listener.Close()
	}
	if d.conn != nil {
		d.conn.Conn.Close()
	}
}

// connectionDetail describes how the daemon reached the host, for the audit
// log: which control master it rode, or that it dialed the host itself.
func connectionDetail(conn *sshutil.ConnResult) string {
	if conn.ControlPath != "" {
		return "connected via control master " + conn.ControlPath
	}
	return "connected directly over ssh"
}

// deployBinary ships the helper to the remote. reused means a cached copy was
// already there.
func deployBinary(runner Runner) (remotePath string, reused bool, err error) {
	localBinary, err := findDeployBinary()
	if err != nil {
		return "", false, err
	}

	data, err := os.ReadFile(localBinary)
	if err != nil {
		return "", false, fmt.Errorf("read binary %s: %w", localBinary, err)
	}

	return deployBinaryData(runner, data)
}

// deployBinaryData uploads the helper, content-addressed. see
// docs/daemon/lifecycle.md
func deployBinaryData(runner Runner, data []byte) (remotePath string, reused bool, err error) {
	sum := sha256.Sum256(data)

	var remoteDir string
	if home, _, exitCode, err := runner.Run(`printf %s "$HOME"`); err == nil && exitCode == 0 &&
		len(home) > 0 && home[0] == '/' {
		remoteDir = fmt.Sprintf("%s/.cache/remote-agent", strings.TrimSpace(string(home)))
		remotePath = fmt.Sprintf("%s/agent-%x", remoteDir, sum[:8])

		// Reuse a cached binary whose content hash matches ours.
		checkCmd := fmt.Sprintf("sha256sum %s 2>/dev/null", shellEscape(remotePath))
		if stdout, _, code, err := runner.Run(checkCmd); err == nil && code == 0 &&
			strings.HasPrefix(strings.TrimSpace(string(stdout)), fmt.Sprintf("%x", sum)) {
			return remotePath, true, nil
		}
	} else {
		// No usable $HOME: a random /tmp path, removed again on disconnect.
		remotePath = fmt.Sprintf("/tmp/.remote-agent-%s", randomSuffix())
	}

	tmpPath := fmt.Sprintf("%s.tmp-%s", remotePath, randomSuffix())
	cmd := fmt.Sprintf("cat > %s && chmod 700 %s && mv -f %s %s",
		shellEscape(tmpPath), shellEscape(tmpPath), shellEscape(tmpPath), shellEscape(remotePath))
	if remoteDir != "" {
		cmd = fmt.Sprintf("mkdir -p %s && %s", shellEscape(remoteDir), cmd)
	}
	_, stderr, exitCode, err := runner.RunStdin(cmd, data)
	if err != nil {
		return "", false, fmt.Errorf("copy binary: %w", err)
	}
	if exitCode != 0 {
		return "", false, fmt.Errorf("copy binary failed (exit %d): %s", exitCode, string(stderr))
	}

	return remotePath, false, nil
}

// A helper in the persistent cache, not on a throwaway path, survives
// disconnect.
func cachedDeploy(remotePath string) bool {
	return strings.Contains(remotePath, "/.cache/remote-agent/")
}

func findDeployBinary() (string, error) {
	// If we're already linux/amd64, use ourselves
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get executable path: %w", err)
	}

	// Check if there's a linux-amd64 variant next to us
	dir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(dir, "remote-agent-linux-amd64"),
		filepath.Join(dir, "dist", "remote-agent-linux-amd64"),
		exe, // fallback: use ourselves (works if local is linux/amd64)
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}

	return "", fmt.Errorf("no suitable binary found for linux/amd64; build with: make build-linux")
}

func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func randomSuffix() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = chars[rand.IntN(len(chars))]
	}
	return string(b)
}
