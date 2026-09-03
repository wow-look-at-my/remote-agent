//go:build unix

package sshutil

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// fakeMaster is a stand-in for `ssh -M`: it speaks the mux protocol and runs
// each requested command locally on the descriptors the client passes, which
// is the whole of what a real master does from this client's point of view.
type fakeMaster struct {
	path string

	mu       sync.Mutex
	commands []string // every command it was asked to run

	// failWith, when set, answers session requests with MUX_S_FAILURE.
	failWith string
	// helloVersion overrides the protocol version it claims to speak.
	helloVersion uint32
	// dropExitMessage closes the session without reporting an exit status.
	dropExitMessage bool
}

func startFakeMaster(t *testing.T, m *fakeMaster) *fakeMaster {
	t.Helper()
	if m == nil {
		m = &fakeMaster{}
	}
	if m.helloVersion == 0 {
		m.helloVersion = muxVersion
	}
	dir, err := os.MkdirTemp("", "muxtest")
	require.NoError(t, err)
	m.path = filepath.Join(dir, "master.sock")

	l, err := net.Listen("unix", m.path)
	require.NoError(t, err)
	t.Cleanup(func() { l.Close(); os.RemoveAll(dir) })

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go m.serve(conn.(*net.UnixConn))
		}
	}()
	return m
}

func (m *fakeMaster) ranCommands() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.commands...)
}

func (m *fakeMaster) serve(conn *net.UnixConn) {
	defer conn.Close()

	// Hello exchange.
	if _, err := readMuxPacket(conn); err != nil {
		return
	}
	hello := putU32(nil, muxMsgHello)
	hello = putU32(hello, m.helloVersion)
	if writeMuxPacket(conn, hello) != nil || m.helloVersion != muxVersion {
		return
	}

	body, err := readMuxPacket(conn)
	if err != nil {
		return
	}
	r := muxReader{body: body}
	msgType, _ := r.u32()
	rid, _ := r.u32()

	switch msgType {
	case muxCAliveCheck:
		reply := putU32(nil, muxSAlive)
		reply = putU32(reply, rid)
		reply = putU32(reply, uint32(os.Getpid()))
		writeMuxPacket(conn, reply)
	case muxCNewSession:
		m.serveSession(conn, rid, &r)
	}
}

func (m *fakeMaster) serveSession(conn *net.UnixConn, rid uint32, r *muxReader) {
	r.str()           // reserved
	tty, _ := r.u32() // want tty
	r.u32()           // x11
	r.u32()           // agent
	r.u32()           // subsystem
	r.u32()           // escape char
	r.str()           // terminal type
	command, _ := r.str()

	m.mu.Lock()
	m.commands = append(m.commands, command)
	m.mu.Unlock()

	files := make([]*os.File, 3)
	for i, name := range []string{"stdin", "stdout", "stderr"} {
		f, err := recvFD(conn, name)
		if err != nil {
			return
		}
		files[i] = f
	}
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()

	if m.failWith != "" {
		reply := putU32(nil, muxSFailure)
		reply = putU32(reply, rid)
		reply = putString(reply, m.failWith)
		writeMuxPacket(conn, reply)
		return
	}

	opened := putU32(nil, muxSSessionOpened)
	opened = putU32(opened, rid)
	opened = putU32(opened, 1) // session id
	if writeMuxPacket(conn, opened) != nil {
		return
	}
	if tty != 0 { // never requested by this client, but a master may say so
		fail := putU32(nil, muxSTTYAllocFail)
		fail = putU32(fail, 1)
		writeMuxPacket(conn, fail)
	}

	cmd := exec.Command("sh", "-c", command)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = files[0], files[1], files[2]
	err := cmd.Run()
	// Closing the master's ends is what gives the client EOF on the command's
	// output, exactly as a real master does when the session ends.
	for _, f := range files {
		f.Close()
	}

	exitCode := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exitCode = ee.ExitCode()
	} else if err != nil {
		exitCode = 255
	}
	if !m.dropExitMessage {
		msg := putU32(nil, muxSExitMessage)
		msg = putU32(msg, 1)
		msg = putU32(msg, uint32(exitCode))
		writeMuxPacket(conn, msg)
	}
}

func recvFD(conn *net.UnixConn, name string) (*os.File, error) {
	buf := make([]byte, 1)
	oob := make([]byte, unix.CmsgSpace(4))
	_, oobn, _, _, err := conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return nil, err
	}
	scms, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(scms) == 0 {
		return nil, fmt.Errorf("no control message for %s: %v", name, err)
	}
	fds, err := unix.ParseUnixRights(&scms[0])
	if err != nil || len(fds) == 0 {
		return nil, fmt.Errorf("no descriptor for %s: %v", name, err)
	}
	return os.NewFile(uintptr(fds[0]), name), nil
}

func TestControlMasterRun(t *testing.T) {
	m := startFakeMaster(t, nil)
	c, err := DialControlMaster(m.path)
	require.NoError(t, err)
	defer c.Close()
	assert.Equal(t, uint32(os.Getpid()), c.MasterPID)

	stdout, stderr, code, err := c.Run("echo out; echo err >&2")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "out\n", string(stdout))
	assert.Equal(t, "err\n", string(stderr))
	assert.Equal(t, []string{"echo out; echo err >&2"}, m.ranCommands())
}

func TestControlMasterExitCode(t *testing.T) {
	c, err := DialControlMaster(startFakeMaster(t, nil).path)
	require.NoError(t, err)
	_, _, code, err := c.Run("exit 42")
	require.NoError(t, err)
	assert.Equal(t, 42, code)
}

func TestControlMasterStdin(t *testing.T) {
	c, err := DialControlMaster(startFakeMaster(t, nil).path)
	require.NoError(t, err)

	// Binary and larger than a socket buffer, like the helper the deploy path
	// ships.
	payload := make([]byte, 1<<20)
	for i := range payload {
		payload[i] = byte(i)
	}
	stdout, _, code, err := c.RunStdin("cat", payload)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, payload, stdout)
}

func TestControlMasterEmptyStdinIsClosed(t *testing.T) {
	c, err := DialControlMaster(startFakeMaster(t, nil).path)
	require.NoError(t, err)
	// A command that reads stdin must see EOF rather than hang forever.
	stdout, _, code, err := c.Run("wc -c")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "0", strings.TrimSpace(string(stdout)))
}

func TestControlMasterStream(t *testing.T) {
	c, err := DialControlMaster(startFakeMaster(t, nil).path)
	require.NoError(t, err)

	s, err := c.StartStream("cat")
	require.NoError(t, err)
	defer s.Close()
	for i := range 3 {
		msg := fmt.Sprintf("frame-%d\n", i)
		_, err := s.Write([]byte(msg))
		require.NoError(t, err)
		buf := make([]byte, len(msg))
		n, err := s.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, msg, string(buf[:n]))
	}
}

func TestControlMasterConcurrentCommands(t *testing.T) {
	m := startFakeMaster(t, nil)
	c, err := DialControlMaster(m.path)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stdout, _, code, err := c.Run(fmt.Sprintf("echo %d", i))
			assert.NoError(t, err)
			assert.Equal(t, 0, code)
			assert.Equal(t, fmt.Sprintf("%d\n", i), string(stdout))
		}()
	}
	wg.Wait()
	assert.Len(t, m.ranCommands(), 12)
}

func TestControlMasterNoSocket(t *testing.T) {
	_, err := DialControlMaster(filepath.Join(t.TempDir(), "absent.sock"))
	assert.ErrorContains(t, err, "absent.sock")
	assert.False(t, ControlMasterAlive(filepath.Join(t.TempDir(), "absent.sock")))
}

func TestControlMasterVersionMismatch(t *testing.T) {
	m := startFakeMaster(t, &fakeMaster{helloVersion: 99})
	_, err := DialControlMaster(m.path)
	assert.ErrorContains(t, err, "mux version 99")
}

func TestControlMasterRefusesSession(t *testing.T) {
	m := startFakeMaster(t, &fakeMaster{failWith: "no more sessions"})
	c, err := DialControlMaster(m.path)
	require.NoError(t, err)

	_, _, _, err = c.Run("true")
	assert.ErrorContains(t, err, "no more sessions")
}

func TestControlMasterWithoutExitStatusFails(t *testing.T) {
	m := startFakeMaster(t, &fakeMaster{dropExitMessage: true})
	c, err := DialControlMaster(m.path)
	require.NoError(t, err)

	stdout, _, code, err := c.Run("echo partial")
	assert.ErrorContains(t, err, "without an exit status")
	assert.Equal(t, -1, code)
	assert.Equal(t, "partial\n", string(stdout), "output received before the master vanished is still returned")
}

func TestControlMasterAlive(t *testing.T) {
	assert.True(t, ControlMasterAlive(startFakeMaster(t, nil).path))
}

func TestConnectViaControlMaster(t *testing.T) {
	m := startFakeMaster(t, nil)
	conn, err := Connect(ConnectOptions{Host: "example.invalid", Port: 22, User: "root", ControlPath: m.path})
	require.NoError(t, err)
	defer conn.Conn.Close()

	assert.Equal(t, m.path, conn.ControlPath)
	assert.Empty(t, conn.Fingerprint, "no key of ours is used through a master")
	stdout, _, _, err := conn.Conn.Run("echo via-master")
	require.NoError(t, err)
	assert.Equal(t, "via-master\n", string(stdout))
}

func TestConnectRequiredControlMasterMissing(t *testing.T) {
	_, err := Connect(ConnectOptions{
		Host:           "127.0.0.1",
		Port:           1,
		User:           "root",
		ControlPath:    filepath.Join(t.TempDir(), "absent.sock"),
		RequireControl: true,
	})
	assert.ErrorContains(t, err, "control master requested but unusable")
}

func TestLiveControlMaster(t *testing.T) {
	path := os.Getenv("REMOTE_AGENT_LIVE_CONTROL_PATH")
	if path == "" {
		t.Skip("set REMOTE_AGENT_LIVE_CONTROL_PATH to a live control socket to run this")
	}
	c, err := DialControlMaster(path)
	require.NoError(t, err)
	t.Logf("master pid %d", c.MasterPID)

	stdout, stderr, code, err := c.Run("echo out; echo err >&2; exit 3")
	require.NoError(t, err)
	assert.Equal(t, 3, code)
	assert.Equal(t, "out\n", string(stdout))
	assert.Equal(t, "err\n", string(stderr))

	stdout, _, _, err = c.RunStdin("cat", []byte("piped\x00\xff"))
	require.NoError(t, err)
	assert.Equal(t, "piped\x00\xff", string(stdout))

	s, err := c.StartStream("cat")
	require.NoError(t, err)
	defer s.Close()
	_, err = s.Write([]byte("ping\n"))
	require.NoError(t, err)
	buf := make([]byte, 5)
	n, err := s.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "ping\n", string(buf[:n]))
}
