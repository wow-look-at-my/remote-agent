package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

func TestSocketPath(t *testing.T) {
	path := SocketPath("user@host.example.com")
	if path == "" {
		t.Error("socket path should not be empty")
	}
	if SocketPath("user@host.example.com") != path {
		t.Error("socket path should be deterministic")
	}
	if SocketPath("other@host.example.com") == path {
		t.Error("different targets should give different socket paths")
	}
}

func TestPIDPath(t *testing.T) {
	path := PIDPath("user@host.example.com")
	if path == "" {
		t.Error("pid path should not be empty")
	}
	if PIDPath("user@host.example.com") != path {
		t.Error("pid path should be deterministic")
	}
}

func TestSocketAndPIDPathDiffer(t *testing.T) {
	target := "user@host.example.com"
	if SocketPath(target) == PIDPath(target) {
		t.Error("socket path and pid path should be different")
	}
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
		if err != nil {
			t.Errorf("parseTarget(%q) error: %v", tt.input, err)
			continue
		}
		if user != tt.wantUser {
			t.Errorf("parseTarget(%q) user = %q, want %q", tt.input, user, tt.wantUser)
		}
		if host != tt.wantHost {
			t.Errorf("parseTarget(%q) host = %q, want %q", tt.input, host, tt.wantHost)
		}
	}
}

func TestParseTargetNoUser(t *testing.T) {
	user, host, err := parseTarget("server.com")
	if err != nil {
		t.Fatal(err)
	}
	if host != "server.com" {
		t.Errorf("host = %q, want %q", host, "server.com")
	}
	if user == "" {
		t.Error("user should not be empty (should default to current user)")
	}
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
		if got != tt.want {
			t.Errorf("shellEscape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRandomSuffix(t *testing.T) {
	s1 := randomSuffix()
	s2 := randomSuffix()

	if len(s1) != 8 {
		t.Errorf("suffix length = %d, want 8", len(s1))
	}
	if s1 == s2 {
		t.Error("two random suffixes should differ")
	}
}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	pidPath := filepath.Join(dir, "test.pid")

	// Create files to clean up
	os.WriteFile(pidPath, []byte("12345"), 0644)
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}

	d := &Daemon{
		listener: l,
		sockPath: sockPath,
		pidPath:  pidPath,
	}
	d.cleanup()

	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Error("socket should be cleaned up")
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("pid file should be cleaned up")
	}
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

	// Verify audit and cleanup commands were run
	if len(mock.calls) < 2 {
		t.Errorf("expected at least 2 calls (audit + rm), got %d", len(mock.calls))
	}
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
		if resp.Error == "" {
			t.Error("expected error for invalid JSON")
		}
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
