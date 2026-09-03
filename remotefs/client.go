// Package remotefs mounts a remote host's filesystem locally over the
package remotefs

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/wow-look-at-my/remote-agent/fswire"
)

var PingTimeout = 10 * time.Second

var ErrClosed = errors.New("remote filesystem session is closed")

type Client struct {
	writer *fswire.Writer
	closer io.Closer

	mu      sync.Mutex
	pending map[uint64]chan reply
	nextID  uint64
	closed  bool
	err     error // why the session ended, reported to later callers
}

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
		// The client close every failed handshake performs releases the waiting
		return fmt.Errorf("no response from the remote filesystem helper after %s", PingTimeout)
	}
}
