//go:build linux || darwin

package agent

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/fswire"
)

// fsTestConn drives ServeFS over an in-memory connection, exercising the real
// wire protocol without SSH or FUSE.
type fsTestConn struct {
	t    *testing.T
	w    *fswire.Writer
	r    *fswire.Reader
	root string

	mu sync.Mutex
	id uint64
}

func startFSServer(t *testing.T) *fsTestConn {
	t.Helper()
	root := t.TempDir()
	clientEnd, serverEnd := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		ServeFS(root, serverEnd, serverEnd)
	}()
	t.Cleanup(func() {
		clientEnd.Close()
		serverEnd.Close()
		<-done
	})

	return &fsTestConn{
		t:    t,
		w:    fswire.NewWriter(clientEnd),
		r:    fswire.NewReader(clientEnd),
		root: root,
	}
}

// call sends one request and returns the reply. Requests are issued one at a
// time here, so replies arrive in order.
func (c *fsTestConn) call(req *fswire.Request, payload []byte) (*fswire.Response, []byte) {
	c.t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()

	c.id++
	req.ID = c.id
	require.NoError(c.t, c.w.WriteFrame(req, payload))

	var resp fswire.Response
	data, err := c.r.ReadFrame(&resp)
	require.NoError(c.t, err)
	require.Equal(c.t, req.ID, resp.ID)
	return &resp, data
}

// ok asserts the operation succeeded and returns the reply.
func (c *fsTestConn) ok(req *fswire.Request, payload []byte) (*fswire.Response, []byte) {
	c.t.Helper()
	resp, data := c.call(req, payload)
	require.Zero(c.t, resp.Errno, "op %s on %q failed with errno %d", req.Op, req.Path, resp.Errno)
	return resp, data
}

func (c *fsTestConn) write(rel, content string) {
	c.t.Helper()
	require.NoError(c.t, os.WriteFile(filepath.Join(c.root, rel), []byte(content), 0o644))
}

func TestFSServerPingAndStat(t *testing.T) {
	c := startFSServer(t)
	c.ok(&fswire.Request{Op: fswire.OpPing}, nil)

	c.write("hello.txt", "hello world")
	resp, _ := c.ok(&fswire.Request{Op: fswire.OpStat, Path: "hello.txt"}, nil)
	require.NotNil(t, resp.Attr)
	assert.Equal(t, int64(11), resp.Attr.Size)
	assert.Equal(t, uint32(syscall.S_IFREG), resp.Attr.Mode&syscall.S_IFMT)
	assert.NotZero(t, resp.Attr.Ino)
	assert.NotZero(t, resp.Attr.Mtime.Sec)
}

func TestFSServerStatMissingIsENOENT(t *testing.T) {
	c := startFSServer(t)
	resp, _ := c.call(&fswire.Request{Op: fswire.OpStat, Path: "nope"}, nil)
	assert.Equal(t, uint32(syscall.ENOENT), resp.Errno, "the caller's kernel needs the real errno, not EIO")
}

func TestFSServerReaddirCarriesAttributes(t *testing.T) {
	c := startFSServer(t)
	c.write("a.txt", "aaa")
	require.NoError(t, os.Mkdir(filepath.Join(c.root, "sub"), 0o755))
	require.NoError(t, os.Symlink("a.txt", filepath.Join(c.root, "link")))

	resp, _ := c.ok(&fswire.Request{Op: fswire.OpReaddir, Path: "."}, nil)
	byName := map[string]fswire.DirEntry{}
	for _, e := range resp.Entries {
		byName[e.Name] = e
	}
	require.Len(t, byName, 3)

	// Attributes ride along so a listing does not cost a round trip per entry.
	assert.Equal(t, uint32(syscall.S_IFREG), byName["a.txt"].Mode&syscall.S_IFMT)
	assert.Equal(t, int64(3), byName["a.txt"].Attr.Size)
	assert.Equal(t, uint32(syscall.S_IFDIR), byName["sub"].Mode&syscall.S_IFMT)
	assert.Equal(t, uint32(syscall.S_IFLNK), byName["link"].Mode&syscall.S_IFMT,
		"symlinks must be reported as links, not as their targets")
}

func TestFSServerReadWriteAndHandles(t *testing.T) {
	c := startFSServer(t)

	create, _ := c.ok(&fswire.Request{
		Op:    fswire.OpCreate,
		Path:  "new.bin",
		Flags: fswire.PortableOpenFlags(os.O_RDWR),
		Mode:  0o644,
	}, nil)
	require.NotZero(t, create.Handle)

	payload := []byte{0x00, 0x01, 0xfe, 0xff, 'z'}
	write, _ := c.ok(&fswire.Request{Op: fswire.OpWrite, Handle: create.Handle, Offset: 0}, payload)
	assert.Equal(t, uint32(len(payload)), write.Written)

	_, data := c.ok(&fswire.Request{Op: fswire.OpRead, Handle: create.Handle, Offset: 0, Size: 64}, nil)
	assert.Equal(t, payload, data, "binary content must survive the wire unchanged")

	// A read past EOF is a short read, not an error.
	_, tail := c.ok(&fswire.Request{Op: fswire.OpRead, Handle: create.Handle, Offset: 3, Size: 64}, nil)
	assert.Equal(t, payload[3:], tail)

	c.ok(&fswire.Request{Op: fswire.OpFsync, Handle: create.Handle}, nil)
	c.ok(&fswire.Request{Op: fswire.OpRelease, Handle: create.Handle}, nil)

	// The handle is gone after release.
	resp, _ := c.call(&fswire.Request{Op: fswire.OpRead, Handle: create.Handle, Size: 1}, nil)
	assert.Equal(t, uint32(syscall.EBADF), resp.Errno)

	onDisk, err := os.ReadFile(filepath.Join(c.root, "new.bin"))
	require.NoError(t, err)
	assert.Equal(t, payload, onDisk)
}

func TestFSServerOpenDoesNotCreate(t *testing.T) {
	c := startFSServer(t)
	resp, _ := c.call(&fswire.Request{Op: fswire.OpOpen, Path: "absent.txt", Flags: fswire.PortableOpenFlags(os.O_RDWR)}, nil)
	assert.Equal(t, uint32(syscall.ENOENT), resp.Errno)
	assert.NoFileExists(t, filepath.Join(c.root, "absent.txt"))
}

func TestFSServerSetattr(t *testing.T) {
	c := startFSServer(t)
	c.write("f.txt", "0123456789")

	mode := uint32(0o600)
	size := int64(4)
	resp, _ := c.ok(&fswire.Request{
		Op:      fswire.OpSetattr,
		Path:    "f.txt",
		SetMode: &mode,
		SetSize: &size,
		SetMtime: &fswire.Time{
			Sec: 1000000000,
		},
	}, nil)
	require.NotNil(t, resp.Attr)
	assert.Equal(t, int64(4), resp.Attr.Size)
	assert.Equal(t, mode, resp.Attr.Mode&0o7777)
	assert.Equal(t, int64(1000000000), resp.Attr.Mtime.Sec)

	info, err := os.Stat(filepath.Join(c.root, "f.txt"))
	require.NoError(t, err)
	assert.Equal(t, int64(4), info.Size())
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestFSServerSetattrLeavesUnsetFieldsAlone(t *testing.T) {
	c := startFSServer(t)
	c.write("keep.txt", "content")
	before, err := os.Stat(filepath.Join(c.root, "keep.txt"))
	require.NoError(t, err)

	mode := uint32(0o640)
	c.ok(&fswire.Request{Op: fswire.OpSetattr, Path: "keep.txt", SetMode: &mode}, nil)

	after, err := os.Stat(filepath.Join(c.root, "keep.txt"))
	require.NoError(t, err)
	assert.Equal(t, before.Size(), after.Size(), "a chmod must not truncate")
	assert.Equal(t, before.ModTime().Unix(), after.ModTime().Unix(), "a chmod must not rewrite mtime")
}

func TestFSServerNamespaceOperations(t *testing.T) {
	c := startFSServer(t)

	c.ok(&fswire.Request{Op: fswire.OpMkdir, Path: "dir", Mode: 0o755}, nil)
	assert.DirExists(t, filepath.Join(c.root, "dir"))

	c.write("dir/file.txt", "data")
	c.ok(&fswire.Request{Op: fswire.OpRename, Path: "dir/file.txt", NewPath: "dir/renamed.txt"}, nil)
	assert.FileExists(t, filepath.Join(c.root, "dir", "renamed.txt"))

	c.ok(&fswire.Request{Op: fswire.OpSymlink, Path: "dir/link", Target: "renamed.txt"}, nil)
	resp, _ := c.ok(&fswire.Request{Op: fswire.OpReadlink, Path: "dir/link"}, nil)
	assert.Equal(t, "renamed.txt", resp.Target)

	c.ok(&fswire.Request{Op: fswire.OpLink, Path: "dir/renamed.txt", NewPath: "dir/hard.txt"}, nil)
	assert.FileExists(t, filepath.Join(c.root, "dir", "hard.txt"))

	c.ok(&fswire.Request{Op: fswire.OpUnlink, Path: "dir/link"}, nil)
	c.ok(&fswire.Request{Op: fswire.OpUnlink, Path: "dir/hard.txt"}, nil)
	c.ok(&fswire.Request{Op: fswire.OpUnlink, Path: "dir/renamed.txt"}, nil)
	c.ok(&fswire.Request{Op: fswire.OpRmdir, Path: "dir"}, nil)
	assert.NoDirExists(t, filepath.Join(c.root, "dir"))
}

func TestFSServerRmdirNonEmptyFails(t *testing.T) {
	c := startFSServer(t)
	require.NoError(t, os.Mkdir(filepath.Join(c.root, "full"), 0o755))
	c.write("full/x", "x")

	resp, _ := c.call(&fswire.Request{Op: fswire.OpRmdir, Path: "full"}, nil)
	assert.NotZero(t, resp.Errno)
}

func TestFSServerStatfs(t *testing.T) {
	c := startFSServer(t)
	resp, _ := c.ok(&fswire.Request{Op: fswire.OpStatfs, Path: "."}, nil)
	require.NotNil(t, resp.Statfs)
	assert.NotZero(t, resp.Statfs.Blocks)
	assert.NotZero(t, resp.Statfs.Bsize)
	assert.NotZero(t, resp.Statfs.NameLen)
}

func TestFSServerRefusesEscapingPaths(t *testing.T) {
	c := startFSServer(t)
	// The helper serves a subtree; climbing out of it, or naming an absolute
	// path, must be refused rather than silently resolved.
	for _, path := range []string{"../etc/passwd", "a/../../b", "/etc/passwd"} {
		resp, _ := c.call(&fswire.Request{Op: fswire.OpStat, Path: path}, nil)
		assert.Equal(t, uint32(syscall.EINVAL), resp.Errno, "path %q should be refused", path)
	}
}

func TestFSServerUnknownOp(t *testing.T) {
	c := startFSServer(t)
	resp, _ := c.call(&fswire.Request{Op: "teleport", Path: "."}, nil)
	assert.Equal(t, uint32(syscall.ENOSYS), resp.Errno)
}

func TestFSServerHandlesConcurrentRequests(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 20; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(root, string(rune('a'+i))+".txt"), []byte("x"), 0o644))
	}

	clientEnd, serverEnd := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ServeFS(root, serverEnd, serverEnd)
	}()
	defer func() {
		clientEnd.Close()
		serverEnd.Close()
		<-done
	}()

	// Fire every request before reading any reply: the server must answer
	// concurrently and tag each reply with its request id.
	w := fswire.NewWriter(clientEnd)
	for i := 0; i < 20; i++ {
		req := &fswire.Request{ID: uint64(i + 1), Op: fswire.OpStat, Path: string(rune('a'+i)) + ".txt"}
		require.NoError(t, w.WriteFrame(req, nil))
	}

	r := fswire.NewReader(clientEnd)
	seen := map[uint64]bool{}
	for i := 0; i < 20; i++ {
		var resp fswire.Response
		_, err := r.ReadFrame(&resp)
		require.NoError(t, err)
		require.Zero(t, resp.Errno)
		seen[resp.ID] = true
	}
	assert.Len(t, seen, 20)
}

func TestFSServerClosesHandlesWhenSessionEnds(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "f"), []byte("x"), 0o644))

	clientEnd, serverEnd := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ServeFS(root, serverEnd, serverEnd)
	}()

	w := fswire.NewWriter(clientEnd)
	r := fswire.NewReader(clientEnd)
	require.NoError(t, w.WriteFrame(&fswire.Request{ID: 1, Op: fswire.OpOpen, Path: "f", Flags: fswire.OpenRead}, nil))
	var resp fswire.Response
	_, err := r.ReadFrame(&resp)
	require.NoError(t, err)
	require.NotZero(t, resp.Handle)

	// Dropping the connection must end the session (and with it the open
	// handles) rather than leaving files open on the remote forever.
	clientEnd.Close()
	<-done
}
