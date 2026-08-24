package client

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The hidden `claude-shim` command: what CLAUDE_CODE_SHELL_PREFIX invokes.
// Claude wraps Bash tool commands, hooks and MCP servers alike, and only the
// first kind may reach the remote. see docs/claude/shim.md

// Present in every Bash tool wrapper and in no hook or MCP command line, so it is the classifier.
const bashToolMarker = "{ shopt -u extglob || setopt NO_EXTENDED_GLOB NO_BARE_GLOB_QUAL; } >/dev/null 2>&1 || true"

// Claude's shell snapshots. Only a source clause naming one of these is stripped.
const snapshotPathMarker = "shell-snapshots/snapshot-"

// cwdTailSeparator joins the final cwd-tracking clause onto the wrapper.
const cwdTailSeparator = " && pwd -P >| "

// A seam: the real Unix path replaces this process through exec(2) and never returns.
var runLocalFunc = runLocal

// Test seams. These stay *os.File, so os/exec hands the descriptors straight to the
// child with no copying goroutines -- which is what a long-lived MCP stdio server needs.
var (
	localStdin  io.Reader = os.Stdin
	localStdout io.Writer = os.Stdout
	localStderr io.Writer = os.Stderr
)

// RunClaudeShim forwards a Bash tool wrapper to the remote and runs everything else
// locally. The error is a transport failure; a failing command shows in the exit code.
func RunClaudeShim(command string) (int, error) {
	if IsBashToolWrapper(command) {
		return forwardBashToolWrapper(command)
	}
	return runLocalFunc(command)
}

// IsBashToolWrapper tells a Bash tool wrapper from a hook or MCP command line.
func IsBashToolWrapper(command string) bool {
	return strings.Contains(command, bashToolMarker)
}

// forwardBashToolWrapper launders the wrapper and runs it on the remote host,
// mirroring the remote exit code. On success it writes Claude's local
// cwd-tracking file (the `&&` semantics of the original tail: the file is
// written only when the command succeeded).
func forwardBashToolWrapper(command string) (int, error) {
	laundered, cwdFile := launderBashToolWrapper(command)
	laundered = prefixWorkingDirectory(laundered)
	code, err := Exec(laundered)
	if err != nil {
		return 0, err
	}
	if code == 0 && cwdFile != "" {
		writeCwdFile(cwdFile)
	}
	return code, nil
}

// prefixWorkingDirectory runs a forwarded command where claude is working, not in the
// remote home. Only a same-path mount makes that directory valid remotely.
func prefixWorkingDirectory(command string) string {
	mount := os.Getenv("REMOTE_AGENT_MOUNT")
	if mount == "" {
		return command
	}
	wd, err := os.Getwd()
	if err != nil {
		return command
	}
	if wd != mount && !strings.HasPrefix(wd, mount+"/") {
		return command
	}
	// && and not ;: a failed cd must abort, not run the command elsewhere.
	return "cd " + shellQuote(wd) + " && " + command
}

// shellQuote wraps a path in single quotes for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// launderBashToolWrapper strips the two local-machine clauses and returns the
// remote-safe remainder plus the local cwd file path. see docs/claude/shim.md
func launderBashToolWrapper(command string) (laundered, cwdFilePath string) {
	laundered = stripSnapshotSource(command)
	laundered, cwdFilePath = stripCwdTail(laundered)
	return laundered, cwdFilePath
}

// stripSnapshotSource removes a leading
//
//	source <snapshot path> 2>/dev/null || true &&
//
// clause where <snapshot path> references a Claude shell-snapshot file. The
// clause is absent when local snapshot creation failed (Claude then spawns a
// login shell instead), so a non-matching command is returned unchanged.
func stripSnapshotSource(command string) string {
	rest, ok := strings.CutPrefix(command, "source ")
	if !ok {
		return command
	}
	path, rest, ok := cutShellToken(rest)
	if !ok || !strings.Contains(path, snapshotPathMarker) {
		return command
	}
	rest, ok = strings.CutPrefix(rest, " 2>/dev/null || true && ")
	if !ok {
		return command
	}
	return rest
}

// stripCwdTail removes a trailing "&& pwd -P >| <path>" and returns the remainder
// plus the unquoted path. It matches the last occurrence only, so a lookalike inside
// the quoted eval body never wins.
func stripCwdTail(command string) (string, string) {
	i := strings.LastIndex(command, cwdTailSeparator)
	if i < 0 {
		return command, ""
	}
	path, rest, ok := cutShellToken(command[i+len(cwdTailSeparator):])
	if !ok || rest != "" {
		return command, ""
	}
	return command[:i], path
}

// cutShellToken parses one shell token from the start of s as produced by
// Claude Code's quoting: either a bare run of the safe charset
// [A-Za-z0-9_./:=@+,-] (paths usually qualify and are emitted unquoted), or a
// single-quoted string in which each embedded quote is encoded as '"'"'.
// It returns the unquoted value and the remainder of s.
func cutShellToken(s string) (value, rest string, ok bool) {
	if s == "" {
		return "", "", false
	}
	if s[0] != '\'' {
		n := 0
		for n < len(s) && isShellSafeByte(s[n]) {
			n++
		}
		if n == 0 {
			return "", "", false
		}
		return s[:n], s[n:], true
	}
	var b strings.Builder
	i := 1
	for i < len(s) {
		if s[i] != '\'' {
			b.WriteByte(s[i])
			i++
			continue
		}
		if strings.HasPrefix(s[i:], `'"'"'`) {
			b.WriteByte('\'')
			i += len(`'"'"'`)
			continue
		}
		return b.String(), s[i+1:], true // closing quote
	}
	return "", "", false // unterminated quote
}

// isShellSafeByte reports whether c is in the charset Claude Code's quoting
// leaves bare: /^[A-Za-z0-9_./:=@+,-]+$/.
func isShellSafeByte(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '_', '.', '/', ':', '=', '@', '+', ',', '-':
		return true
	}
	return false
}

// writeCwdFile writes Claude's local cwd-tracking file with this process's
// working directory -- what `pwd -P` would report for a local run that did not
// `cd` (Claude spawns the prefix in the directory it is tracking). Failure is
// only warned about: the remote command already succeeded, and failing the
// whole call over a local telemetry file would misreport the command itself as
// broken.
func writeCwdFile(path string) {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote-agent: warning: cannot determine working directory for cwd file: %v\n", err)
		return
	}
	// pwd -P reports the physical directory; resolve symlinks to match.
	if resolved, rerr := filepath.EvalSymlinks(wd); rerr == nil {
		wd = resolved
	}
	if err := os.WriteFile(path, []byte(wd+"\n"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "remote-agent: warning: cannot write cwd file: %v\n", err)
	}
}

// runLocalPortable executes command with the local shell as an ordinary child
// process, with inherited environment and stdio, and returns its exit code.
// It is the local-execution fallback (and the whole implementation on
// platforms without exec(2)).
func runLocalPortable(command string) (int, error) {
	c := exec.Command(localShellPath(), "-c", command)
	c.Stdin = localStdin
	c.Stdout = localStdout
	c.Stderr = localStderr
	err := c.Run()
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return 0, fmt.Errorf("run local command: %w", err)
}
