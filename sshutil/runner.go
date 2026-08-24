package sshutil

import (
	"fmt"
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Two spares cover the steady state: one session per command, one per audit write.
const spareTarget = 2

// 8 running plus 2 spares stays inside OpenSSH's default MaxSessions of 10.
const maxConcurrentSessions = 8

// CommandRunner keeps sessions pre-opened, because opening one costs a round trip
// that would otherwise sit in every command's latency. see docs/ssh/connection.md
type CommandRunner struct {
	client  *ssh.Client
	sem     chan struct{} // bounds concurrently running commands
	mu      sync.Mutex
	spares  []*ssh.Session // pre-opened sessions ready for the next commands
	warming bool           // a prewarm goroutine is in flight
	closed  bool           // stop replenishing spares
}

// NewCommandRunner starts pre-opening sessions at once. Run and RunStdin are
// safe for concurrent use, bounded at maxConcurrentSessions.
func NewCommandRunner(client *ssh.Client) *CommandRunner {
	r := &CommandRunner{
		client: client,
		sem:    make(chan struct{}, maxConcurrentSessions),
	}
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
	r.sem <- struct{}{}
	defer func() { <-r.sem }()

	sess, fromSpare, err := r.takeSession()
	if err != nil {
		return nil, nil, -1, err
	}
	stdout, stderr, exitCode, started, err := execOnSession(sess, command, stdin)
	sess.Close()
	if !started && fromSpare {
		// The spare went stale before the exec, so the command never began and a retry is safe.
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

// Close releases the spares. The SSH client stays open, and Run still opens sessions on demand.
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
