//go:build unix

package sshutil

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Client half of OpenSSH's ControlMaster multiplexing protocol (PROTOCOL.mux,
// version 4), in "passenger" mode: the client asks the master to run a
// command and hands it three file descriptors to use as the command's stdio.
//
// This exists so remote-agent can ride a connection the user already
// authenticated. The master owns the SSH transport -- there is no way to
// borrow it as a raw connection for x/crypto/ssh -- so speaking its socket
// protocol is the only way in.
// see docs/ssh/control-sockets.md
const (
	muxVersion = 4

	muxMsgHello          = 0x00000001
	muxCNewSession       = 0x10000002
	muxCAliveCheck       = 0x10000004
	muxSPermissionDenied = 0x80000002
	muxSFailure          = 0x80000003
	muxSExitMessage      = 0x80000004
	muxSAlive            = 0x80000005
	muxSSessionOpened    = 0x80000006
	muxSTTYAllocFail     = 0x80000008

	// muxNoEscape disables the escape character for a session.
	muxNoEscape = 0xffffffff
	// A master's reply is a few words. Anything larger is a desynchronized stream.
	muxMaxPacket = 256 << 10
	// Bounds the handshake, so a socket whose owner is wedged fails instead of hanging.
	muxDialTimeout = 10 * time.Second
)

// ControlConn runs commands through an OpenSSH control master. Each command takes its
// own connection and its own session, so they run concurrently. see docs/ssh/connection.md
type ControlConn struct {
	path string
	sem  chan struct{} // bounds concurrent sessions, as CommandRunner does
	// MasterPID is the process id the master reported at connect time.
	MasterPID uint32
}

// DialControlMaster probes the master with an alive check, so a stale socket file
// fails here rather than on the first command.
func DialControlMaster(path string) (*ControlConn, error) {
	c := &ControlConn{path: path, sem: make(chan struct{}, maxConcurrentSessions)}
	pid, err := c.aliveCheck()
	if err != nil {
		return nil, err
	}
	c.MasterPID = pid
	return c, nil
}

// ControlMasterAlive reports whether a control master is answering at path.
func ControlMasterAlive(path string) bool {
	_, err := DialControlMaster(path)
	return err == nil
}

// Close releases this side only. The master belongs to whoever started it.
func (c *ControlConn) Close() error { return nil }

// Run executes a command on the master's connection.
func (c *ControlConn) Run(command string) (stdout, stderr []byte, exitCode int, err error) {
	return c.RunStdin(command, nil)
}

// RunStdin executes a command with stdin piped to it. A nil stdin still
// closes the command's stdin immediately, so a command that reads sees EOF
// instead of blocking forever.
func (c *ControlConn) RunStdin(command string, stdin []byte) (stdout, stderr []byte, exitCode int, err error) {
	c.sem <- struct{}{}
	defer func() { <-c.sem }()

	sess, err := c.openSession(command)
	if err != nil {
		return nil, nil, -1, err
	}
	defer sess.Close()

	var outBuf, errBuf safeBuffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(&outBuf, sess.stdout) }()
	go func() { defer wg.Done(); io.Copy(&errBuf, sess.stderr) }()

	if len(stdin) > 0 {
		if _, err := sess.stdin.Write(stdin); err != nil {
			return nil, nil, -1, fmt.Errorf("write stdin: %w", err)
		}
	}
	// EOF on stdin, from a half-close: the command's other two fds stay open.
	sess.stdin.CloseWrite()

	exitCode, err = sess.wait()
	// The master closes the stdio fds as the session ends, so these finish.
	wg.Wait()
	return outBuf.Bytes(), errBuf.Bytes(), exitCode, err
}

// StartStream runs a command whose stdin and stdout stay open as a
// bidirectional byte stream -- the transport a filesystem mount rides on.
func (c *ControlConn) StartStream(command string) (io.ReadWriteCloser, error) {
	sess, err := c.openSession(command)
	if err != nil {
		return nil, err
	}
	// The helper reports failures in-band, and a full stderr socket stalls the session.
	go io.Copy(io.Discard, sess.stderr)
	return sess, nil
}

// One command on the master: its control connection, plus our ends of the stdio socketpairs.
type muxSession struct {
	ctl       *net.UnixConn
	sessionID uint32
	stdin     *net.UnixConn
	stdout    *net.UnixConn
	stderr    *net.UnixConn

	closeOnce sync.Once
}

// Read receives the command's stdout, for a session used as a stream.
func (s *muxSession) Read(p []byte) (int, error) { return s.stdout.Read(p) }

// Write sends bytes to the command's stdin, for a session used as a stream.
func (s *muxSession) Write(p []byte) (int, error) { return s.stdin.Write(p) }

// Close ends the session. Closing the control connection is what tells the
// master to tear the session down.
func (s *muxSession) Close() error {
	s.closeOnce.Do(func() {
		s.stdin.Close()
		s.stdout.Close()
		s.stderr.Close()
		s.ctl.Close()
	})
	return nil
}

// wait reads the control connection until the master closes it, returning the
// command's exit status. PROTOCOL.mux requires the client to stay until that
// close: hanging up early makes the master tear the session down, which can
// discard output still in flight.
func (s *muxSession) wait() (int, error) {
	exit, seen := -1, false
	for {
		body, err := readMuxPacket(s.ctl)
		if err != nil {
			if errors.Is(err, io.EOF) || isClosedConn(err) {
				break
			}
			return -1, fmt.Errorf("control master read: %w", err)
		}
		r := muxReader{body: body}
		msgType, err := r.u32()
		if err != nil {
			return -1, err
		}
		switch msgType {
		case muxSExitMessage:
			if _, err := r.u32(); err != nil { // session id
				return -1, err
			}
			v, err := r.u32()
			if err != nil {
				return -1, err
			}
			exit, seen = int(v), true
		case muxSTTYAllocFail:
			// No tty was requested, so this is nothing to act on.
		default:
			return -1, fmt.Errorf("control master returned error 0x%08x: %s", msgType, r.errString())
		}
	}
	if !seen {
		return -1, fmt.Errorf("control master closed the session without an exit status (master gone?)")
	}
	return exit, nil
}

// openSession asks the master to run command, handing it socketpairs for the
// command's stdin, stdout and stderr.
func (c *ControlConn) openSession(command string) (*muxSession, error) {
	ctl, err := dialControlSocket(c.path)
	if err != nil {
		return nil, err
	}
	sess := &muxSession{ctl: ctl}
	ok := false
	defer func() {
		if !ok {
			sess.closeStarted()
		}
	}()

	if err := muxHello(ctl); err != nil {
		return nil, err
	}

	req := putU32(nil, muxCNewSession)
	req = putU32(req, 0)     // request id
	req = putString(req, "") // reserved
	req = putU32(req, 0)     // want tty
	req = putU32(req, 0)     // want X11 forwarding
	req = putU32(req, 0)     // want agent forwarding
	req = putU32(req, 0)     // subsystem
	req = putU32(req, muxNoEscape)
	req = putString(req, "") // terminal type
	req = putString(req, command)
	// No environment strings: the daemon's own environment is not the
	// session's, and the master applies the user's SendEnv/SetEnv itself.
	if err := writeMuxPacket(ctl, req); err != nil {
		return nil, fmt.Errorf("control master write: %w", err)
	}

	// The master takes over the far ends; ours stay for the command's stdio.
	// They go in the order the protocol names them: stdin, stdout, stderr.
	for _, p := range []struct {
		name string
		dst  **net.UnixConn
	}{{"stdin", &sess.stdin}, {"stdout", &sess.stdout}, {"stderr", &sess.stderr}} {
		ours, theirs, err := socketPair(p.name)
		if err != nil {
			return nil, err
		}
		*p.dst = ours
		err = sendFD(ctl, theirs)
		unix.Close(theirs)
		if err != nil {
			return nil, fmt.Errorf("pass %s to control master: %w", p.name, err)
		}
	}

	body, err := readMuxPacket(ctl)
	if err != nil {
		return nil, fmt.Errorf("control master reply: %w", err)
	}
	r := muxReader{body: body}
	msgType, err := r.u32()
	if err != nil {
		return nil, err
	}
	if _, err := r.u32(); err != nil { // request id, echoed back
		return nil, err
	}
	switch msgType {
	case muxSSessionOpened:
		sid, err := r.u32()
		if err != nil {
			return nil, err
		}
		sess.sessionID = sid
	case muxSPermissionDenied:
		return nil, fmt.Errorf("control master refused the session: %s", r.errString())
	case muxSFailure:
		return nil, fmt.Errorf("control master could not open a session: %s", r.errString())
	default:
		return nil, fmt.Errorf("unexpected reply 0x%08x from control master", msgType)
	}

	ok = true
	return sess, nil
}

// closeStarted releases whatever of a half-built session exists.
func (s *muxSession) closeStarted() {
	for _, c := range []*net.UnixConn{s.stdin, s.stdout, s.stderr} {
		if c != nil {
			c.Close()
		}
	}
	s.ctl.Close()
}

// aliveCheck asks the master to identify itself, returning its process id.
func (c *ControlConn) aliveCheck() (uint32, error) {
	ctl, err := dialControlSocket(c.path)
	if err != nil {
		return 0, err
	}
	defer ctl.Close()

	if err := muxHello(ctl); err != nil {
		return 0, err
	}
	req := putU32(nil, muxCAliveCheck)
	req = putU32(req, 0) // request id
	if err := writeMuxPacket(ctl, req); err != nil {
		return 0, fmt.Errorf("control socket %s: alive check: %w", c.path, err)
	}
	body, err := readMuxPacket(ctl)
	if err != nil {
		return 0, fmt.Errorf("control socket %s: alive check: %w", c.path, err)
	}
	r := muxReader{body: body}
	msgType, err := r.u32()
	if err != nil {
		return 0, err
	}
	if _, err := r.u32(); err != nil { // request id
		return 0, err
	}
	if msgType != muxSAlive {
		return 0, fmt.Errorf("control socket %s: master answered 0x%08x, not an alive reply: %s", c.path, msgType, r.errString())
	}
	return r.u32()
}

// muxHello performs the version handshake every mux connection opens with.
func muxHello(ctl *net.UnixConn) error {
	hello := putU32(nil, muxMsgHello)
	hello = putU32(hello, muxVersion)
	if err := writeMuxPacket(ctl, hello); err != nil {
		return fmt.Errorf("control master hello: %w", err)
	}
	body, err := readMuxPacket(ctl)
	if err != nil {
		return fmt.Errorf("control master hello: %w", err)
	}
	r := muxReader{body: body}
	msgType, err := r.u32()
	if err != nil {
		return err
	}
	if msgType != muxMsgHello {
		return fmt.Errorf("control master greeted with 0x%08x, not a hello", msgType)
	}
	version, err := r.u32()
	if err != nil {
		return err
	}
	if version != muxVersion {
		return fmt.Errorf("control master speaks mux version %d, this client speaks %d", version, muxVersion)
	}
	return nil
}

// dialControlSocket connects to a control socket, with a deadline covering
// the handshake that follows.
func dialControlSocket(path string) (*net.UnixConn, error) {
	conn, err := net.DialTimeout("unix", path, muxDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("control socket %s: %w", path, err)
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("control socket %s: not a unix socket", path)
	}
	return unixConn, nil
}

// socketPair returns a connected pair: our end as a conn, theirs as a raw fd
// ready to be passed to the master.
func socketPair(name string) (ours *net.UnixConn, theirs int, err error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, -1, fmt.Errorf("socketpair for %s: %w", name, err)
	}
	f := os.NewFile(uintptr(fds[0]), "mux-"+name)
	conn, err := net.FileConn(f) // dups the descriptor
	f.Close()
	if err != nil {
		unix.Close(fds[1])
		return nil, -1, fmt.Errorf("wrap %s socket: %w", name, err)
	}
	return conn.(*net.UnixConn), fds[1], nil
}

// One byte of payload carrying SCM_RIGHTS, the way OpenSSH's mm_send_fd does it.
func sendFD(ctl *net.UnixConn, fd int) error {
	_, _, err := ctl.WriteMsgUnix([]byte{0}, unix.UnixRights(fd), nil)
	return err
}

// writeMuxPacket frames one message: a uint32 length, then the body.
func writeMuxPacket(w io.Writer, body []byte) error {
	buf := binary.BigEndian.AppendUint32(make([]byte, 0, 4+len(body)), uint32(len(body)))
	_, err := w.Write(append(buf, body...))
	return err
}

// readMuxPacket reads one length-prefixed message.
func readMuxPacket(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > muxMaxPacket {
		return nil, fmt.Errorf("control master sent a %d byte packet, over the %d byte limit", n, muxMaxPacket)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// muxReader walks the fields of a received packet.
type muxReader struct{ body []byte }

func (r *muxReader) u32() (uint32, error) {
	if len(r.body) < 4 {
		return 0, fmt.Errorf("truncated packet from control master")
	}
	v := binary.BigEndian.Uint32(r.body[:4])
	r.body = r.body[4:]
	return v, nil
}

func (r *muxReader) str() (string, error) {
	n, err := r.u32()
	if err != nil {
		return "", err
	}
	if uint32(len(r.body)) < n {
		return "", fmt.Errorf("truncated string from control master")
	}
	s := string(r.body[:n])
	r.body = r.body[n:]
	return s, nil
}

// errString reads the reason an error reply carries, for use in a message
// that is already reporting a failure.
func (r *muxReader) errString() string {
	s, err := r.str()
	if err != nil || s == "" {
		return "no reason given"
	}
	return s
}

func putU32(b []byte, v uint32) []byte { return binary.BigEndian.AppendUint32(b, v) }

func putString(b []byte, s string) []byte {
	return append(binary.BigEndian.AppendUint32(b, uint32(len(s))), s...)
}

// isClosedConn reports whether an error is this side having closed the
// connection, which reads the same as a clean end of session.
func isClosedConn(err error) bool {
	return errors.Is(err, net.ErrClosed)
}
