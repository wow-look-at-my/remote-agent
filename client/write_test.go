package client

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// startCapturingDaemon is like startMockDaemon but also delivers each decoded
// request on the returned channel, so tests can assert on the exact wire params.
func startCapturingDaemon(t *testing.T) (<-chan protocol.DaemonRequest, func()) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	sockPath := filepath.Join(dir, "remote-agent-capture.sock")
	l, err := net.Listen("unix", sockPath)
	require.Nil(t, err)

	reqCh := make(chan protocol.DaemonRequest, 16)
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				var req protocol.DaemonRequest
				if json.NewDecoder(c).Decode(&req) != nil {
					return
				}
				reqCh <- req
				json.NewEncoder(c).Encode(protocol.DaemonResponse{
					OK:   true,
					Data: map[string]any{"bytes_written": 1},
				})
			}(conn)
		}
	}()

	return reqCh, func() { l.Close() }
}

func TestWriteBinaryUsesB64Framing(t *testing.T) {
	reqs, cleanup := startCapturingDaemon(t)
	defer cleanup()

	binary := []byte{0x00, 0x80, 0xff, 0x01} // not valid UTF-8
	withSuppressedStdout(t, func() {
		require.Nil(t, Write("/tmp/blob", "0644", binary))
	})

	req := <-reqs
	require.Equal(t, "write", req.Action)
	b64, ok := req.Params["content_b64"].(string)
	require.True(t, ok, "binary content must be sent as content_b64, params: %v", req.Params)
	decoded, err := base64.StdEncoding.DecodeString(b64)
	require.Nil(t, err)
	assert.Equal(t, binary, decoded, "binary bytes must survive the socket hop exactly")
	_, hasPlain := req.Params["content"]
	assert.False(t, hasPlain, "content and content_b64 must not both be sent")
}

func TestWriteTextUsesPlainContent(t *testing.T) {
	reqs, cleanup := startCapturingDaemon(t)
	defer cleanup()

	withSuppressedStdout(t, func() {
		require.Nil(t, Write("/tmp/greeting", "0644", []byte("hello\n")))
	})

	req := <-reqs
	require.Equal(t, "write", req.Action)
	assert.Equal(t, "hello\n", req.Params["content"])
	_, hasB64 := req.Params["content_b64"]
	assert.False(t, hasB64, "text content must stay human-readable, not base64")
}
