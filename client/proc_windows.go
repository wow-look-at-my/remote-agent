//go:build windows

package client

import "syscall"

// daemonSysProcAttr returns the SysProcAttr used when spawning the detached
// daemon.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000200}
}
