//go:build windows

package client

import "syscall"

// daemonSysProcAttr returns the SysProcAttr used when spawning the detached
// daemon. Windows has no Setsid; CREATE_NEW_PROCESS_GROUP (0x00000200) starts
// the daemon in its own process group so it is not killed when the launcher
// exits.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000200}
}
