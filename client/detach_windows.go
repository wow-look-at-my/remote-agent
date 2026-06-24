//go:build windows

package client

import "syscall"

// detachAttr returns SysProcAttr that starts the daemon in its own process group
// so it is not killed when the launcher exits.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000200} // CREATE_NEW_PROCESS_GROUP
}
