package sshutil

import (
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const spareTarget = 2

const maxConcurrentSessions = 8

// see docs/ssh/connection.md
type CommandRunner struct {
	client  *ssh.Client
	sem     chan struct{} // bounds concurrently running commands
	mu      sync.Mutex
	spares  []*ssh.Session // pre-opened sessions ready for the next commands
	warming bool           // a prewarm goroutine is in flight
	closed  bool           // stop replenishing spares
}

// Run and RunStdin are safe for concurrent use, bounded at
// maxConcurrentSessions.
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
	return r.run(command, nil, 0)
}

// RunStdin executes a command with the given bytes piped to its stdin.
func (r *CommandRunner) RunStdin(command string, stdin []byte) (stdout, stderr []byte, exitCode int, err error) {
	if stdin == nil {
		stdin = []byte{}
	}
	return r.run(command, stdin, 0)
}

// RunTimeout executes a command and abandons it after d, closing its session.
func (r *CommandRunner) RunTimeout(command string, d time.Duration) (stdout, stderr []byte, exitCode int, err error) {
	return r.run(command, nil, d)
}

func (r *CommandRunner) run(command string, stdin []byte, timeout time.Duration) (stdout, stderr []byte, exitCode int, err error) {
	r.sem <- struct{}{}
	defer func() { <-r.sem }()

	sess, fromSpare, err := r.takeSession()
	if err != nil {
		return nil, nil, -1, err
	}
	res := r.execBounded(sess, command, stdin, timeout)
	if !res.Started && fromSpare {
		// The spare went stale before the exec, so the command never began and a
		// retry is safe.
		fresh, ferr := r.client.NewSession()
		if ferr != nil {
			return nil, nil, -1, res.Err
		}
		res = r.execBounded(fresh, command, stdin, timeout)
	}
	return res.Stdout, res.Stderr, res.ExitCode, res.Err
}

func (r *CommandRunner) execBounded(sess *ssh.Session, command string, stdin []byte, timeout time.Duration) CommandResult {
	// Closing the session is what unblocks Wait, so an abandoned run unwinds.
	abort := func() { sess.Signal(ssh.SIGKILL); sess.Close() }
	return bounded(timeout, abort, func() CommandResult {
		stdout, stderr, exitCode, started, err := execOnSession(sess, command, stdin)
		sess.Close()
		return CommandResult{Stdout: stdout, Stderr: stderr, ExitCode: exitCode, Err: err, Started: started}
	})
}

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

// Close releases the spares. The SSH client stays open, and Run still opens
// sessions on demand.
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

// started reports whether the exec request was accepted by the server; when
// false the command never began executing, so the caller may safely retry on
// another session. The caller owns closing the session.
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
