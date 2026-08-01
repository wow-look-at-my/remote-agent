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

// Idle-timeout tuning. Overridable in tests.
var (
	idleTimeout       = 30 * time.Minute
	idleCheckInterval = 60 * time.Second
)

// Daemon holds a persistent SSH connection and serves requests over a Unix socket.
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
}

// opStart records that a client request began handling. The idle watchdog
// never shuts the daemon down while operations are in flight, no matter how
// long they run.
func (d *Daemon) opStart() {
	d.mu.Lock()
	d.activeOps++
	d.lastActivity = time.Now()
	d.mu.Unlock()
}

// opEnd records that a client request finished. It also refreshes
// lastActivity so the idle countdown starts at completion, not at the start
// of a long-running command.
func (d *Daemon) opEnd() {
	d.mu.Lock()
	d.activeOps--
	d.lastActivity = time.Now()
	d.mu.Unlock()
}

// auditAsync writes an audit entry on the remote without blocking the
// operation it describes: the audit command runs on its own SSH channel,
// concurrently with the operation. This halves the network round trips per
// operation compared to running the audit serially first. shutdown drains
// in-flight audits via auditWG, so a graceful shutdown never loses entries.
// Audit failures are ignored, exactly as they were when the call was serial.
func (d *Daemon) auditAsync(action, detail string) {
	cmd := fmt.Sprintf("%s serve audit --action %s --detail %s",
		d.remotePath, shellEscape(action), shellEscape(detail))
	d.auditWG.Add(1)
	go func() {
		defer d.auditWG.Done()
		d.runner.Run(cmd)
	}()
}

// SocketPath returns the Unix socket path for a given host.
func SocketPath(target string) string {
	h := sha256.Sum256([]byte(target))
	return filepath.Join(os.TempDir(), fmt.Sprintf("remote-agent-%x.sock", h[:6]))
}

// PIDPath returns the PID file path for a given host.
func PIDPath(target string) string {
	h := sha256.Sum256([]byte(target))
	return filepath.Join(os.TempDir(), fmt.Sprintf("remote-agent-%x.pid", h[:6]))
}

// Start connects to the remote, deploys the helper binary, and starts the daemon.
func Start(target string, port int) error {
	// Parse user@host
	user, host, err := parseTarget(target)
	if err != nil {
		return err
	}

	// Auto-connect: if a daemon for this target is already up and answering,
	// reuse it rather than dialing/deploying a second time.
	sockPath := SocketPath(target)
	if pingSocket(sockPath) {
		fmt.Fprintf(os.Stderr, "Already connected to %s\n", target)
		return nil
	}

	// Resolve any ~/.ssh/config Host alias to its real hostname/user/port via
	// `ssh -G`, so a bare alias like "myserver" connects to the configured host.
	// Only fill in values the caller did not supply explicitly.
	if cfg := sshutil.ResolveSSHConfig(host); cfg != nil {
		if cfg.HostName != "" {
			host = cfg.HostName
		}
		if cfg.User != "" && !strings.Contains(target, "@") { // only when user wasn't explicitly given
			user = cfg.User
		}
		if cfg.Port != 0 && port == 22 { // only override the default port
			port = cfg.Port
		}
	}

	fmt.Fprintf(os.Stderr, "Connecting to %s@%s:%d...\n", user, host, port)

	// SSH connect
	conn, err := sshutil.Connect(host, port, user)
	if err != nil {
		return fmt.Errorf("ssh connect: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Connected. Key fingerprint: %s\n", conn.Fingerprint)

	// The runner keeps spare SSH sessions pre-opened so each command skips
	// the channel-open round trip.
	runner := sshutil.NewCommandRunner(conn.Client)

	// Deploy helper binary (or reuse the cached copy from a previous connect)
	remotePath, cached, err := deployBinary(runner)
	if err != nil {
		conn.Client.Close()
		return fmt.Errorf("deploy: %w", err)
	}

	if cached {
		fmt.Fprintf(os.Stderr, "Reusing cached helper at %s\n", remotePath)
	} else {
		fmt.Fprintf(os.Stderr, "Deployed helper to %s\n", remotePath)
	}

	// Run startup audit
	auditCmd := fmt.Sprintf("%s serve audit --action startup --user %s --client-ip %s --fingerprint %s",
		remotePath, shellEscape(user), shellEscape(conn.Host), shellEscape(conn.Fingerprint))
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

	// Remember the target this socket belongs to, so a later command can
	// restart the daemon itself instead of erroring out. Kept on shutdown.
	WriteTargetRecord(target, port)

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

// pingSocket reports whether a live daemon is already listening at sockPath. It
// dials the Unix socket (2s timeout), sends a ping request using the same JSON
// framing as the client, and returns true if any valid DaemonResponse decodes.
// A read deadline ensures a present-but-dead socket cannot hang the caller.
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

	// Track the request so the idle watchdog neither fires mid-operation nor
	// counts a long-running command's duration as idle time.
	d.opStart()
	defer d.opEnd()

	handler := &Handler{daemon: d}
	resp := handler.Handle(&req)
	encoder.Encode(resp)
}

// watchIdle shuts the daemon down once it has been idle for idleTimeout with
// no operations in flight. It fires at most once.
func (d *Daemon) watchIdle() {
	ticker := time.NewTicker(idleCheckInterval)
	defer ticker.Stop()
	for range ticker.C {
		d.mu.Lock()
		idle := time.Since(d.lastActivity)
		busy := d.activeOps > 0
		d.mu.Unlock()
		// A live mount is state the user is relying on: unmounting it because
		// nobody ran a command for a while would break every process with a
		// file open under it, so mounts hold the daemon open.
		if !busy && !d.hasMounts() && idle > idleTimeout {
			fmt.Fprintf(os.Stderr, "\nIdle for %s; shutting down...\n", idle.Round(time.Second))
			d.shutdown()
			exitFunc(0)
			return
		}
	}
}

func (d *Daemon) shutdown() {
	// Detach mounts first: they run over the SSH connection this is about to
	// close, and a mountpoint whose backing connection is gone hangs every
	// process that touches it.
	d.unmountAll()

	// Drain in-flight async audit writes before the shutdown audit/cleanup so
	// no entries are lost and the shutdown entry stays last.
	d.auditWG.Wait()

	if d.runner != nil {
		// Send shutdown audit to remote
		auditCmd := fmt.Sprintf("%s serve audit --action shutdown", d.remotePath)
		d.runner.Run(auditCmd)

		// Delete the remote binary, unless it lives in the content-addressed
		// cache dir — then it stays for the next connect to reuse.
		if !d.keepBinary {
			d.runner.Run(fmt.Sprintf("rm -f %s", d.remotePath))
		}
	}

	d.cleanup()
}

func (d *Daemon) cleanup() {
	// Remove the files before closing the listener: closing it unblocks the
	// accept loop, whose return exits the process, and that race left the PID
	// file behind on every clean disconnect. The target record is kept -- it is
	// what lets the next command start a daemon without being told the target.
	os.Remove(d.sockPath)
	os.Remove(d.pidPath)
	if d.listener != nil {
		d.listener.Close()
	}
	if d.conn != nil {
		d.conn.Client.Close()
	}
}

// deployBinary ships the helper binary to the remote and returns its path.
// reused reports that an identical cached binary was already present and no
// upload happened.
func deployBinary(runner Runner) (remotePath string, reused bool, err error) {
	// Find the binary to deploy
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

// deployBinaryData deploys the given helper binary bytes. The remote path is
// content-addressed (sha256 of the binary) under ~/.cache/remote-agent, so a
// reconnect finds the identical binary already in place and skips uploading
// the multi-megabyte payload entirely. The cache lives under $HOME rather
// than world-writable /tmp so no other user can pre-plant or swap the file,
// and uploads go through a unique temp path plus rename so a concurrent
// connect can never observe a partial binary.
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
		// No usable $HOME (or probe failed): fall back to a random /tmp path.
		// It is removed again on disconnect (keepBinary stays false).
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

// cachedDeploy reports whether remotePath lives in the persistent helper
// cache (as opposed to a throwaway /tmp path), in which case disconnect
// leaves it in place for the next connect to reuse.
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

func parseTarget(target string) (user, host string, err error) {
	if user, host, found := strings.Cut(target, "@"); found {
		return user, host, nil
	}
	// No user specified — fall back to $USER, then root.
	currentUser := os.Getenv("USER")
	if currentUser == "" {
		currentUser = "root"
	}
	return currentUser, target, nil
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
