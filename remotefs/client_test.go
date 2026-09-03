package remotefs

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/remote-agent/fswire"
)

// echoServer answers requests out of order, which is exactly what the real
// remote does: replies come back as operations finish, not as they arrived.
func echoServer(t *testing.T, conn net.Conn, delay func(id uint64) time.Duration) {
	t.Helper()
	r := fswire.NewReader(conn)
	w := fswire.NewWriter(conn)
	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		var req fswire.Request
		payload, err := r.ReadFrame(&req)
		if err != nil {
			return
		}
		wg.Add(1)
		go func(req fswire.Request, payload []byte) {
			defer wg.Done()
			if delay != nil {
				time.Sleep(delay(req.ID))
			}
			_ = w.WriteFrame(&fswire.Response{ID: req.ID, Target: req.Path}, payload)
		}(req, payload)
	}
}

func TestClientMatchesRepliesToCallers(t *testing.T) {
	clientEnd, serverEnd := net.Pipe()
	go echoServer(t, serverEnd, func(id uint64) time.Duration {
		return time.Duration(20-id) * time.Millisecond
	})
	c := New(clientEnd, clientEnd, clientEnd)
	defer c.Close()

	var wg sync.WaitGroup
	for i := 1; i <= 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := string(rune('a' + i))
			resp, payload, err := c.Call(&fswire.Request{Op: fswire.OpStat, Path: path}, []byte(path))
			require.NoError(t, err)
			assert.Equal(t, path, resp.Target, "reply must match the caller's request")
			assert.Equal(t, path, string(payload))
		}(i)
	}
	wg.Wait()
}

func TestClientPing(t *testing.T) {
	clientEnd, serverEnd := net.Pipe()
	go echoServer(t, serverEnd, nil)
	c := New(clientEnd, clientEnd, clientEnd)
	defer c.Close()

	assert.NoError(t, c.Ping())
}

func TestClientReportsRemoteErrno(t *testing.T) {
	clientEnd, serverEnd := net.Pipe()
	go func() {
		r := fswire.NewReader(serverEnd)
		w := fswire.NewWriter(serverEnd)
		for {
			var req fswire.Request
			if _, err := r.ReadFrame(&req); err != nil {
				return
			}
			_ = w.WriteFrame(&fswire.Response{ID: req.ID, Errno: 2}, nil)
		}
	}()
	c := New(clientEnd, clientEnd, clientEnd)
	defer c.Close()

	// An errno is data, not a transport failure, so Call succeeds and the caller
	resp, _, err := c.Call(&fswire.Request{Op: fswire.OpStat, Path: "x"}, nil)
	require.NoError(t, err)
	assert.Equal(t, uint32(2), resp.Errno)
	assert.Error(t, c.Ping(), "Ping must fail when the helper reports an errno")
}

func TestClientFailsCallsAfterSessionEnds(t *testing.T) {
	clientEnd, serverEnd := net.Pipe()
	c := New(clientEnd, clientEnd, clientEnd)

	// A dead connection must fail an in-flight call.
	inFlight := make(chan error, 1)
	go func() {
		_, _, err := c.Call(&fswire.Request{Op: fswire.OpStat, Path: "x"}, nil)
		inFlight <- err
	}()

	// Let the request reach the pipe, then drop the far end.
	go func() {
		r := fswire.NewReader(serverEnd)
		var req fswire.Request
		_, _ = r.ReadFrame(&req)
		serverEnd.Close()
	}()

	select {
	case err := <-inFlight:
		assert.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("call did not fail after the session ended")
	}

	// Later calls fail immediately with the same reason.
	_, _, err := c.Call(&fswire.Request{Op: fswire.OpStat, Path: "y"}, nil)
	assert.Error(t, err)
}

func TestClientCallAfterCloseFails(t *testing.T) {
	clientEnd, serverEnd := net.Pipe()
	go echoServer(t, serverEnd, nil)
	c := New(clientEnd, clientEnd, clientEnd)
	require.NoError(t, c.Close())

	_, _, err := c.Call(&fswire.Request{Op: fswire.OpStat}, nil)
	assert.ErrorIs(t, err, ErrClosed)
}

func TestClientCloseIsIdempotent(t *testing.T) {
	clientEnd, serverEnd := net.Pipe()
	go echoServer(t, serverEnd, nil)
	c := New(clientEnd, clientEnd, clientEnd)
	assert.NoError(t, c.Close())
	_ = c.Close()
}

func TestClientAssignsUniqueIDs(t *testing.T) {
	clientEnd, serverEnd := net.Pipe()

	ids := make(chan uint64, 4)
	go func() {
		r := fswire.NewReader(serverEnd)
		w := fswire.NewWriter(serverEnd)
		for {
			var req fswire.Request
			if _, err := r.ReadFrame(&req); err != nil {
				return
			}
			ids <- req.ID
			_ = w.WriteFrame(&fswire.Response{ID: req.ID}, nil)
		}
	}()

	c := New(clientEnd, clientEnd, clientEnd)
	defer c.Close()
	for i := 0; i < 4; i++ {
		_, _, err := c.Call(&fswire.Request{Op: fswire.OpPing}, nil)
		require.NoError(t, err)
	}

	seen := set.New[uint64]()
	for i := 0; i < 4; i++ {
		id := <-ids
		assert.True(t, seen.Add(id), "request ids must be unique")
	}
}

func TestClientWriteFailureIsReported(t *testing.T) {
	// A stream that rejects writes, a closed SSH channel, is an error and not a
	c := New(brokenWriter{}, io.LimitReader(nil, 0), nil)
	_, _, err := c.Call(&fswire.Request{Op: fswire.OpPing}, nil)
	assert.Error(t, err)
}

type brokenWriter struct{}

func (brokenWriter) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }
