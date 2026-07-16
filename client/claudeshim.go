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

// This file implements the hidden `claude-shim` command -- the program that
// CLAUDE_CODE_SHELL_PREFIX invokes (via client/shellprefix.sh) when Claude Code
// is launched through `remote-agent claude`.
//
// Claude Code v2.1.185+ wraps THREE kinds of spawns with the shell prefix,
// passing each full command line as ONE argument:
//
//  1. Bash tool commands -- wrapped in machine-generated scaffolding (below).
//     These must run on the REMOTE host; that is the whole point of the
//     launcher.
//  2. Hook commands (SessionStart etc.) -- passed bare. Claude injects env vars
//     (CLAUDE_PROJECT_DIR, CLAUDE_ENV_FILE, ...) into the LOCAL spawn
//     environment only, and the hook script exists on the local machine, so
//     hooks must run LOCALLY -- exactly as they did before v2.1.185, when hook
//     commands were not prefix-wrapped at all.
//  3. MCP stdio servers -- long-lived bidirectional JSON-RPC processes. The
//     buffered request/response remote exec path can never carry them (no
//     stdin bridge), and like hooks they rely on local env and local files, so
//     they must run LOCALLY too.
//
// The Bash tool wrapper built by v2.1.185 for a user command <cmd> is a single
// "&&"-joined string:
//
//	source <local snapshot path> 2>/dev/null || true &&   [iff the snapshot exists locally]
//	<session-env preamble text>\n: &&                      [iff a hook wrote session env]
//	{ shopt -u extglob || setopt NO_EXTENDED_GLOB NO_BARE_GLOB_QUAL; } >/dev/null 2>&1 || true &&
//	eval '<cmd>' < /dev/null &&
//	pwd -P >| <local tmpdir>/claude-<id>-cwd
//
// Two of those clauses are poison on a remote host:
//
//   - The leading `source` of the LOCAL shell-snapshot file. The file does not
//     exist remotely (and sourcing a local bash state dump would be wrong even
//     if it did). On BusyBox ash `source` is the POSIX special builtin `.`, so
//     the failed open is a FATAL shell abort that `|| true` cannot rescue and
//     `2>/dev/null` silences -- every command returns zero bytes with exit 2,
//     which Claude renders as "(No output)". bash remotes survive only because
//     bash's `source` is non-fatal; dash only because it has no `source` at
//     all.
//   - The trailing `pwd -P >| <path>` writes Claude's cwd-tracking file into
//     the REMOTE /tmp. The local file Claude actually reads is never written,
//     so Claude reports "Shell cwd was reset" after every command.
//
// launderBashToolWrapper strips exactly those two clauses before forwarding;
// everything between them (the inlined session-env text, the glob-setup
// marker, the eval) is remote-safe and forwarded verbatim. After a successful
// remote run the shim writes the local cwd file itself with its own working
// directory -- which is what `pwd -P` would have printed for a local run that
// did not `cd` -- keeping Claude's cwd tracker quiet.

// bashToolMarker is the glob-setup clause Claude Code v2.1.185 embeds in every
// Bash tool wrapper when CLAUDE_CODE_SHELL_PREFIX is set (and only then -- an
// unset prefix gets a shell-specific `shopt`/`setopt` form instead, but then no
// shim runs at all). Hook and MCP command lines are user configuration and
// cannot plausibly contain this machine-generated text, so its presence is the
// classification signal: marker => Bash tool command (forward to the remote),
// no marker => hook/MCP/other (run locally).
//
// Misclassification risks are asymmetric: forwarding a hook remotely breaks it
// (missing env/files), but running a Bash tool command locally would silently
// execute the user's command on the WRONG machine -- worse. The marker is the
// most conservative signal available for telling the two apart.
const bashToolMarker = "{ shopt -u extglob || setopt NO_EXTENDED_GLOB NO_BARE_GLOB_QUAL; } >/dev/null 2>&1 || true"

// snapshotPathMarker identifies Claude Code's shell-snapshot files: they always
// live in a `shell-snapshots` directory and are named `snapshot-<shell>-...`.
// The leading source clause is stripped only when the sourced path matches, so
// nothing that is not a Claude snapshot reference is ever removed.
const snapshotPathMarker = "shell-snapshots/snapshot-"

// cwdTailSeparator joins the final cwd-tracking clause onto the wrapper.
const cwdTailSeparator = " && pwd -P >| "

// runLocalFunc is a seam so tests can intercept the local-execution path (the
// real Unix implementation replaces this process via exec(2) and never
// returns).
var runLocalFunc = runLocal

// Test seams: the portable local runner wires the child process to these
// streams. They default to the real stdio (as *os.File values, so os/exec
// passes the file descriptors straight through with no copying goroutines --
// which is what long-lived bidirectional MCP stdio servers need).
var (
	localStdin  io.Reader = os.Stdin
	localStdout io.Writer = os.Stdout
	localStderr io.Writer = os.Stderr
)

// RunClaudeShim handles one CLAUDE_CODE_SHELL_PREFIX invocation: Bash tool
// wrappers are laundered and forwarded to the remote host through the daemon,
// everything else (hooks, MCP stdio servers) runs locally with inherited env
// and stdio. It returns the command's exit code; a non-nil error indicates a
// transport/daemon failure rather than the command itself failing.
func RunClaudeShim(command string) (int, error) {
	if IsBashToolWrapper(command) {
		return forwardBashToolWrapper(command)
	}
	return runLocalFunc(command)
}

// IsBashToolWrapper reports whether command is a Claude Code Bash tool wrapper
// (as opposed to a hook or MCP server command line). See bashToolMarker.
func IsBashToolWrapper(command string) bool {
	return strings.Contains(command, bashToolMarker)
}

// forwardBashToolWrapper launders the wrapper and runs it on the remote host,
// mirroring the remote exit code. On success it writes Claude's local
// cwd-tracking file (the `&&` semantics of the original tail: the file is
// written only when the command succeeded).
func forwardBashToolWrapper(command string) (int, error) {
	laundered, cwdFile := launderBashToolWrapper(command)
	code, err := Exec(laundered)
	if err != nil {
		return 0, err
	}
	if code == 0 && cwdFile != "" {
		writeCwdFile(cwdFile)
	}
	return code, nil
}

// launderBashToolWrapper strips the two local-machine clauses from a Bash tool
// wrapper: the leading shell-snapshot source (fatal on BusyBox ash, wrong
// everywhere remotely) and the trailing cwd-file write (litters the remote
// /tmp; the path is local). It returns the remote-safe remainder and the local
// cwd file path captured from the tail ("" when no tail was present). Clauses
// that do not match exactly are left untouched -- unrecognized input is
// forwarded unchanged rather than mangled.
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

// stripCwdTail removes a trailing
//
//	&& pwd -P >| <path>
//
// clause and returns the remainder plus the unquoted <path>. The genuine tail
// is always the last thing in the wrapper, so the last separator occurrence is
// used and the path token must consume the entire remainder -- a lookalike
// sequence inside the quoted eval body never matches.
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
