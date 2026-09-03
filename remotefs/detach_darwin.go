package remotefs

import "golang.org/x/sys/unix"

// detachFlag forces the unmount.
const detachFlag = unix.MNT_FORCE
