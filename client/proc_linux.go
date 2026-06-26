//go:build linux

package client

import "syscall"

// daemonSysProcAttr returns the SysProcAttr used when spawning the detached
// daemon. Setsid places the daemon in its own session so it survives the
// launcher (and the controlling terminal) exiting.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
