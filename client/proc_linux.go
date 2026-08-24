//go:build linux

package client

import "syscall"

// Setsid puts the daemon in its own session, so it survives the launcher and the terminal.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
