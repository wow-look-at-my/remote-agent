package remotefs

import "golang.org/x/sys/unix"

// detachFlag forces the unmount. macOS has no MNT_DETACH, and Go's darwin
// syscall package does not export MNT_FORCE, so the constant comes from
// x/sys/unix rather than being hardcoded.
const detachFlag = unix.MNT_FORCE
