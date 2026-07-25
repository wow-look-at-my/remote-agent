// Package remotefs mounts a remote host's filesystem locally over the
// daemon's SSH connection, so that *every* program on the local machine --
// not just tools that know about remote-agent -- reads and writes remote
// files through ordinary paths.
//
// That universality is the point. A tool-by-tool bridge only ever covers the
// tools it was written for; a mount covers editors, compilers, language
// servers, and any agent tool that has not been written yet, because they all
// go through the kernel's filesystem interface.
package remotefs

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/wow-look-at-my/remote-agent/fswire"
)

// PingTimeout bounds the handshake with the remote helper. Ordinary
// filesystem calls are deliberately unbounded (a large read over a slow link
// legitimately takes a while), but the handshake must not be: a helper that
// accepts the stream and never answers would otherwise hang the mount request
// -- and the caller waiting on it -- forever. Overridable in tests.
var PingTimeout = 10 * time.Second

// ErrClosed is returned once the session to the remote helper has ended.
var ErrClosed = errors.New("remote filesystem session is closed")

// Client multiplexes filesystem requests to the remote helper over a single
// stream. Calls from many FUSE threads are in flight at once and replies
// arrive in whatever order the remote finishes them, so each is matched back
// to its caller by request ID.
type Client struct {
	writer *fswire.Writer
	closer io.Closer

	mu      sync.Mutex
	pending map[uint64]chan reply
	nextID  uint64
	closed  bool
	err     error // why the session ended, reported to later callers
}

// reply is one completed response handed back to the waiting caller.
type reply struct {
	resp    *fswire.Response
	payload []byte
}

// New starts a client that writes requests to w, reads replies from r, and
// closes closer (the underlying session) when the client is closed. The read
// loop runs until the stream ends.
func New(w io.Writer, r io.Reader, closer io.Closer) *Client {
	c := &Client{
		writer:  fswire.NewWriter(w),
		closer:  closer,
		pending: map[uint64]chan reply{},
	}
	go c.readLoop(fswire.NewReader(r))
	return c
}

// Call sends one request and waits for its reply, returning the response
// header and any binary payload (file data for reads).
func (c *Client) Call(req *fswire.Request, payload []byte) (*fswire.Response, []byte, error) {
	ch := make(chan reply, 1)

	c.mu.Lock()
	if c.closed {
		err := c.err
		c.mu.Unlock()
		if err == nil {
			err = ErrClosed
		}
		return nil, nil, err
	}
	c.nextID++
	req.ID = c.nextID
	c.pending[req.ID] = ch
	c.mu.Unlock()

	if err := c.writer.WriteFrame(req, payload); err != nil {
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
		return nil, nil, err
	}

	r, ok := <-ch
	if !ok {
		return nil, nil, c.sessionErr()
	}
	return r.resp, r.payload, nil
}

// readLoop dispatches replies to their waiting callers until the stream ends,
// then fails every outstanding and future call rather than leaving FUSE
// threads blocked forever on a dead connection.
func (c *Client) readLoop(reader *fswire.Reader) {
	for {
		var resp fswire.Response
		payload, err := reader.ReadFrame(&resp)
		if err != nil {
			c.shutdown(err)
			return
		}

		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.mu.Unlock()
		if !ok {
			continue // a reply to a call that already gave up
		}
		ch <- reply{resp: &resp, payload: payload}
	}
}

// shutdown records why the session ended and wakes every waiting caller.
func (c *Client) shutdown(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if err != nil && !errors.Is(err, io.EOF) {
		c.err = fmt.Errorf("remote filesystem session ended: %w", err)
	} else {
		c.err = ErrClosed
	}
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

// Close ends the session with the remote helper.
func (c *Client) Close() error {
	c.shutdown(nil)
	if c.closer != nil {
		return c.closer.Close()
	}
	return nil
}

// sessionErr reports why the session ended.
func (c *Client) sessionErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	return ErrClosed
}

// Ping checks that the remote helper is answering, within PingTimeout.
func (c *Client) Ping() error {
	type outcome struct {
		resp *fswire.Response
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		resp, _, err := c.Call(&fswire.Request{Op: fswire.OpPing}, nil)
		done <- outcome{resp: resp, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			return got.err
		}
		if got.resp.Errno != 0 {
			return fmt.Errorf("remote filesystem helper returned errno %d", got.resp.Errno)
		}
		return nil
	case <-time.After(PingTimeout):
		// The waiting goroutine is released when the caller closes the client,
		// which is what every failed-handshake path does.
		return fmt.Errorf("no response from the remote filesystem helper after %s", PingTimeout)
	}
}
