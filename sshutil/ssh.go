package sshutil

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SSHConfig holds the fields resolved from `ssh -G <host>` (the effective SSH
// client configuration, including any ~/.ssh/config Host alias).
type SSHConfig struct {
	HostName string
	User     string
	Port     int
}

// sshGCommand builds the `ssh -G <host>` command. It is a test seam so the
// resolver can be exercised without invoking the real ssh binary.
var sshGCommand = func(host string) *exec.Cmd { return exec.Command("ssh", "-G", host) }

// ResolveSSHConfig runs `ssh -G <host>` and parses the effective hostname, user,
// and port for the given host (resolving any ~/.ssh/config Host alias). It
// returns nil if ssh is unavailable or the command fails. Unknown keys and an
// unparseable port are silently ignored.
func ResolveSSHConfig(host string) *SSHConfig {
	out, err := sshGCommand(host).Output()
	if err != nil {
		slog.Debug("ssh -G failed; skipping ssh config resolution", "host", host, "error", err)
		return nil
	}

	cfg := &SSHConfig{}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		switch strings.ToLower(key) {
		case "hostname":
			cfg.HostName = value
		case "user":
			cfg.User = value
		case "port":
			if p, err := strconv.Atoi(value); err == nil {
				cfg.Port = p
			}
		}
	}
	return cfg
}

// ConnResult holds the SSH client and metadata from a successful connection.
type ConnResult struct {
	Client      *ssh.Client
	Fingerprint string // SHA256 fingerprint of the key used
	User        string
	Host        string
	Port        int
}

// Connect establishes an SSH connection using agent auth or key files.
func Connect(host string, port int, user string) (*ConnResult, error) {
	if port == 0 {
		port = 22
	}

	authMethods, fingerprint, err := buildAuthMethods()
	if err != nil {
		return nil, fmt.Errorf("ssh auth setup: %w", err)
	}

	hostKeyCallback, err := buildHostKeyCallback()
	if err != nil {
		return nil, fmt.Errorf("host key verification setup: %w", err)
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         10 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	// Start keepalive. Capture the interval on this (synchronous) goroutine so
	// the spawned keepAlive goroutine never reads the keepAliveInterval package
	// var concurrently (tests mutate it).
	go keepAlive(client, keepAliveInterval)

	return &ConnResult{
		Client:      client,
		Fingerprint: fingerprint,
		User:        user,
		Host:        host,
		Port:        port,
	}, nil
}

// RunCommand opens a new SSH session, runs a command, and returns stdout+stderr+exit code.
func RunCommand(client *ssh.Client, command string) (stdout, stderr []byte, exitCode int, err error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, nil, -1, fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	var outBuf, errBuf safeBuffer
	session.Stdout = &outBuf
	session.Stderr = &errBuf

	err = session.Run(command)
	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			return outBuf.Bytes(), errBuf.Bytes(), exitErr.ExitStatus(), nil
		}
		return outBuf.Bytes(), errBuf.Bytes(), -1, err
	}
	return outBuf.Bytes(), errBuf.Bytes(), 0, nil
}

// RunCommandWithStdin opens a session, pipes data to stdin, runs the command.
func RunCommandWithStdin(client *ssh.Client, command string, stdin []byte) (stdout, stderr []byte, exitCode int, err error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, nil, -1, fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	stdinPipe, err := session.StdinPipe()
	if err != nil {
		return nil, nil, -1, fmt.Errorf("stdin pipe: %w", err)
	}

	var outBuf, errBuf safeBuffer
	session.Stdout = &outBuf
	session.Stderr = &errBuf

	if err := session.Start(command); err != nil {
		return nil, nil, -1, fmt.Errorf("start command: %w", err)
	}

	if _, err := stdinPipe.Write(stdin); err != nil {
		return nil, nil, -1, fmt.Errorf("write stdin: %w", err)
	}
	stdinPipe.Close()

	err = session.Wait()
	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			return outBuf.Bytes(), errBuf.Bytes(), exitErr.ExitStatus(), nil
		}
		return outBuf.Bytes(), errBuf.Bytes(), -1, err
	}
	return outBuf.Bytes(), errBuf.Bytes(), 0, nil
}

func buildAuthMethods() ([]ssh.AuthMethod, string, error) {
	var methods []ssh.AuthMethod
	var fingerprint string

	// Try SSH agent first
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			agentClient := agent.NewClient(conn)
			methods = append(methods, ssh.PublicKeysCallback(agentClient.Signers))

			// Get fingerprint from agent's first key
			keys, err := agentClient.List()
			if err == nil && len(keys) > 0 {
				fingerprint = ssh.FingerprintSHA256(keys[0])
			}
		}
	}

	// Try key files
	home, err := os.UserHomeDir()
	if err == nil {
		keyFiles := []string{
			filepath.Join(home, ".ssh", "id_ed25519"),
			filepath.Join(home, ".ssh", "id_ecdsa"),
			filepath.Join(home, ".ssh", "id_rsa"),
		}
		for _, kf := range keyFiles {
			data, err := os.ReadFile(kf)
			if err != nil {
				continue
			}
			signer, err := ssh.ParsePrivateKey(data)
			if err != nil {
				continue
			}
			methods = append(methods, ssh.PublicKeys(signer))
			if fingerprint == "" {
				fingerprint = ssh.FingerprintSHA256(signer.PublicKey())
			}
		}
	}

	if len(methods) == 0 {
		return nil, "", fmt.Errorf("no SSH authentication methods available (no SSH_AUTH_SOCK and no key files found)")
	}

	if fingerprint == "" {
		fingerprint = "unknown"
	}

	return methods, fingerprint, nil
}

// buildHostKeyCallback returns a host-key callback with OpenSSH
// "accept-new" semantics (true trust-on-first-use): a host already in
// ~/.ssh/known_hosts must present the recorded key (a mismatch fails the
// connection), while a host never seen before is accepted once and its key is
// recorded so every later connection detects substitution.
//
// Previously an absent known_hosts file disabled verification entirely and
// permanently (InsecureIgnoreHostKey), and a present known_hosts file made
// connections to any new host fail outright.
func buildHostKeyCallback() (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	sshDir := filepath.Join(home, ".ssh")
	knownHostsFile := filepath.Join(sshDir, "known_hosts")
	if _, err := os.Stat(knownHostsFile); os.IsNotExist(err) {
		// Create an empty file so knownhosts.New works and first-use keys
		// have somewhere to be recorded.
		if err := os.MkdirAll(sshDir, 0700); err != nil {
			return nil, fmt.Errorf("create %s: %w", sshDir, err)
		}
		f, err := os.OpenFile(knownHostsFile, os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return nil, fmt.Errorf("create %s: %w", knownHostsFile, err)
		}
		f.Close()
	}

	verify, err := knownhosts.New(knownHostsFile)
	if err != nil {
		return nil, fmt.Errorf("parse known_hosts: %w", err)
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := verify(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
			// Unknown host: first use. Record the key so later connections
			// verify it, and tell the user what was trusted.
			if aerr := appendKnownHost(knownHostsFile, hostname, key); aerr != nil {
				return fmt.Errorf("record host key for %s: %w", hostname, aerr)
			}
			fmt.Fprintf(os.Stderr,
				"Warning: unknown host %s; trusting on first use and recording key %s in %s\n",
				hostname, ssh.FingerprintSHA256(key), knownHostsFile)
			return nil
		}
		// Key mismatch (or unreadable file): hard failure.
		return err
	}, nil
}

// appendKnownHost appends a known_hosts entry for hostname with the given key.
func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	_, err = fmt.Fprintln(f, line)
	return err
}

// keepAliveInterval is the interval between keepalive pings. Can be overridden in tests.
var keepAliveInterval = 30 * time.Second

// keepAlive pings the remote every interval so idle NAT/firewall state does
// not drop the connection, and stops as soon as the connection is gone.
//
// The ping deliberately does NOT request a reply, and the loop watches
// client.Wait() rather than relying on SendRequest to report the disconnect.
// A reply-wanting global request is unusable here: golang.org/x/crypto/ssh
// (v0.52) drains buffered responses with `select { case <-m.globalResponses:
// default: }`, and once the connection closes that channel is closed, so the
// receive is always ready and the drain loop spins forever at 100% CPU
// instead of returning an error (ssh/mux.go:158). A no-reply ping returns the
// write error directly, and client.Wait() covers a write that lands in a dead
// socket's buffer.
func keepAlive(client *ssh.Client, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	closed := make(chan struct{})
	go func() {
		client.Wait()
		close(closed)
	}()

	for {
		select {
		case <-closed:
			return
		case <-ticker.C:
			if _, _, err := client.SendRequest("keepalive@remote-agent", false, nil); err != nil {
				return
			}
		}
	}
}

// safeBuffer is a simple bytes.Buffer replacement that's safe to use as io.Writer.
type safeBuffer struct {
	data []byte
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *safeBuffer) Bytes() []byte {
	return b.data
}
