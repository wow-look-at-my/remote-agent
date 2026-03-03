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
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestSafeBuffer(t *testing.T) {
	var buf safeBuffer
	assert.

	// Empty buffer
	Equal(t, 0, len(buf.Bytes()))

	// Write some data
	n, err := buf.Write([]byte("hello"))
	require.Nil(t, err)
	assert.Equal(t, 5, n)

	// Write more data
	n, err = buf.Write([]byte(" world"))
	require.Nil(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t,// Check accumulated content
	"hello world", string(buf.Bytes()))

}

func TestSafeBufferEmpty(t *testing.T) {
	var buf safeBuffer
	assert.Nil(t, buf.Bytes())

}

func TestSafeBufferBinaryData(t *testing.T) {
	var buf safeBuffer
	data := []byte{0x00, 0x01, 0xff, 0xfe}
	buf.Write(data)
	got := buf.Bytes()
	assert.False(t, len(got) != 4 || got[0] != 0x00 || got[2] != 0xff)

}

func TestConnResultFields(t *testing.T) {
	cr := ConnResult{
		Fingerprint:	"SHA256:test",
		User:		"admin",
		Host:		"example.com",
		Port:		22,
	}
	assert.Equal(t, "SHA256:test", cr.Fingerprint)
	assert.Equal(t, "admin", cr.User)
	assert.Equal(t, "example.com", cr.Host)
	assert.Equal(t, 22, cr.Port)

}

// generateTestKey creates a test ED25519 key pair and returns the signer.
func generateTestKey(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.Nil(t, err)

	signer, err := ssh.NewSignerFromKey(priv)
	require.Nil(t, err)

	return signer
}

// writeTestKey writes a test SSH private key to the given path in PEM format.
func writeTestKey(t *testing.T, path string) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.Nil(t, err)

	// Marshal to OpenSSH format
	privBytes, err := ssh.MarshalPrivateKey(priv, "")
	require.Nil(t, err)
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(privBytes), 0600))

	signer, err := ssh.NewSignerFromKey(priv)
	require.Nil(t, err)

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
	require.Nil(t, err)

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
	require.Nil(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Equal(t, "pong\n", string(stdout))
	assert.Equal(t, 0, len(stderr))

}

func TestRunCommandNonZeroExit(t *testing.T) {
	addr, cleanup := startTestSSHServer(t)
	defer cleanup()

	client := dialTestServer(t, addr)
	defer client.Close()

	_, _, exitCode, err := RunCommand(client, "exit 42")
	require.Nil(t, err)
	assert.Equal(t, 42, exitCode)

}

func TestRunCommandWithStdin(t *testing.T) {
	addr, cleanup := startTestSSHServer(t)
	defer cleanup()

	client := dialTestServer(t, addr)
	defer client.Close()

	input := []byte("hello from stdin")
	stdout, _, exitCode, err := RunCommandWithStdin(client, "cat", input)
	require.Nil(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Equal(t, "hello from stdin", string(stdout))

}

func TestRunCommandWithStdinEmpty(t *testing.T) {
	addr, cleanup := startTestSSHServer(t)
	defer cleanup()

	client := dialTestServer(t, addr)
	defer client.Close()

	// Test with empty stdin
	stdout, _, exitCode, err := RunCommandWithStdin(client, "cat", nil)
	require.Nil(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Equal(t, 0, len(stdout))

}

func dialTestServer(t *testing.T, addr string) *ssh.Client {
	t.Helper()
	config := &ssh.ClientConfig{
		User:			"test",
		HostKeyCallback:	ssh.InsecureIgnoreHostKey(),
	}
	client, err := ssh.Dial("tcp", addr, config)
	require.Nil(t, err)

	return client
}

func TestBuildAuthMethodsWithKeyFile(t *testing.T) {
	// Create a temp home directory with an SSH key
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	os.MkdirAll(sshDir, 0700)

	writeTestKey(t, filepath.Join(sshDir, "id_ed25519"))

	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")	// disable agent

	methods, fingerprint, err := buildAuthMethods()
	require.Nil(t, err)
	assert.NotEqual(t, 0, len(methods))
	assert.False(t, fingerprint == "" || fingerprint == "unknown")

}

func TestBuildAuthMethodsNoMethods(t *testing.T) {
	// Empty home dir, no agent
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".ssh"), 0700)

	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")

	_, _, err := buildAuthMethods()
	assert.NotNil(t, err)

}

func TestBuildHostKeyCallbackNoKnownHosts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	callback, err := buildHostKeyCallback()
	require.Nil(t, err)
	assert.NotNil(t, callback)

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
	require.Nil(t, err)
	assert.NotNil(t, callback)

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
	assert.NotNil(t, err)

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
	assert.NotNil(t, err)

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
	require.Nil(t, err)

	defer result.Client.Close()
	assert.Equal(t, "testuser", result.User)
	assert.Equal(t, host, result.Host)
	assert.Equal(t, port, result.Port)
	assert.NotEqual(t, "", result.Fingerprint)

	// Verify we can run commands through this connection
	stdout, _, exitCode, err := RunCommand(result.Client, "echo pong")
	require.Nil(t, err)
	assert.False(t, exitCode != 0 || string(stdout) != "pong\n")

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
