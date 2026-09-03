package fswire

import "os"

// Portable open flags: raw O_* values differ per platform. see
// docs/mount/behaviour.md
const (
	OpenRead   = 1 << 0
	OpenWrite  = 1 << 1
	OpenAppend = 1 << 2
	OpenTrunc  = 1 << 3
	OpenExcl   = 1 << 4
	OpenSync   = 1 << 5
)

// PortableOpenFlags converts local O_* flags to the wire representation.
func PortableOpenFlags(local int) uint32 {
	var flags uint32
	switch local & (os.O_RDONLY | os.O_WRONLY | os.O_RDWR) {
	case os.O_WRONLY:
		flags |= OpenWrite
	case os.O_RDWR:
		flags |= OpenRead | OpenWrite
	default:
		flags |= OpenRead
	}
	for _, bit := range []struct {
		local    int
		portable uint32
	}{
		{os.O_APPEND, OpenAppend},
		{os.O_TRUNC, OpenTrunc},
		{os.O_EXCL, OpenExcl},
		{os.O_SYNC, OpenSync},
	} {
		if local&bit.local != 0 {
			flags |= bit.portable
		}
	}
	return flags
}

// LocalOpenFlags converts wire flags back to this platform's O_* values.
// O_CREATE is not represented: it is implied by the create operation itself,
// so an open can never accidentally create a file.
func LocalOpenFlags(flags uint32) int {
	local := os.O_RDONLY
	switch {
	case flags&OpenRead != 0 && flags&OpenWrite != 0:
		local = os.O_RDWR
	case flags&OpenWrite != 0:
		local = os.O_WRONLY
	}
	for _, bit := range []struct {
		portable uint32
		local    int
	}{
		{OpenAppend, os.O_APPEND},
		{OpenTrunc, os.O_TRUNC},
		{OpenExcl, os.O_EXCL},
		{OpenSync, os.O_SYNC},
	} {
		if flags&bit.portable != 0 {
			local |= bit.local
		}
	}
	return local
}
