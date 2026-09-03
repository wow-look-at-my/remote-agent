package sshutil

import (
	"fmt"
	"io"

	"golang.org/x/crypto/ssh"
)

// Stream is the mount's transport, on its own SSH channel. see
// docs/ssh/connection.md
type Stream struct {
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
}

// StartStream runs command on the remote and returns its stdin/stdout as a
// stream. Stderr is discarded: the helper reports failures in-band, and a
// blocked stderr pipe would stall the session.
func StartStream(client *ssh.Client, command string) (*Stream, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("open stdin pipe: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("open stdout pipe: %w", err)
	}
	session.Stderr = io.Discard

	if err := session.Start(command); err != nil {
		session.Close()
		return nil, fmt.Errorf("start %q: %w", command, err)
	}
	return &Stream{session: session, stdin: stdin, stdout: stdout}, nil
}

// Write sends bytes to the remote command's stdin.
func (s *Stream) Write(p []byte) (int, error) { return s.stdin.Write(p) }

// Read receives bytes from the remote command's stdout.
func (s *Stream) Read(p []byte) (int, error) { return s.stdout.Read(p) }

// Close ends the remote command and releases the SSH channel.
func (s *Stream) Close() error {
	// EOF lets a well-behaved helper exit. The session close reclaims the
	// channel regardless.
	s.stdin.Close()
	return s.session.Close()
}
