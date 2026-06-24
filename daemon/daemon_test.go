package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

func TestSocketPath(t *testing.T) {
	path := SocketPath("user@host.example.com")
	assert.NotEqual(t, "", path)
	assert.Equal(t, path, SocketPath("user@host.example.com"))
	assert.NotEqual(t, path, SocketPath("other@host.example.com"))

}

func TestPIDPath(t *testing.T) {
	path := PIDPath("user@host.example.com")
	assert.NotEqual(t, "", path)
	assert.Equal(t, path, PIDPath("user@host.example.com"))

}

func TestSocketAndPIDPathDiffer(t *testing.T) {
	target := "user@host.example.com"
	assert.NotEqual(t, PIDPath(target), SocketPath(target))

}

func TestParseTarget(t *testing.T) {
	tests := []struct {
		input    string
		wantUser string
		wantHost string
	}{
		{"admin@server.com", "admin", "server.com"},
		{"root@10.0.0.1", "root", "10.0.0.1"},
		{"deploy@my-host", "deploy", "my-host"},
	}
	for _, tt := range tests {
		user, host, err := parseTarget(tt.input)
		assert.Nil(t, err)
		assert.Equal(t, tt.wantUser, user)
		assert.Equal(t, tt.wantHost, host)

	}
}

func TestParseTargetNoUser(t *testing.T) {
	user, host, err := parseTarget("server.com")
	require.Nil(t, err)
	assert.Equal(t, "server.com", host)
	assert.NotEqual(t, "", user)

}

func TestShellEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"with spaces", "'with spaces'"},
		{"it's", "'it'\"'\"'s'"},
		{"", "''"},
		{"hello world", "'hello world'"},
	}
	for _, tt := range tests {
		got := shellEscape(tt.input)
		assert.Equal(t, tt.want, got)

	}
}

func TestRandomSuffix(t *testing.T) {
	s1 := randomSuffix()
	s2 := randomSuffix()
	assert.Equal(t, 8, len(s1))
	assert.NotEqual(t, s2, s1)

}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	pidPath := filepath.Join(dir, "test.pid")

	// Create files to clean up
	os.WriteFile(pidPath, []byte("12345"), 0644)
	l, err := net.Listen("unix", sockPath)
	require.Nil(t, err)

	d := &Daemon{
		listener: l,
		sockPath: sockPath,
		pidPath:  pidPath,
	}
	d.cleanup()

	_, err = os.Stat(sockPath)
	assert.True(t, os.IsNotExist(err))

	_, err = os.Stat(pidPath)
	assert.True(t, os.IsNotExist(err))

}

func TestCleanupNilListener(t *testing.T) {
	dir := t.TempDir()
	d := &Daemon{
		sockPath: filepath.Join(dir, "nonexistent.sock"),
		pidPath:  filepath.Join(dir, "nonexistent.pid"),
	}
	// Should not panic
	d.cleanup()
}

func TestShutdownWithRunner(t *testing.T) {
	mock := newMockRunner()
	d := &Daemon{
		runner:     mock,
		remotePath: "/tmp/.remote-agent-test",
		sockPath:   filepath.Join(t.TempDir(), "test.sock"),
		pidPath:    filepath.Join(t.TempDir(), "test.pid"),
	}
	d.shutdown()
	assert.

		// Verify audit and cleanup commands were run
		GreaterOrEqual(t, len(mock.calls), 2)

}

func TestShutdownNilRunner(t *testing.T) {
	d := &Daemon{
		sockPath: filepath.Join(t.TempDir(), "test.sock"),
		pidPath:  filepath.Join(t.TempDir(), "test.pid"),
	}
	// Should not panic
	d.shutdown()
}

func TestHandleClient(t *testing.T) {
	mock := newMockRunner()
	mock.onCommand("echo pong", []byte("pong\n"), 0)

	d := &Daemon{
		runner:     mock,
		remotePath: "/tmp/.remote-agent-test",
	}

	// Create a pair of connected sockets
	server, client := net.Pipe()

	go func() {
		// Send request
		json.NewEncoder(client).Encode(protocol.DaemonRequest{Action: "ping"})
		// Read response
		var resp protocol.DaemonResponse
		json.NewDecoder(client).Decode(&resp)
		client.Close()
	}()

	d.handleClient(server)
}

func TestHandleClientInvalidJSON(t *testing.T) {
	d := &Daemon{
		runner:     newMockRunner(),
		remotePath: "/tmp/.remote-agent-test",
	}

	server, client := net.Pipe()

	go func() {
		client.Write([]byte("not json\n"))
		var resp protocol.DaemonResponse
		json.NewDecoder(client).Decode(&resp)
		assert.NotEqual(t, "", resp.Error)

		client.Close()
	}()

	d.handleClient(server)
}

func TestFindDeployBinary(t *testing.T) {
	// This test depends on the environment - we just verify it doesn't panic
	_, err := findDeployBinary()
	// May succeed (finds our test binary) or fail (no linux-amd64 variant)
	_ = err
}

func TestSshRunner(t *testing.T) {
	// Verify the sshRunner struct implements Runner
	var _ Runner = (*sshRunner)(nil)
}
