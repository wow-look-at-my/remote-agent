package agent

import (
	"syscall"

	"github.com/wow-look-at-my/remote-agent/fswire"
)

// lstatAttr stats a path without following symlinks and converts the result
// to the wire form. Linux and darwin disagree on the field names and integer
// widths of syscall.Stat_t, which is why this lives in a platform file.
func lstatAttr(path string) (*fswire.Attr, error) {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return nil, err
	}
	return &fswire.Attr{
		Ino:     st.Ino,
		Size:    st.Size,
		Blocks:  st.Blocks,
		Mode:    st.Mode,
		Nlink:   uint32(st.Nlink),
		UID:     st.Uid,
		GID:     st.Gid,
		Rdev:    uint32(st.Rdev),
		Blksize: int32(st.Blksize),
		Atime:   fswire.Time{Sec: st.Atim.Sec, Nsec: uint32(st.Atim.Nsec)},
		Mtime:   fswire.Time{Sec: st.Mtim.Sec, Nsec: uint32(st.Mtim.Nsec)},
		Ctime:   fswire.Time{Sec: st.Ctim.Sec, Nsec: uint32(st.Ctim.Nsec)},
	}, nil
}

// statfs reports filesystem-wide usage for the mount.
func statfs(path string) (*fswire.Statfs, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return nil, err
	}
	return &fswire.Statfs{
		Blocks:  st.Blocks,
		Bfree:   st.Bfree,
		Bavail:  st.Bavail,
		Files:   st.Files,
		Ffree:   st.Ffree,
		Bsize:   uint32(st.Bsize),
		NameLen: uint32(st.Namelen),
		Frsize:  uint32(st.Frsize),
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
