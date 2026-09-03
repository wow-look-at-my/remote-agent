//go:build !cosmo

package remotefs

import "syscall"

const detachFlag = syscall.MNT_DETACH
