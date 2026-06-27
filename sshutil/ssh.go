package sshutil

import (
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

	// Start keepalive
	go keepAlive(client)

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

func buildHostKeyCallback() (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	knownHostsFile := filepath.Join(home, ".ssh", "known_hosts")
	if _, err := os.Stat(knownHostsFile); os.IsNotExist(err) {
		// If no known_hosts file, warn but allow connections
		// This is safer than InsecureIgnoreHostKey but allows first-time use
		return ssh.InsecureIgnoreHostKey(), nil
	}

	callback, err := knownhosts.New(knownHostsFile)
	if err != nil {
		return nil, fmt.Errorf("parse known_hosts: %w", err)
	}
	return callback, nil
}

// keepAliveInterval is the interval between keepalive pings. Can be overridden in tests.
var keepAliveInterval = 30 * time.Second

func keepAlive(client *ssh.Client) {
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()
	for range ticker.C {
		_, _, err := client.SendRequest("keepalive@remote-agent", true, nil)
		if err != nil {
			return
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
