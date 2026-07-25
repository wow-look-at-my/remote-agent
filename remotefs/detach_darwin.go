package remotefs

import "syscall"

// detachFlag forces the unmount. macOS has no MNT_DETACH; MNT_FORCE is the
// closest equivalent for taking down a mount whose server is gone.
const detachFlag = syscall.MNT_FORCE
