//go:build !windows

package client

import "syscall"

// detachAttr returns SysProcAttr that places the daemon in its own session so it
// survives the launcher (and the controlling terminal) exiting.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
