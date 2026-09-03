//go:build !unix

package sshutil

import (
	"fmt"
	"io"
	"time"
)

// OpenSSH's ControlMaster multiplexing needs a Unix-domain socket and file
// descriptor passing, neither of which Windows OpenSSH implements -- it
// rejects ControlMaster outright. This build keeps the same call shape so the
// caller's code does not fork, and refuses instead of pretending.

// ControlConn is unavailable on this platform.
type ControlConn struct{}

// DialControlMaster always fails here.
func DialControlMaster(path string) (*ControlConn, error) {
	return nil, fmt.Errorf("control sockets are not supported on this platform (%s)", path)
}

// ControlMasterAlive is always false here.
func ControlMasterAlive(path string) bool { return false }

func (c *ControlConn) Run(command string) (stdout, stderr []byte, exitCode int, err error) {
	return nil, nil, -1, fmt.Errorf("control sockets are not supported on this platform")
}

func (c *ControlConn) RunStdin(command string, stdin []byte) (stdout, stderr []byte, exitCode int, err error) {
	return nil, nil, -1, fmt.Errorf("control sockets are not supported on this platform")
}

func (c *ControlConn) RunTimeout(command string, d time.Duration) (stdout, stderr []byte, exitCode int, err error) {
	return nil, nil, -1, fmt.Errorf("control sockets are not supported on this platform")
}

func (c *ControlConn) StartStream(command string) (io.ReadWriteCloser, error) {
	return nil, fmt.Errorf("control sockets are not supported on this platform")
}

func (c *ControlConn) Close() error { return nil }
