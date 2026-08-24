package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/sshutil"
	"golang.org/x/crypto/ssh"
)

// The fake remote helper path in audit commands.
const benchRemotePath = "/tmp/.remote-agent-test"

// startBenchSSHServer starts a minimal SSH server that accepts exec requests.
// Behavior by command:
//   - "echo pong"                                    -> writes "pong\n", exit 0
//   - stdin-consuming commands ("cat > ", base64 -d) -> drains stdin, exit 0
//   - anything else                                  -> no output, exit 0
func startBenchSSHServer(tb testing.TB) (addr string, cleanup func()) {
	tb.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(tb, err)
	hostKey, err := ssh.NewSignerFromKey(priv)
	require.NoError(tb, err)

	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(hostKey)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(tb, err)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveBenchConn(conn, config)
		}
	}()

	return listener.Addr().String(), func() { listener.Close() }
}

func serveBenchConn(conn net.Conn, config *ssh.ServerConfig) {
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer sshConn.Close()
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
		go serveBenchSession(ch, requests)
	}
}

func serveBenchSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			req.Reply(false, nil)
			continue
		}
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

		switch {
		case cmd == "echo pong":
			ch.Write([]byte("pong\n"))
		case strings.HasPrefix(cmd, "cat > "), strings.Contains(cmd, "base64 -d"):
			io.Copy(io.Discard, ch) // drain stdin until client EOF
		}
		ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
		ch.CloseWrite()
		return
	}
}

// delayLine forwards bytes from src to dst, delivering each chunk one-way
// delay after it was read. Chunks already in flight do not delay each other,
// so pipelined traffic is modeled faithfully (like a real network path).
func delayLine(dst, src net.Conn, delay time.Duration) {
	type chunk struct {
		data []byte
		due  time.Time
	}
	ch := make(chan chunk, 4096)
	go func() {
		for c := range ch {
			time.Sleep(time.Until(c.due))
			if _, err := dst.Write(c.data); err != nil {
				break
			}
		}
		dst.Close()
	}()
	buf := make([]byte, 64*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			ch <- chunk{data: data, due: time.Now().Add(delay)}
		}
		if err != nil {
			close(ch)
			return
		}
	}
}

// startDelayProxy listens locally and forwards connections to backendAddr with
// the given one-way delay applied in each direction (RTT = 2*delay).
func startDelayProxy(tb testing.TB, backendAddr string, delay time.Duration) (addr string, cleanup func()) {
	tb.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(tb, err)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			backend, err := net.Dial("tcp", backendAddr)
			if err != nil {
				conn.Close()
				continue
			}
			go delayLine(backend, conn, delay)
			go delayLine(conn, backend, delay)
		}
	}()

	return listener.Addr().String(), func() { listener.Close() }
}

// newBenchDaemon dials the given SSH address and returns a daemon handler
// wired to a real SSH runner, plus a cleanup func.
func newBenchDaemon(tb testing.TB, addr string) (*Handler, func()) {
	tb.Helper()
	config := &ssh.ClientConfig{
		User:            "bench",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	client, err := ssh.Dial("tcp", addr, config)
	require.NoError(tb, err)

	runner := sshutil.NewCommandRunner(client)
	d := &Daemon{
		runner:     runner,
		remotePath: benchRemotePath,
	}
	return &Handler{daemon: d}, func() {
		runner.Close()
		client.Close()
	}
}

// TestHandleExecOverRealSSH asserts stdout and exit code survive the real SSH
// transport end-to-end (the rest of the suite uses mock runners only).
func TestHandleExecOverRealSSH(t *testing.T) {
	addr, stopServer := startBenchSSHServer(t)
	defer stopServer()

	h, closeClient := newBenchDaemon(t, addr)
	defer closeClient()

	resp := h.handleExec(map[string]any{"command": "echo pong"})
	require.Empty(t, resp.Error)
	require.True(t, resp.OK)

	// Through JSON, like the socket, so the assertion sees what a client sees.
	raw, err := json.Marshal(resp.Data)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	assert.Equal(t, "pong\n", m["stdout"])
	assert.Equal(t, float64(0), m["exit_code"])
}

// benchExecOnce runs one exec through the handler and fails the benchmark on error.
func benchExecOnce(tb testing.TB, h *Handler) {
	resp := h.handleExec(map[string]any{"command": "true"})
	if resp.Error != "" {
		tb.Fatalf("exec failed: %s", resp.Error)
	}
}

// Half an RTT. A real RTT is 10-100x larger, and the overhead scales linearly with it.
const oneWayDelay = 2 * time.Millisecond

// BenchmarkExecSequentialRTT measures the wall time of sequential execs over a
// connection with synthetic latency. ns/op divided by the 4ms RTT approximates
// the number of network round trips each exec costs.
func BenchmarkExecSequentialRTT(b *testing.B) {
	serverAddr, stopServer := startBenchSSHServer(b)
	defer stopServer()
	proxyAddr, stopProxy := startDelayProxy(b, serverAddr, oneWayDelay)
	defer stopProxy()

	h, closeClient := newBenchDaemon(b, proxyAddr)
	defer closeClient()

	// Warm up the connection.
	benchExecOnce(b, h)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchExecOnce(b, h)
	}
}

// BenchmarkExecParallel4RTT measures 4 goroutines issuing execs concurrently
// (as parallel agent tool calls do) over a connection with synthetic latency.
// ns/op is per exec.
func BenchmarkExecParallel4RTT(b *testing.B) {
	serverAddr, stopServer := startBenchSSHServer(b)
	defer stopServer()
	proxyAddr, stopProxy := startDelayProxy(b, serverAddr, oneWayDelay)
	defer stopProxy()

	h, closeClient := newBenchDaemon(b, proxyAddr)
	defer closeClient()

	benchExecOnce(b, h)

	const workers = 4
	b.ResetTimer()
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for i := offset; i < b.N; i += workers {
				benchExecOnce(b, h)
			}
		}(w)
	}
	wg.Wait()
}

// BenchmarkRawSSHCommandRTT measures a bare sshutil.RunCommand round trip
// (session open + exec + exit) with synthetic latency, without any daemon
// bookkeeping. This isolates the transport cost from the audit overhead.
func BenchmarkRawSSHCommandRTT(b *testing.B) {
	serverAddr, stopServer := startBenchSSHServer(b)
	defer stopServer()
	proxyAddr, stopProxy := startDelayProxy(b, serverAddr, oneWayDelay)
	defer stopProxy()

	config := &ssh.ClientConfig{
		User:            "bench",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	client, err := ssh.Dial("tcp", proxyAddr, config)
	require.NoError(b, err)
	defer client.Close()

	_, _, _, err = sshutil.RunCommand(client, "true")
	require.Nil(b, err)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, code, err := sshutil.RunCommand(client, "true")
		require.False(b, err != nil || code != 0)

	}
}
