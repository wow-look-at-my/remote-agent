package sshutil

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
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
	assert.Equal(t, // Check accumulated content
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
		Fingerprint: "SHA256:test",
		User:        "admin",
		Host:        "example.com",
		Port:        22,
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
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
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
	t.Setenv("SSH_AUTH_SOCK", "") // disable agent

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

// fakeAddr is a minimal net.Addr for host-key callback tests.
type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

func TestHostKeyTOFURecordsAndThenVerifies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	keyA := generateTestKey(t).PublicKey()
	keyB := generateTestKey(t).PublicKey()
	addr := fakeAddr("192.0.2.10:2222")

	// First contact, no known_hosts: accept-new records the key.
	callback, err := buildHostKeyCallback()
	require.Nil(t, err)
	require.Nil(t, callback("192.0.2.10:2222", addr, keyA))

	data, err := os.ReadFile(filepath.Join(home, ".ssh", "known_hosts"))
	require.Nil(t, err)
	assert.Contains(t, string(data), "[192.0.2.10]:2222")

	// Rebuild the callback (fresh process): the recorded key must verify...
	callback, err = buildHostKeyCallback()
	require.Nil(t, err)
	assert.Nil(t, callback("192.0.2.10:2222", addr, keyA))

	// A second key for the recorded host is the case recording it exists for.
	assert.NotNil(t, callback("192.0.2.10:2222", addr, keyB))
}

func TestHostKeyUnknownHostAddedAlongsideExistingEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	require.Nil(t, os.MkdirAll(sshDir, 0700))

	// Pre-existing known_hosts with some other host.
	existing := generateTestKey(t).PublicKey()
	line := fmt.Sprintf("known.example.com %s", string(ssh.MarshalAuthorizedKey(existing)))
	require.Nil(t, os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(line), 0600))

	// An unknown host is trusted on first use, even when known_hosts already exists.
	callback, err := buildHostKeyCallback()
	require.Nil(t, err)
	newKey := generateTestKey(t).PublicKey()
	assert.Nil(t, callback("fresh.example.com:22", fakeAddr("198.51.100.7:22"), newKey))

	data, err := os.ReadFile(filepath.Join(sshDir, "known_hosts"))
	require.Nil(t, err)
	assert.Contains(t, string(data), "known.example.com")
	assert.Contains(t, string(data), "fresh.example.com")
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
	_, errIPv6 := Connect(ConnectOptions{Host: "::1", Port: 1, User: "user"})
	require.Error(t, errIPv6)
	assert.Contains(t, errIPv6.Error(), "[::1]:1",
		"an IPv6 address needs brackets, or the dial address is unparseable")

	_, err := Connect(ConnectOptions{Host: "127.0.0.1", Port: 1, User: "user"})
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
	_, err := Connect(ConnectOptions{Host: "127.0.0.1", User: "user"})
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

	result, err := Connect(ConnectOptions{Host: host, Port: port, User: "testuser"})
	require.Nil(t, err)

	defer result.Conn.Close()
	assert.Equal(t, "testuser", result.User)
	assert.Equal(t, host, result.Host)
	assert.Equal(t, port, result.Port)
	assert.NotEqual(t, "", result.Fingerprint)
	assert.Empty(t, result.ControlPath, "a dialed connection rode no control master")

	// Verify we can run commands through this connection
	stdout, _, exitCode, err := result.Conn.Run("echo pong")
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
	go keepAlive(client, keepAliveInterval)

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
		keepAlive(client, keepAliveInterval)
		close(done)
	}()

	// Let it run briefly then disconnect
	time.Sleep(30 * time.Millisecond)
	client.Close()

	// keepAlive should return after disconnect
	<-done
}

// sshGFunc is the shape of the ssh -G seam.
type sshGFunc func(user, host string, port int) *exec.Cmd

// stubSSHG installs stub as the seam for the rest of the test and returns the
// real one, which a stub that inspects the command ssh would run still needs.
// The seam is a package variable the whole process shares, so the test takes
// the process to itself.
func stubSSHG(t *testing.T, stub sshGFunc) sshGFunc {
	t.Helper()
	t.Serial()
	real := sshGFunc(sshGCommand)
	sshGCommand = stub
	t.Cleanup(func() { sshGCommand = real })
	return real
}

func TestResolveSSHConfig(t *testing.T) {
	stubSSHG(t, func(user, host string, port int) *exec.Cmd {
		return exec.Command("echo", "hostname 1.2.3.4\nuser bob\nport 2222")
	})

	cfg := ResolveSSHConfig("", "myalias", 0)
	require.NotNil(t, cfg)
	assert.Equal(t, "1.2.3.4", cfg.HostName)
	assert.Equal(t, "bob", cfg.User)
	assert.Equal(t, 2222, cfg.Port)
}

// ssh expands the %r and %p tokens of a ControlPath from what it is told, so a
// host on port 2201 resolved without -p reports the socket of the same host on
// port 22 -- a master for the wrong endpoint.
func TestResolveSSHConfigPassesLoginAndPort(t *testing.T) {
	var args []string
	var real sshGFunc
	real = stubSSHG(t, func(user, host string, port int) *exec.Cmd {
		cmd := real(user, host, port)
		args = cmd.Args
		return exec.Command("echo", "hostname 127.0.0.1")
	})

	require.NotNil(t, ResolveSSHConfig("root", "127.0.0.1", 2201))
	assert.Contains(t, args, "-p")
	assert.Contains(t, args, "2201")
	assert.Contains(t, args, "-l")
	assert.Contains(t, args, "root")
	assert.Equal(t, "127.0.0.1", args[len(args)-1])

	require.NotNil(t, ResolveSSHConfig("", "myalias", 0))
	assert.NotContains(t, args, "-p", "a target that names no port must not pin one")
	assert.NotContains(t, args, "-l")
}

func TestResolveSSHConfigMultipleLines(t *testing.T) {
	// Realistic `ssh -G` output: the three wanted keys interleaved with many
	// extra fields that must be ignored.
	stubSSHG(t, func(user, host string, port int) *exec.Cmd {
		return exec.Command("echo", "identityfile ~/.ssh/id_rsa\nhostname example.com\nforwardagent no\nuser deploy\naddkeystoagent no\nport 2200\nciphers aes128-ctr")
	})

	cfg := ResolveSSHConfig("", "myalias", 0)
	require.NotNil(t, cfg)
	assert.Equal(t, "example.com", cfg.HostName)
	assert.Equal(t, "deploy", cfg.User)
	assert.Equal(t, 2200, cfg.Port)
}

func TestResolveSSHConfigCommandFails(t *testing.T) {
	stubSSHG(t, func(user, host string, port int) *exec.Cmd {
		return exec.Command("false")
	})

	assert.Nil(t, ResolveSSHConfig("", "myalias", 0))
}

func TestResolveSSHConfigPartialOutput(t *testing.T) {
	stubSSHG(t, func(user, host string, port int) *exec.Cmd {
		return exec.Command("echo", "hostname only.example.com")
	})

	cfg := ResolveSSHConfig("", "myalias", 0)
	require.NotNil(t, cfg)
	assert.Equal(t, "only.example.com", cfg.HostName)
	assert.Equal(t, "", cfg.User)
	assert.Equal(t, 0, cfg.Port)
}

func TestResolveSSHConfigInvalidPort(t *testing.T) {
	stubSSHG(t, func(user, host string, port int) *exec.Cmd {
		return exec.Command("echo", "hostname h.example.com\nport notanumber\nuser carol")
	})

	cfg := ResolveSSHConfig("", "myalias", 0)
	require.NotNil(t, cfg)
	assert.Equal(t, "h.example.com", cfg.HostName)
	assert.Equal(t, "carol", cfg.User)
	assert.Equal(t, 0, cfg.Port) // unparseable port is silently skipped
}
