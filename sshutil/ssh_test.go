package sshutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestSafeBuffer(t *testing.T) {
	var buf safeBuffer

	// Empty buffer
	if len(buf.Bytes()) != 0 {
		t.Error("empty buffer should have 0 bytes")
	}

	// Write some data
	n, err := buf.Write([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("wrote %d, want 5", n)
	}

	// Write more data
	n, err = buf.Write([]byte(" world"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Errorf("wrote %d, want 6", n)
	}

	// Check accumulated content
	if string(buf.Bytes()) != "hello world" {
		t.Errorf("content = %q, want %q", string(buf.Bytes()), "hello world")
	}
}

func TestSafeBufferEmpty(t *testing.T) {
	var buf safeBuffer
	if buf.Bytes() != nil {
		t.Error("Bytes() on empty buffer should be nil")
	}
}

func TestSafeBufferBinaryData(t *testing.T) {
	var buf safeBuffer
	data := []byte{0x00, 0x01, 0xff, 0xfe}
	buf.Write(data)
	got := buf.Bytes()
	if len(got) != 4 || got[0] != 0x00 || got[2] != 0xff {
		t.Errorf("binary data mismatch: %v", got)
	}
}

func TestConnResultFields(t *testing.T) {
	cr := ConnResult{
		Fingerprint: "SHA256:test",
		User:        "admin",
		Host:        "example.com",
		Port:        22,
	}
	if cr.Fingerprint != "SHA256:test" {
		t.Errorf("fingerprint = %q", cr.Fingerprint)
	}
	if cr.User != "admin" {
		t.Errorf("user = %q", cr.User)
	}
	if cr.Host != "example.com" {
		t.Errorf("host = %q", cr.Host)
	}
	if cr.Port != 22 {
		t.Errorf("port = %d", cr.Port)
	}
}

// generateTestKey creates a test ED25519 key pair and returns the signer.
func generateTestKey(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

// writeTestKey writes a test SSH private key to the given path in PEM format.
func writeTestKey(t *testing.T, path string) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// Marshal to OpenSSH format
	privBytes, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(privBytes), 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

// startTestSSHServer creates a minimal SSH server for testing.
// It accepts connections and runs commands via a simple shell emulator.
func startTestSSHServer(t *testing.T) (addr string, cleanup func()) {
	t.Helper()

	hostKey := generateTestKey(t)

	config := &ssh.ServerConfig{
		NoClientAuth: true,
	}
	config.AddHostKey(hostKey)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleTestConnection(conn, config)
		}
	}()

	return listener.Addr().String(), func() { listener.Close() }
}

func handleTestConnection(conn net.Conn, config *ssh.ServerConfig) {
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer sshConn.Close()

	// Discard global requests
	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		ch, requests, err := newChan.Accept()
		if err != nil {
			continue
		}

		go func(ch ssh.Channel, reqs <-chan *ssh.Request) {
			defer ch.Close()
			for req := range reqs {
				switch req.Type {
				case "exec":
					// Parse the command from the request payload
					if len(req.Payload) < 4 {
						req.Reply(false, nil)
						continue
					}
					cmdLen := int(req.Payload[0])<<24 | int(req.Payload[1])<<16 | int(req.Payload[2])<<8 | int(req.Payload[3])
					if len(req.Payload) < 4+cmdLen {
						req.Reply(false, nil)
						continue
					}
					cmd := string(req.Payload[4 : 4+cmdLen])
					req.Reply(true, nil)

					// Simple command handling
					switch {
					case cmd == "echo pong":
						ch.Write([]byte("pong\n"))
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					case cmd == "exit 42":
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{42}))
					case cmd == "cat":
						// Read stdin and echo it back
						buf := make([]byte, 4096)
						n, _ := ch.Read(buf)
						ch.Write(buf[:n])
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					case cmd == "fail":
						ch.Stderr().Write([]byte("command failed\n"))
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{1}))
					default:
						ch.Write([]byte("executed: " + cmd + "\n"))
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					}
					// Close channel after command execution to signal EOF
					ch.CloseWrite()
					return
				default:
					req.Reply(false, nil)
				}
			}
		}(ch, requests)
	}
}

func TestRunCommand(t *testing.T) {
	addr, cleanup := startTestSSHServer(t)
	defer cleanup()

	client := dialTestServer(t, addr)
	defer client.Close()

	stdout, stderr, exitCode, err := RunCommand(client, "echo pong")
	if err != nil {
		t.Fatalf("RunCommand error: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0", exitCode)
	}
	if string(stdout) != "pong\n" {
		t.Errorf("stdout = %q, want %q", string(stdout), "pong\n")
	}
	if len(stderr) != 0 {
		t.Errorf("stderr = %q, want empty", string(stderr))
	}
}

func TestRunCommandNonZeroExit(t *testing.T) {
	addr, cleanup := startTestSSHServer(t)
	defer cleanup()

	client := dialTestServer(t, addr)
	defer client.Close()

	_, _, exitCode, err := RunCommand(client, "exit 42")
	if err != nil {
		t.Fatalf("RunCommand error: %v", err)
	}
	if exitCode != 42 {
		t.Errorf("exit code = %d, want 42", exitCode)
	}
}

func TestRunCommandWithStdin(t *testing.T) {
	addr, cleanup := startTestSSHServer(t)
	defer cleanup()

	client := dialTestServer(t, addr)
	defer client.Close()

	input := []byte("hello from stdin")
	stdout, _, exitCode, err := RunCommandWithStdin(client, "cat", input)
	if err != nil {
		t.Fatalf("RunCommandWithStdin error: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0", exitCode)
	}
	if string(stdout) != "hello from stdin" {
		t.Errorf("stdout = %q, want %q", string(stdout), "hello from stdin")
	}
}

func TestRunCommandWithStdinEmpty(t *testing.T) {
	addr, cleanup := startTestSSHServer(t)
	defer cleanup()

	client := dialTestServer(t, addr)
	defer client.Close()

	// Test with empty stdin
	stdout, _, exitCode, err := RunCommandWithStdin(client, "cat", nil)
	if err != nil {
		t.Fatalf("RunCommandWithStdin error: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0", exitCode)
	}
	if len(stdout) != 0 {
		t.Errorf("stdout = %q, want empty", string(stdout))
	}
}

func dialTestServer(t *testing.T, addr string) *ssh.Client {
	t.Helper()
	config := &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		t.Fatalf("dial test server: %v", err)
	}
	return client
}

func TestBuildAuthMethodsWithKeyFile(t *testing.T) {
	// Create a temp home directory with an SSH key
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	os.MkdirAll(sshDir, 0700)

	writeTestKey(t, filepath.Join(sshDir, "id_ed25519"))

	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "") // disable agent

	methods, fingerprint, err := buildAuthMethods()
	if err != nil {
		t.Fatalf("buildAuthMethods error: %v", err)
	}
	if len(methods) == 0 {
		t.Error("expected at least one auth method")
	}
	if fingerprint == "" || fingerprint == "unknown" {
		t.Errorf("fingerprint = %q, want valid fingerprint", fingerprint)
	}
}

func TestBuildAuthMethodsNoMethods(t *testing.T) {
	// Empty home dir, no agent
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".ssh"), 0700)

	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")

	_, _, err := buildAuthMethods()
	if err == nil {
		t.Error("expected error when no auth methods available")
	}
}

func TestBuildHostKeyCallbackNoKnownHosts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	callback, err := buildHostKeyCallback()
	if err != nil {
		t.Fatal(err)
	}
	if callback == nil {
		t.Error("callback should not be nil (should use insecure fallback)")
	}
}

func TestBuildHostKeyCallbackWithKnownHosts(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	os.MkdirAll(sshDir, 0700)

	// Create a valid known_hosts file with a test host key
	hostKey := generateTestKey(t)
	knownHostsLine := fmt.Sprintf("example.com %s", string(ssh.MarshalAuthorizedKey(hostKey.PublicKey())))
	os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(knownHostsLine), 0600)

	t.Setenv("HOME", home)

	callback, err := buildHostKeyCallback()
	if err != nil {
		t.Fatal(err)
	}
	if callback == nil {
		t.Error("callback should not be nil")
	}
}

func TestConnectInvalidHost(t *testing.T) {
	// Set up auth so we don't fail at auth setup stage
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	os.MkdirAll(sshDir, 0700)
	writeTestKey(t, filepath.Join(sshDir, "id_ed25519"))

	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")

	// Connect to an address that will be refused
	_, err := Connect("127.0.0.1", 1, "user")
	if err == nil {
		t.Error("expected error connecting to invalid host")
	}
}

func TestConnectDefaultPort(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	os.MkdirAll(sshDir, 0700)
	writeTestKey(t, filepath.Join(sshDir, "id_ed25519"))

	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")

	// Port 0 should default to 22 and fail (no server on 22)
	_, err := Connect("127.0.0.1", 0, "user")
	if err == nil {
		t.Error("expected error (no SSH server on port 22)")
	}
}

func TestConnectSuccess(t *testing.T) {
	// Start test SSH server
	addr, srvCleanup := startTestSSHServer(t)
	defer srvCleanup()

	// Parse host:port from the test server address
	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	// Set up auth with a test key
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	os.MkdirAll(sshDir, 0700)
	writeTestKey(t, filepath.Join(sshDir, "id_ed25519"))

	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")

	// Use short keepalive interval for test
	oldInterval := keepAliveInterval
	keepAliveInterval = 50 * time.Millisecond
	defer func() { keepAliveInterval = oldInterval }()

	result, err := Connect(host, port, "testuser")
	if err != nil {
		t.Fatalf("Connect error: %v", err)
	}
	defer result.Client.Close()

	if result.User != "testuser" {
		t.Errorf("user = %q, want %q", result.User, "testuser")
	}
	if result.Host != host {
		t.Errorf("host = %q, want %q", result.Host, host)
	}
	if result.Port != port {
		t.Errorf("port = %d, want %d", result.Port, port)
	}
	if result.Fingerprint == "" {
		t.Error("fingerprint should not be empty")
	}

	// Verify we can run commands through this connection
	stdout, _, exitCode, err := RunCommand(result.Client, "echo pong")
	if err != nil {
		t.Fatalf("RunCommand through Connect: %v", err)
	}
	if exitCode != 0 || string(stdout) != "pong\n" {
		t.Errorf("unexpected result: exit=%d stdout=%q", exitCode, string(stdout))
	}
}

func TestKeepAlive(t *testing.T) {
	addr, cleanup := startTestSSHServer(t)
	defer cleanup()

	client := dialTestServer(t, addr)
	defer client.Close()

	// Use a very short interval for testing
	oldInterval := keepAliveInterval
	keepAliveInterval = 10 * time.Millisecond
	defer func() { keepAliveInterval = oldInterval }()

	// Start keepalive - it should run at least once
	go keepAlive(client)

	// Wait a bit for at least one keepalive
	time.Sleep(50 * time.Millisecond)

	// Close the client - this will cause keepAlive to exit
	client.Close()
	time.Sleep(20 * time.Millisecond)
}

func TestKeepAliveDisconnect(t *testing.T) {
	addr, cleanup := startTestSSHServer(t)
	defer cleanup()

	client := dialTestServer(t, addr)

	oldInterval := keepAliveInterval
	keepAliveInterval = 10 * time.Millisecond
	defer func() { keepAliveInterval = oldInterval }()

	done := make(chan struct{})
	go func() {
		keepAlive(client)
		close(done)
	}()

	// Let it run briefly then disconnect
	time.Sleep(30 * time.Millisecond)
	client.Close()

	// keepAlive should return after disconnect
	<-done
}
