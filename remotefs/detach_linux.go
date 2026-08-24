package remotefs

import "syscall"

// Unlinks the mount at once, and lets existing users finish against it.
const detachFlag = syscall.MNT_DETACH
