# The bound on a remote command

An MCP server answers requests in arrival order (`mcpserver/server.go`), and a
client has no way to cancel one. A tool call that never returns therefore does
not fail a single call: it takes the whole session, because every later call
queues behind it. `tail -f`, a dev server, a build waiting on a prompt and a
command reading a fifo all do this.

So `exec` -- the one action whose text the caller wrote -- carries a deadline.
Everything else the daemon runs ends on its own: `cat`, `find`, `readlink` and
the helper subcommands are all bounded by the work they describe.

## What the deadline does

`sshutil.bounded` runs the command and, when the deadline passes, aborts the
session: `Signal(SIGKILL)` then `Close()` on a dialed connection, and a session
close on a control master, which is what tells the master to tear the command
down. Closing is also what unblocks the waiting goroutine, so the session slot
comes back rather than being held by a command nobody is reading any more.

The caller gets an error naming the deadline and saying the command may still
be running on the remote host. That last part is honest rather than defensive:
the remote process belongs to the remote, and a program that ignores its hangup
keeps running there. Nothing this side can promise otherwise.

## Choosing one

| | |
|---|---|
| default | `protocol.ExecDefaultTimeout` |
| maximum | `protocol.ExecMaxTimeout` |
| MCP | the `timeout` argument on `run_command`, in seconds |
| CLI | `REMOTE_AGENT_TIMEOUT`, in seconds |

The MCP argument is per call because a model cannot restart its own server: a
deadline that could only be set when the server started would not exist for it
(the per-call rule in CLAUDE.md's Conventions).

A `REMOTE_AGENT_TIMEOUT` that is set but is not a positive number is refused,
rather than falling back to the default. Falling back would read, to whoever
set it, as the deadline having been raised.

The maximum exists so that "never" cannot be spelled as a number large enough
to slip past a reader. A genuinely long job wants `nohup`, a job runner, or a
command that returns a handle -- not a call held open for a day.
