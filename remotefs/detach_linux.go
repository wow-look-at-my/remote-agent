package remotefs

import "syscall"

// detachFlag detaches a mount immediately, letting existing users finish
// against a filesystem that is already unlinked from the namespace.
const detachFlag = syscall.MNT_DETACH
