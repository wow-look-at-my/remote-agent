//go:build linux || darwin

package agent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/wow-look-at-my/remote-agent/fswire"
)

// Concurrent, so a large read never stalls the stats behind it. Bounded, so nothing runs out.
const fsMaxInFlight = 32

// ServeFS runs the remote half of a mount: it reads filesystem requests from
// in and writes replies to out, serving paths under root.
//
// It is deliberately a long-lived process speaking a framed protocol rather
// than one SSH command per operation. A FUSE mount issues thousands of tiny
// calls (a single `ls -l` is a lookup plus a stat per entry), and paying a
// process spawn and a channel open for each one would make the mount unusable.
func ServeFS(root string, in io.Reader, out io.Writer) error {
	root = filepath.Clean(root)
	if root == "" {
		root = "/"
	}
	server := &fsServer{
		root:    root,
		handles: map[uint64]*os.File{},
		writer:  fswire.NewWriter(out),
		sem:     make(chan struct{}, fsMaxInFlight),
	}
	defer server.closeAll()

	reader := fswire.NewReader(in)
	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		var req fswire.Request
		payload, err := reader.ReadFrame(&req)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		server.sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-server.sem }()
			resp, data := server.handle(&req, payload)
			// A write failure means the client is gone, and the read loop sees EOF.
			_ = server.writer.WriteFrame(resp, data)
		}()
	}
}

// fsServer holds the open handles for one mount session.
type fsServer struct {
	root   string
	writer *fswire.Writer
	sem    chan struct{}

	mu      sync.Mutex
	handles map[uint64]*os.File
	nextID  uint64
}

func (s *fsServer) handle(req *fswire.Request, payload []byte) (*fswire.Response, []byte) {
	resp := &fswire.Response{ID: req.ID}

	path, err := s.resolve(req.Path)
	if err != nil {
		resp.Errno = uint32(syscall.EINVAL)
		return resp, nil
	}

	switch req.Op {
	case fswire.OpPing:
		return resp, nil
	case fswire.OpStat:
		attr, err := lstatAttr(path)
		if err != nil {
			return errnoResponse(resp, err), nil
		}
		resp.Attr = attr
	case fswire.OpReaddir:
		entries, err := s.readdir(path)
		if err != nil {
			return errnoResponse(resp, err), nil
		}
		resp.Entries = entries
	case fswire.OpReadlink:
		target, err := os.Readlink(path)
		if err != nil {
			return errnoResponse(resp, err), nil
		}
		resp.Target = target
	case fswire.OpOpen, fswire.OpCreate:
		// O_APPEND goes: pwrite ignores an offset on it. see docs/mount/behaviour.md
		flags := fswire.LocalOpenFlags(req.Flags) &^ os.O_APPEND
		if req.Op == fswire.OpCreate {
			flags |= os.O_CREATE
		}
		f, err := os.OpenFile(path, flags, os.FileMode(req.Mode&0o7777))
		if err != nil {
			return errnoResponse(resp, err), nil
		}
		resp.Handle = s.store(f)
		if attr, err := lstatAttr(path); err == nil {
			resp.Attr = attr
		}
	case fswire.OpRead:
		f, ok := s.lookup(req.Handle)
		if !ok {
			resp.Errno = uint32(syscall.EBADF)
			return resp, nil
		}
		buf := make([]byte, req.Size)
		n, err := f.ReadAt(buf, req.Offset)
		// A short read at EOF is success with fewer bytes, not an error.
		if err != nil && !errors.Is(err, io.EOF) {
			return errnoResponse(resp, err), nil
		}
		return resp, buf[:n]
	case fswire.OpWrite:
		f, ok := s.lookup(req.Handle)
		if !ok {
			resp.Errno = uint32(syscall.EBADF)
			return resp, nil
		}
		n, err := f.WriteAt(payload, req.Offset)
		if err != nil {
			return errnoResponse(resp, err), nil
		}
		resp.Written = uint32(n)
	case fswire.OpRelease:
		if f, ok := s.take(req.Handle); ok {
			if err := f.Close(); err != nil {
				return errnoResponse(resp, err), nil
			}
		}
	case fswire.OpFsync:
		f, ok := s.lookup(req.Handle)
		if !ok {
			resp.Errno = uint32(syscall.EBADF)
			return resp, nil
		}
		if err := f.Sync(); err != nil {
			return errnoResponse(resp, err), nil
		}
	case fswire.OpSetattr:
		if err := s.setattr(path, req); err != nil {
			return errnoResponse(resp, err), nil
		}
		attr, err := lstatAttr(path)
		if err != nil {
			return errnoResponse(resp, err), nil
		}
		resp.Attr = attr
	case fswire.OpMkdir:
		if err := os.Mkdir(path, os.FileMode(req.Mode&0o7777)); err != nil {
			return errnoResponse(resp, err), nil
		}
		attr, err := lstatAttr(path)
		if err != nil {
			return errnoResponse(resp, err), nil
		}
		resp.Attr = attr
	case fswire.OpRmdir:
		if err := syscall.Rmdir(path); err != nil {
			return errnoResponse(resp, err), nil
		}
	case fswire.OpUnlink:
		if err := syscall.Unlink(path); err != nil {
			return errnoResponse(resp, err), nil
		}
	case fswire.OpRename:
		newPath, err := s.resolve(req.NewPath)
		if err != nil {
			resp.Errno = uint32(syscall.EINVAL)
			return resp, nil
		}
		if err := os.Rename(path, newPath); err != nil {
			return errnoResponse(resp, err), nil
		}
	case fswire.OpSymlink:
		if err := os.Symlink(req.Target, path); err != nil {
			return errnoResponse(resp, err), nil
		}
		attr, err := lstatAttr(path)
		if err != nil {
			return errnoResponse(resp, err), nil
		}
		resp.Attr = attr
	case fswire.OpLink:
		newPath, err := s.resolve(req.NewPath)
		if err != nil {
			resp.Errno = uint32(syscall.EINVAL)
			return resp, nil
		}
		if err := os.Link(path, newPath); err != nil {
			return errnoResponse(resp, err), nil
		}
		attr, err := lstatAttr(newPath)
		if err != nil {
			return errnoResponse(resp, err), nil
		}
		resp.Attr = attr
	case fswire.OpMknod:
		if err := mknod(path, req.Mode, req.Dev); err != nil {
			return errnoResponse(resp, err), nil
		}
		attr, err := lstatAttr(path)
		if err != nil {
			return errnoResponse(resp, err), nil
		}
		resp.Attr = attr
	case fswire.OpStatfs:
		stat, err := statfs(path)
		if err != nil {
			return errnoResponse(resp, err), nil
		}
		resp.Statfs = stat
	default:
		resp.Errno = uint32(syscall.ENOSYS)
	}
	return resp, nil
}

// setattr applies whichever attributes the request carries. Each is optional,
// so a plain truncate does not also rewrite the mode.
func (s *fsServer) setattr(path string, req *fswire.Request) error {
	if req.SetMode != nil {
		if err := syscall.Chmod(path, *req.SetMode&0o7777); err != nil {
			return err
		}
	}
	if req.SetUID != nil || req.SetGID != nil {
		uid, gid := -1, -1
		if req.SetUID != nil {
			uid = int(*req.SetUID)
		}
		if req.SetGID != nil {
			gid = int(*req.SetGID)
		}
		if err := os.Lchown(path, uid, gid); err != nil {
			return err
		}
	}
	if req.SetSize != nil {
		if err := os.Truncate(path, *req.SetSize); err != nil {
			return err
		}
	}
	if req.SetAtime != nil || req.SetMtime != nil {
		if err := utimes(path, req.SetAtime, req.SetMtime); err != nil {
			return err
		}
	}
	return nil
}

// readdir lists a directory with attributes attached. Stat-ing every entry
// here is one local syscall each on the remote, and saves the client a
// network round trip per entry -- the difference between `ls -l` costing one
// request and costing one per file.
func (s *fsServer) readdir(path string) ([]fswire.DirEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}

	entries := make([]fswire.DirEntry, 0, len(names))
	for _, name := range names {
		entry := fswire.DirEntry{Name: name}
		if attr, err := lstatAttr(filepath.Join(path, name)); err == nil {
			entry.Attr = attr
			entry.Mode = attr.Mode
			entry.Ino = attr.Ino
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// resolve maps a client path (relative to the mount root) onto an absolute
// remote path, refusing anything that would climb out of the root.
func (s *fsServer) resolve(rel string) (string, error) {
	if rel == "" || rel == "." {
		return s.root, nil
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative to the mount root", rel)
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path %q escapes the mount root", rel)
	}
	return filepath.Join(s.root, clean), nil
}

func (s *fsServer) store(f *os.File) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := s.nextID
	s.handles[id] = f
	return id
}

func (s *fsServer) lookup(id uint64) (*os.File, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.handles[id]
	return f, ok
}

func (s *fsServer) take(id uint64) (*os.File, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.handles[id]
	delete(s.handles, id)
	return f, ok
}

// closeAll releases every handle when the session ends, so a disconnected
// client cannot leak open files on the remote.
func (s *fsServer) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, f := range s.handles {
		f.Close()
		delete(s.handles, id)
	}
}

// errnoResponse fills in the errno for a failed operation, preserving the
// real cause (ENOENT, EACCES, ...) so the client's kernel reports what the
// remote reported. Errors with no errno become EIO.
func errnoResponse(resp *fswire.Response, err error) *fswire.Response {
	var errno syscall.Errno
	switch {
	case errors.As(err, &errno):
	case errors.Is(err, os.ErrNotExist):
		errno = syscall.ENOENT
	case errors.Is(err, os.ErrPermission):
		errno = syscall.EACCES
	case errors.Is(err, os.ErrExist):
		errno = syscall.EEXIST
	default:
		errno = syscall.EIO
	}
	resp.Errno = uint32(errno)
	return resp
}
