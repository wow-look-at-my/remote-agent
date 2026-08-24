//go:build linux || darwin

package remotefs

import (
	"context"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/wow-look-at-my/remote-agent/fswire"
)

// Cache timeouts, kept short because forwarded commands change the remote
// underneath the mount. Negative lookups are not cached. see docs/mount/behaviour.md
const (
	attrCacheTimeout  = time.Second
	entryCacheTimeout = time.Second
)

// The largest write the kernel hands us at once. 8x the default, so a copy costs 8x fewer trips.
const maxWrite = 1 << 20

// Options configures a mount.
type Options struct {
	// AllowOther exposes the mount to other users on the local machine.
	AllowOther bool
	// Debug logs every FUSE request; very noisy, for diagnosis only.
	Debug bool
	// Name identifies the source in `mount` output, e.g. "user@host:/srv".
	Name string
}

// Mount is a live mount of a remote filesystem.
type Mount struct {
	server *fuse.Server
	client *Client
	dir    string
}

// MountClient mounts the filesystem served by c at dir. The mount stays up
// until Unmount is called or the process exits.
func MountClient(dir string, c *Client, opts Options) (*Mount, error) {
	name := opts.Name
	if name == "" {
		name = "remote-agent"
	}

	attrTimeout := attrCacheTimeout
	entryTimeout := entryCacheTimeout
	negativeTimeout := time.Duration(0)

	root := &node{shared: &shared{client: c}}
	fsOpts := &fs.Options{
		AttrTimeout:     &attrTimeout,
		EntryTimeout:    &entryTimeout,
		NegativeTimeout: &negativeTimeout,
		MountOptions: fuse.MountOptions{
			FsName:     name,
			Name:       "remote-agent",
			AllowOther: opts.AllowOther,
			Debug:      opts.Debug,
			MaxWrite:   maxWrite,
			// mount(2) first, fusermount after: works as root and as an ordinary user.
			DirectMount: true,
			// The protocol has no xattrs, and saying so stops the kernel asking.
			DisableXAttrs: true,
		},
	}

	server, err := fs.Mount(dir, root, fsOpts)
	if err != nil {
		return nil, fmt.Errorf("mount %s: %w", dir, err)
	}
	return &Mount{server: server, client: c, dir: dir}, nil
}

// Dir returns the local mount point.
func (m *Mount) Dir() string { return m.dir }

// Unmount fails while the mount is busy. A caller that asked deserves to hear that,
// rather than have its shell yanked out from under it.
func (m *Mount) Unmount() error {
	err := m.server.Unmount()
	if err != nil {
		return fmt.Errorf("unmount %s: %w", m.dir, err)
	}
	// Closed either way: a helper attached to a departing mount is a leaked process.
	return m.client.Close()
}

// ForceUnmount detaches a busy mount, and every shutdown path uses it: a mount on a
// dead session blocks any process that stats it, forever. see docs/mount/behaviour.md
func (m *Mount) ForceUnmount() error {
	err := m.server.Unmount()
	if err != nil {
		err = forceDetach(m.dir)
	}
	if cerr := m.client.Close(); err == nil {
		err = cerr
	}
	return err
}

// forceDetach removes a mount point without waiting for its users. It prefers
// fusermount, which works without privileges, and falls back to the unmount
// syscall for systems where fusermount is not installed.
func forceDetach(dir string) error {
	var lastErr error
	for _, tool := range []string{"fusermount3", "fusermount"} {
		path, err := exec.LookPath(tool)
		if err != nil {
			continue
		}
		if out, err := exec.Command(path, "-uz", dir).CombinedOutput(); err == nil {
			return nil
		} else if len(out) > 0 {
			lastErr = fmt.Errorf("%s: %s", tool, strings.TrimSpace(string(out)))
		}
	}
	if err := syscall.Unmount(dir, detachFlag); err != nil {
		if lastErr != nil {
			return fmt.Errorf("detach %s: %w (%v)", dir, err, lastErr)
		}
		return fmt.Errorf("detach %s: %w", dir, err)
	}
	return nil
}

// Wait blocks until something unmounts the filesystem, here or from outside.
func (m *Mount) Wait() { m.server.Wait() }

// shared holds what every node in the tree needs.
type shared struct {
	client *Client
}

// One file or directory. Its path comes from its position, so a parent rename moves it.
type node struct {
	fs.Inode
	shared *shared
}

// Interface assertions: the operations this filesystem implements. Anything
// missing degrades to go-fuse's default (usually ENOSYS or a no-op), so
// keeping this list explicit is what stops a silently unimplemented call.
var (
	_ = (fs.NodeGetattrer)((*node)(nil))
	_ = (fs.NodeSetattrer)((*node)(nil))
	_ = (fs.NodeLookuper)((*node)(nil))
	_ = (fs.NodeReaddirer)((*node)(nil))
	_ = (fs.NodeReadlinker)((*node)(nil))
	_ = (fs.NodeOpener)((*node)(nil))
	_ = (fs.NodeCreater)((*node)(nil))
	_ = (fs.NodeReader)((*node)(nil))
	_ = (fs.NodeWriter)((*node)(nil))
	_ = (fs.NodeReleaser)((*node)(nil))
	_ = (fs.NodeFsyncer)((*node)(nil))
	_ = (fs.NodeMkdirer)((*node)(nil))
	_ = (fs.NodeRmdirer)((*node)(nil))
	_ = (fs.NodeUnlinker)((*node)(nil))
	_ = (fs.NodeRenamer)((*node)(nil))
	_ = (fs.NodeSymlinker)((*node)(nil))
	_ = (fs.NodeLinker)((*node)(nil))
	_ = (fs.NodeMknoder)((*node)(nil))
	_ = (fs.NodeStatfser)((*node)(nil))
)

// handle is the open-file token handed back to the kernel; it carries the
// remote helper's handle id.
type handle struct {
	id uint64
}

// call sends one request and converts transport failures into EIO -- a dead
// SSH connection has to surface as an I/O error to the calling program, not
// as a hang.
func (n *node) call(req *fswire.Request, payload []byte) (*fswire.Response, []byte, syscall.Errno) {
	resp, data, err := n.shared.client.Call(req, payload)
	if err != nil {
		return nil, nil, syscall.EIO
	}
	if resp.Errno != 0 {
		return nil, nil, syscall.Errno(resp.Errno)
	}
	return resp, data, 0
}

// path returns this node's path relative to the mount root, which is what the
// remote helper resolves against its own root.
func (n *node) path() string {
	p := n.Path(nil)
	if p == "" {
		return "."
	}
	return p
}

// child returns the relative path of a name inside this node.
func (n *node) child(name string) string {
	if p := n.Path(nil); p != "" {
		return path.Join(p, name)
	}
	return name
}

func (n *node) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	resp, _, errno := n.call(&fswire.Request{Op: fswire.OpStat, Path: n.path()}, nil)
	if errno != 0 {
		return errno
	}
	fillAttr(resp.Attr, &out.Attr)
	return 0
}

func (n *node) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	req := &fswire.Request{Op: fswire.OpSetattr, Path: n.path()}
	if mode, ok := in.GetMode(); ok {
		req.SetMode = &mode
	}
	if uid, ok := in.GetUID(); ok {
		req.SetUID = &uid
	}
	if gid, ok := in.GetGID(); ok {
		req.SetGID = &gid
	}
	if size, ok := in.GetSize(); ok {
		signed := int64(size)
		req.SetSize = &signed
	}
	if atime, ok := in.GetATime(); ok {
		req.SetAtime = &fswire.Time{Sec: atime.Unix(), Nsec: uint32(atime.Nanosecond())}
	}
	if mtime, ok := in.GetMTime(); ok {
		req.SetMtime = &fswire.Time{Sec: mtime.Unix(), Nsec: uint32(mtime.Nanosecond())}
	}

	resp, _, errno := n.call(req, nil)
	if errno != 0 {
		return errno
	}
	fillAttr(resp.Attr, &out.Attr)
	return 0
}

func (n *node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	resp, _, errno := n.call(&fswire.Request{Op: fswire.OpStat, Path: n.child(name)}, nil)
	if errno != 0 {
		return nil, errno
	}
	fillAttr(resp.Attr, &out.Attr)
	return n.newChild(ctx, resp.Attr), 0
}

func (n *node) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	resp, _, errno := n.call(&fswire.Request{Op: fswire.OpReaddir, Path: n.path()}, nil)
	if errno != 0 {
		return nil, errno
	}
	entries := make([]fuse.DirEntry, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		entries = append(entries, fuse.DirEntry{Name: e.Name, Mode: e.Mode, Ino: e.Ino})
	}
	return fs.NewListDirStream(entries), 0
}

func (n *node) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	resp, _, errno := n.call(&fswire.Request{Op: fswire.OpReadlink, Path: n.path()}, nil)
	if errno != 0 {
		return nil, errno
	}
	return []byte(resp.Target), 0
}

func (n *node) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	resp, _, errno := n.call(&fswire.Request{
		Op:    fswire.OpOpen,
		Path:  n.path(),
		Flags: fswire.PortableOpenFlags(int(flags)),
	}, nil)
	if errno != 0 {
		return nil, 0, errno
	}
	return &handle{id: resp.Handle}, 0, 0
}

func (n *node) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	resp, _, errno := n.call(&fswire.Request{
		Op:    fswire.OpCreate,
		Path:  n.child(name),
		Flags: fswire.PortableOpenFlags(int(flags)),
		Mode:  mode,
	}, nil)
	if errno != 0 {
		return nil, nil, 0, errno
	}
	fillAttr(resp.Attr, &out.Attr)
	return n.newChild(ctx, resp.Attr), &handle{id: resp.Handle}, 0, 0
}

func (n *node) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	id, ok := handleID(f)
	if !ok {
		return nil, syscall.EBADF
	}
	_, data, errno := n.call(&fswire.Request{
		Op:     fswire.OpRead,
		Handle: id,
		Offset: off,
		Size:   uint32(len(dest)),
	}, nil)
	if errno != 0 {
		return nil, errno
	}
	n2 := copy(dest, data)
	return fuse.ReadResultData(dest[:n2]), 0
}

func (n *node) Write(ctx context.Context, f fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno) {
	id, ok := handleID(f)
	if !ok {
		return 0, syscall.EBADF
	}
	resp, _, errno := n.call(&fswire.Request{
		Op:     fswire.OpWrite,
		Handle: id,
		Offset: off,
	}, data)
	if errno != 0 {
		return 0, errno
	}
	return resp.Written, 0
}

func (n *node) Release(ctx context.Context, f fs.FileHandle) syscall.Errno {
	id, ok := handleID(f)
	if !ok {
		return 0
	}
	_, _, errno := n.call(&fswire.Request{Op: fswire.OpRelease, Handle: id}, nil)
	return errno
}

func (n *node) Fsync(ctx context.Context, f fs.FileHandle, flags uint32) syscall.Errno {
	id, ok := handleID(f)
	if !ok {
		return 0
	}
	_, _, errno := n.call(&fswire.Request{Op: fswire.OpFsync, Handle: id}, nil)
	return errno
}

func (n *node) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	resp, _, errno := n.call(&fswire.Request{Op: fswire.OpMkdir, Path: n.child(name), Mode: mode}, nil)
	if errno != 0 {
		return nil, errno
	}
	fillAttr(resp.Attr, &out.Attr)
	return n.newChild(ctx, resp.Attr), 0
}

func (n *node) Rmdir(ctx context.Context, name string) syscall.Errno {
	_, _, errno := n.call(&fswire.Request{Op: fswire.OpRmdir, Path: n.child(name)}, nil)
	return errno
}

func (n *node) Unlink(ctx context.Context, name string) syscall.Errno {
	_, _, errno := n.call(&fswire.Request{Op: fswire.OpUnlink, Path: n.child(name)}, nil)
	return errno
}

func (n *node) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	target, ok := newParent.(*node)
	if !ok {
		return syscall.EXDEV
	}
	_, _, errno := n.call(&fswire.Request{
		Op:      fswire.OpRename,
		Path:    n.child(name),
		NewPath: target.child(newName),
	}, nil)
	return errno
}

func (n *node) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	resp, _, errno := n.call(&fswire.Request{Op: fswire.OpSymlink, Path: n.child(name), Target: target}, nil)
	if errno != 0 {
		return nil, errno
	}
	fillAttr(resp.Attr, &out.Attr)
	return n.newChild(ctx, resp.Attr), 0
}

func (n *node) Link(ctx context.Context, target fs.InodeEmbedder, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	existing, ok := target.(*node)
	if !ok {
		return nil, syscall.EXDEV
	}
	resp, _, errno := n.call(&fswire.Request{
		Op:      fswire.OpLink,
		Path:    existing.path(),
		NewPath: n.child(name),
	}, nil)
	if errno != 0 {
		return nil, errno
	}
	fillAttr(resp.Attr, &out.Attr)
	return n.newChild(ctx, resp.Attr), 0
}

func (n *node) Mknod(ctx context.Context, name string, mode uint32, dev uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	resp, _, errno := n.call(&fswire.Request{Op: fswire.OpMknod, Path: n.child(name), Mode: mode, Dev: dev}, nil)
	if errno != 0 {
		return nil, errno
	}
	fillAttr(resp.Attr, &out.Attr)
	return n.newChild(ctx, resp.Attr), 0
}

func (n *node) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
	resp, _, errno := n.call(&fswire.Request{Op: fswire.OpStatfs, Path: n.path()}, nil)
	if errno != 0 {
		return errno
	}
	s := resp.Statfs
	out.Blocks, out.Bfree, out.Bavail = s.Blocks, s.Bfree, s.Bavail
	out.Files, out.Ffree = s.Files, s.Ffree
	out.Bsize, out.NameLen, out.Frsize = s.Bsize, s.NameLen, s.Frsize
	return 0
}

// newChild creates (or reuses) the inode for a child, keyed by the remote's
// inode number so hard links and repeated lookups map to one local inode.
func (n *node) newChild(ctx context.Context, attr *fswire.Attr) *fs.Inode {
	return n.NewInode(ctx, &node{shared: n.shared}, fs.StableAttr{
		Mode: attr.Mode & syscall.S_IFMT,
		Ino:  attr.Ino,
	})
}

// handleID extracts the remote handle id from a kernel file handle.
func handleID(f fs.FileHandle) (uint64, bool) {
	h, ok := f.(*handle)
	if !ok {
		return 0, false
	}
	return h.id, true
}

// fillAttr converts wire attributes into the kernel's form.
func fillAttr(a *fswire.Attr, out *fuse.Attr) {
	if a == nil {
		return
	}
	out.Ino = a.Ino
	out.Size = uint64(a.Size)
	out.Blocks = uint64(a.Blocks)
	out.Mode = a.Mode
	out.Nlink = a.Nlink
	out.Owner = fuse.Owner{Uid: a.UID, Gid: a.GID}
	out.Rdev = a.Rdev
	out.Blksize = uint32(a.Blksize)
	out.Atime, out.Atimensec = uint64(a.Atime.Sec), a.Atime.Nsec
	out.Mtime, out.Mtimensec = uint64(a.Mtime.Sec), a.Mtime.Nsec
	out.Ctime, out.Ctimensec = uint64(a.Ctime.Sec), a.Ctime.Nsec
}
