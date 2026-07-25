// Package fswire defines the filesystem protocol spoken between the local
// FUSE mount and the remote helper, plus its framing. It has no dependencies
// beyond the standard library so both ends -- including the remote helper
// built for platforms that cannot mount anything -- can share it.
//
// One request/response pair corresponds to one filesystem operation. The
// local side multiplexes many in flight over a single SSH channel, matching
// replies by ID, so a mount costs one channel rather than one process per
// syscall.
package fswire

// Op names. They mirror the POSIX calls the FUSE layer has to service.
const (
	OpStat     = "stat"     // lstat a path
	OpReaddir  = "readdir"  // list a directory, with attributes
	OpReadlink = "readlink" // read a symlink target
	OpOpen     = "open"     // open an existing file, returns a handle
	OpCreate   = "create"   // create+open a file, returns a handle and attrs
	OpRead     = "read"     // read from a handle
	OpWrite    = "write"    // write to a handle
	OpRelease  = "release"  // close a handle
	OpFsync    = "fsync"    // flush a handle to stable storage
	OpSetattr  = "setattr"  // chmod/chown/truncate/utimes
	OpMkdir    = "mkdir"
	OpRmdir    = "rmdir"
	OpUnlink   = "unlink"
	OpRename   = "rename"
	OpSymlink  = "symlink"
	OpLink     = "link"
	OpMknod    = "mknod"
	OpStatfs   = "statfs"
	OpPing     = "ping" // liveness check for the helper process
)

// Request is one filesystem operation. Fields not relevant to Op are unset.
// Write payloads travel as the frame's binary payload, not in this header.
type Request struct {
	ID   uint64 `json:"id"`
	Op   string `json:"op"`
	Path string `json:"path,omitempty"`

	NewPath string `json:"new_path,omitempty"` // rename/link destination
	Target  string `json:"target,omitempty"`   // symlink target

	Handle uint64 `json:"handle,omitempty"`
	Offset int64  `json:"offset,omitempty"`
	Size   uint32 `json:"size,omitempty"`
	Flags  uint32 `json:"flags,omitempty"`
	Mode   uint32 `json:"mode,omitempty"`
	Dev    uint32 `json:"dev,omitempty"`

	// Setattr fields are pointers so "not requested" is distinguishable from
	// "set to zero" -- truncating to 0 and leaving the size alone are
	// different operations.
	SetMode  *uint32 `json:"set_mode,omitempty"`
	SetUID   *uint32 `json:"set_uid,omitempty"`
	SetGID   *uint32 `json:"set_gid,omitempty"`
	SetSize  *int64  `json:"set_size,omitempty"`
	SetAtime *Time   `json:"set_atime,omitempty"`
	SetMtime *Time   `json:"set_mtime,omitempty"`
}

// Response is the reply to a Request. Errno is a raw errno value (0 on
// success); read payloads travel as the frame's binary payload.
type Response struct {
	ID    uint64 `json:"id"`
	Errno uint32 `json:"errno,omitempty"`

	Attr    *Attr      `json:"attr,omitempty"`
	Entries []DirEntry `json:"entries,omitempty"`
	Handle  uint64     `json:"handle,omitempty"`
	Written uint32     `json:"written,omitempty"`
	Target  string     `json:"target,omitempty"`
	Statfs  *Statfs    `json:"statfs,omitempty"`
}

// Time is a wall-clock timestamp split the way stat(2) reports it.
type Time struct {
	Sec  int64  `json:"sec"`
	Nsec uint32 `json:"nsec"`
}

// Attr is the subset of struct stat the FUSE layer needs.
type Attr struct {
	Ino     uint64 `json:"ino"`
	Size    int64  `json:"size"`
	Blocks  int64  `json:"blocks"`
	Mode    uint32 `json:"mode"` // raw mode bits, including the file type
	Nlink   uint32 `json:"nlink"`
	UID     uint32 `json:"uid"`
	GID     uint32 `json:"gid"`
	Rdev    uint32 `json:"rdev"`
	Blksize int32  `json:"blksize"`
	Atime   Time   `json:"atime"`
	Mtime   Time   `json:"mtime"`
	Ctime   Time   `json:"ctime"`
}

// DirEntry is one readdir result. Attr is filled in so a directory listing
// answers the stat calls that follow it without extra round trips -- the
// difference between `ls -l` costing one request and costing one per file.
type DirEntry struct {
	Name string `json:"name"`
	Mode uint32 `json:"mode"` // file type bits, as returned by readdir
	Ino  uint64 `json:"ino,omitempty"`
	Attr *Attr  `json:"attr,omitempty"`
}

// Statfs is the subset of statfs(2) reported for the mount.
type Statfs struct {
	Blocks  uint64 `json:"blocks"`
	Bfree   uint64 `json:"bfree"`
	Bavail  uint64 `json:"bavail"`
	Files   uint64 `json:"files"`
	Ffree   uint64 `json:"ffree"`
	Bsize   uint32 `json:"bsize"`
	NameLen uint32 `json:"namelen"`
	Frsize  uint32 `json:"frsize"`
}
