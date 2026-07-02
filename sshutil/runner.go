package sshutil

import (
	"fmt"
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

// spareTarget is how many pre-opened sessions the runner keeps warm. Two
// covers the daemon's steady state: each operation consumes one session for
// the command itself and one for its concurrent audit write.
const spareTarget = 2

// CommandRunner executes commands over a persistent SSH connection. It keeps
// a small pool of pre-opened sessions warm: opening a session costs a full
// network round trip (SSH_MSG_CHANNEL_OPEN -> CONFIRMATION), so opening the
// next sessions in the background while the current command runs removes that
// round trip from every command's latency.
type CommandRunner struct {
	client  *ssh.Client
	mu      sync.Mutex
	spares  []*ssh.Session // pre-opened sessions ready for the next commands
	warming bool           // a prewarm goroutine is in flight
	closed  bool           // stop replenishing spares
}

// NewCommandRunner returns a runner for the given SSH client and starts
// pre-opening sessions immediately.
func NewCommandRunner(client *ssh.Client) *CommandRunner {
	r := &CommandRunner{client: client}
	r.prewarmAsync()
	return r
}

// Run executes a command and returns stdout, stderr, and the exit code.
func (r *CommandRunner) Run(command string) (stdout, stderr []byte, exitCode int, err error) {
	return r.run(command, nil)
}

// RunStdin executes a command with the given bytes piped to its stdin.
func (r *CommandRunner) RunStdin(command string, stdin []byte) (stdout, stderr []byte, exitCode int, err error) {
	if stdin == nil {
		stdin = []byte{}
	}
	return r.run(command, stdin)
}

func (r *CommandRunner) run(command string, stdin []byte) (stdout, stderr []byte, exitCode int, err error) {
	sess, fromSpare, err := r.takeSession()
	if err != nil {
		return nil, nil, -1, err
	}
	stdout, stderr, exitCode, started, err := execOnSession(sess, command, stdin)
	sess.Close()
	if !started && fromSpare {
		// The pre-opened session went stale before the exec request was
		// accepted (the command never began), so a retry is safe. Use a fresh
		// session; if the connection itself is down this fails too.
		fresh, ferr := r.client.NewSession()
		if ferr != nil {
			return nil, nil, -1, err
		}
		defer fresh.Close()
		stdout, stderr, exitCode, _, err = execOnSession(fresh, command, stdin)
	}
	return stdout, stderr, exitCode, err
}

// takeSession returns a session for one command, preferring a pre-opened
// spare, and kicks off replenishment in the background.
func (r *CommandRunner) takeSession() (sess *ssh.Session, fromSpare bool, err error) {
	r.mu.Lock()
	if n := len(r.spares); n > 0 {
		sess = r.spares[n-1]
		r.spares = r.spares[:n-1]
	}
	r.mu.Unlock()

	r.prewarmAsync()

	if sess != nil {
		return sess, true, nil
	}
	fresh, err := r.client.NewSession()
	if err != nil {
		return nil, false, fmt.Errorf("new session: %w", err)
	}
	return fresh, false, nil
}

// prewarmAsync tops the spare pool back up to spareTarget in the background.
// At most one prewarm goroutine runs at a time.
func (r *CommandRunner) prewarmAsync() {
	r.mu.Lock()
	if r.closed || r.warming || len(r.spares) >= spareTarget {
		r.mu.Unlock()
		return
	}
	r.warming = true
	r.mu.Unlock()

	go func() {
		for {
			sess, err := r.client.NewSession()
			r.mu.Lock()
			if err != nil || r.closed || len(r.spares) >= spareTarget {
				r.warming = false
				r.mu.Unlock()
				if sess != nil {
					sess.Close()
				}
				return
			}
			r.spares = append(r.spares, sess)
			if len(r.spares) >= spareTarget {
				r.warming = false
				r.mu.Unlock()
				return
			}
			r.mu.Unlock()
		}
	}()
}

// Close releases the pre-opened spare sessions and stops replenishing them.
// The underlying SSH client is not closed, and Run/RunStdin still work
// afterwards (they fall back to opening sessions on demand).
func (r *CommandRunner) Close() {
	r.mu.Lock()
	spares := r.spares
	r.spares = nil
	r.closed = true
	r.mu.Unlock()
	for _, s := range spares {
		s.Close()
	}
}

// execOnSession runs one command on an already-open session. started reports
// whether the exec request was accepted by the server; when false the command
// never began executing, so the caller may safely retry on another session.
// The caller owns closing the session.
func execOnSession(session *ssh.Session, command string, stdin []byte) (stdout, stderr []byte, exitCode int, started bool, err error) {
	var outBuf, errBuf safeBuffer
	session.Stdout = &outBuf
	session.Stderr = &errBuf

	var stdinPipe io.WriteCloser
	if stdin != nil {
		stdinPipe, err = session.StdinPipe()
		if err != nil {
			return nil, nil, -1, false, fmt.Errorf("stdin pipe: %w", err)
		}
	}

	if err := session.Start(command); err != nil {
		return nil, nil, -1, false, fmt.Errorf("start command: %w", err)
	}

	if stdinPipe != nil {
		if _, err := stdinPipe.Write(stdin); err != nil {
			return outBuf.Bytes(), errBuf.Bytes(), -1, true, fmt.Errorf("write stdin: %w", err)
		}
		stdinPipe.Close()
	}

	err = session.Wait()
	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			return outBuf.Bytes(), errBuf.Bytes(), exitErr.ExitStatus(), true, nil
		}
		return outBuf.Bytes(), errBuf.Bytes(), -1, true, err
	}
	return outBuf.Bytes(), errBuf.Bytes(), 0, true, nil
}
