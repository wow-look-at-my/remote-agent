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

	"github.com/wow-look-at-my/remote-agent/protocol"
	"github.com/wow-look-at-my/remote-agent/sshutil"
	"golang.org/x/crypto/ssh"
)

// Daemon holds a persistent SSH connection and serves requests over a Unix socket.
type Daemon struct {
	conn       *sshutil.ConnResult
	runner     Runner // abstraction over SSH command execution
	remotePath string // path to remote helper binary
	sockPath   string
	pidPath    string
	listener   net.Listener
	mu         sync.Mutex // serialize SSH session access
}

// sshRunner implements Runner using a real SSH client.
type sshRunner struct {
	client *ssh.Client
}

func (r *sshRunner) Run(command string) (stdout, stderr []byte, exitCode int, err error) {
	return sshutil.RunCommand(r.client, command)
}

func (r *sshRunner) RunStdin(command string, stdin []byte) (stdout, stderr []byte, exitCode int, err error) {
	return sshutil.RunCommandWithStdin(r.client, command, stdin)
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

	fmt.Fprintf(os.Stderr, "Connecting to %s@%s:%d...\n", user, host, port)

	// SSH connect
	conn, err := sshutil.Connect(host, port, user)
	if err != nil {
		return fmt.Errorf("ssh connect: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Connected. Key fingerprint: %s\n", conn.Fingerprint)

	// Deploy helper binary
	remotePath, err := deployBinary(conn.Client)
	if err != nil {
		conn.Client.Close()
		return fmt.Errorf("deploy: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Deployed helper to %s\n", remotePath)

	runner := &sshRunner{client: conn.Client}

	// Run startup audit
	auditCmd := fmt.Sprintf("%s serve audit --action startup --user %s --client-ip %s --fingerprint %s",
		remotePath, shellEscape(user), shellEscape(conn.Host), shellEscape(conn.Fingerprint))
	runner.Run(auditCmd)

	d := &Daemon{
		conn:       conn,
		runner:     runner,
		remotePath: remotePath,
		sockPath:   SocketPath(target),
		pidPath:    PIDPath(target),
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

func (d *Daemon) handleClient(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	var req protocol.DaemonRequest
	if err := decoder.Decode(&req); err != nil {
		encoder.Encode(protocol.DaemonResponse{Error: "invalid request: " + err.Error()})
		return
	}

	handler := &Handler{daemon: d}
	resp := handler.Handle(&req)
	encoder.Encode(resp)
}

func (d *Daemon) shutdown() {
	if d.runner != nil {
		// Send shutdown audit to remote
		auditCmd := fmt.Sprintf("%s serve audit --action shutdown", d.remotePath)
		d.runner.Run(auditCmd)

		// Delete remote binary
		d.runner.Run(fmt.Sprintf("rm -f %s", d.remotePath))
	}

	d.cleanup()
}

func (d *Daemon) cleanup() {
	if d.listener != nil {
		d.listener.Close()
	}
	os.Remove(d.sockPath)
	os.Remove(d.pidPath)
	if d.conn != nil {
		d.conn.Client.Close()
	}
}

func deployBinary(client *ssh.Client) (string, error) {
	// Find the binary to deploy
	localBinary, err := findDeployBinary()
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(localBinary)
	if err != nil {
		return "", fmt.Errorf("read binary %s: %w", localBinary, err)
	}

	// Generate random remote path
	remotePath := fmt.Sprintf("/tmp/.remote-agent-%s", randomSuffix())

	// Copy via cat > path
	cmd := fmt.Sprintf("cat > %s && chmod 700 %s", remotePath, remotePath)
	_, stderr, exitCode, err := sshutil.RunCommandWithStdin(client, cmd, data)
	if err != nil {
		return "", fmt.Errorf("copy binary: %w", err)
	}
	if exitCode != 0 {
		return "", fmt.Errorf("copy binary failed (exit %d): %s", exitCode, string(stderr))
	}

	return remotePath, nil
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
