package agent

import (
	"syscall"

	"github.com/wow-look-at-my/remote-agent/fswire"
)

// lstatAttr stats a path without following symlinks and converts the result
// to the wire form. darwin names the timestamp fields *timespec and uses
// different integer widths than Linux, so the conversion is per-platform.
func lstatAttr(path string) (*fswire.Attr, error) {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return nil, err
	}
	return &fswire.Attr{
		Ino:     st.Ino,
		Size:    st.Size,
		Blocks:  st.Blocks,
		Mode:    uint32(st.Mode),
		Nlink:   uint32(st.Nlink),
		UID:     st.Uid,
		GID:     st.Gid,
		Rdev:    uint32(st.Rdev),
		Blksize: st.Blksize,
		Atime:   fswire.Time{Sec: st.Atimespec.Sec, Nsec: uint32(st.Atimespec.Nsec)},
		Mtime:   fswire.Time{Sec: st.Mtimespec.Sec, Nsec: uint32(st.Mtimespec.Nsec)},
		Ctime:   fswire.Time{Sec: st.Ctimespec.Sec, Nsec: uint32(st.Ctimespec.Nsec)},
	}, nil
}

// statfs reports filesystem-wide usage for the mount.
func statfs(path string) (*fswire.Statfs, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return nil, err
	}
	return &fswire.Statfs{
		Blocks: st.Blocks,
		Bfree:  st.Bfree,
		Bavail: st.Bavail,
		Files:  st.Files,
		Ffree:  st.Ffree,
		Bsize:  uint32(st.Bsize),
		// darwin's statfs has no name-length field; 255 is the POSIX
		// minimum every filesystem macOS mounts satisfies.
		NameLen: 255,
		Frsize:  uint32(st.Bsize),
	}, nil
}

// mknod creates a device or FIFO node.
func mknod(path string, mode, dev uint32) error {
	return syscall.Mknod(path, mode, int(dev))
}

// utimes sets access and modification times, leaving unset ones untouched
// (UTIME_OMIT) rather than silently rewriting them to now.
func utimes(path string, atime, mtime *fswire.Time) error {
	ts := []syscall.Timespec{
		{Sec: 0, Nsec: unixUtimeOmit},
		{Sec: 0, Nsec: unixUtimeOmit},
	}
	if atime != nil {
		ts[0] = syscall.Timespec{Sec: atime.Sec, Nsec: int64(atime.Nsec)}
	}
	if mtime != nil {
		ts[1] = syscall.Timespec{Sec: mtime.Sec, Nsec: int64(mtime.Nsec)}
	}
	return syscall.UtimesNano(path, ts)
}

// unixUtimeOmit is UTIME_OMIT: leave this timestamp as it is.
const unixUtimeOmit = (1 << 30) - 2
