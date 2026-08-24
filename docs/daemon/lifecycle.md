# Daemon lifecycle

Depth behind the short comments in `daemon/daemon.go`. The daemon holds one SSH
connection for one target and serves requests over a Unix socket until it is
told to stop or idles out.

## Identity: the target, port included

`SocketPath`, `PIDPath` and `TargetPath` hash the *canonical* target, so
`user@host:2201` and `user@host:2202` are two daemons, and every spelling of one
endpoint -- a port in the target, `--port`, `REMOTE_AGENT_PORT` -- is the same
daemon. `daemon/target.go` does the parsing and the merge; `CLAUDE.md` records
why a port resolved from `ssh_config` deliberately stays out of the identity.

`Start` therefore canonicalizes before it does anything else, and reuses a
daemon that is already answering on that socket rather than dialing and
deploying a second time.

## ssh_config resolution

A bare target may be a `~/.ssh/config` Host alias, so `Start` runs `ssh -G` and
fills in only the values the caller did not supply: hostname, user, port,
ControlPath. The login and the port are passed to `ssh -G` because a ControlPath
containing `%r` or `%p` resolves differently without them.

A control path the caller named is a requirement rather than a hint: the point
of naming one is that no second connection to the host is opened. `Start` fails
instead of dialing when that master is not answering.

## Idle accounting

`opStart` and `opEnd` bracket every request. The watchdog never shuts the daemon
down while operations are in flight, however long they run, and `opEnd`
refreshes the activity stamp so the idle countdown starts when a long command
finishes rather than when it began. A live mount also holds the daemon open:
unmounting one because nobody ran a command for a while would break every
process with a file open under it.

## Audit entries

`auditAsync` runs the audit command on its own SSH channel, concurrently with
the operation it describes, which halves the round trips per operation compared
with running it serially first. `shutdown` drains the in-flight audits through
`auditWG`, so a graceful shutdown loses no entries. Audit failures are ignored,
exactly as they were when the call was serial.

## Shutdown order

`shutdown` detaches mounts first: they run over the SSH connection it is about
to close, and a mountpoint whose backing connection is gone hangs every process
that touches it. It then drains audits, writes the shutdown entry, and removes
the helper binary unless the binary lives in the remote's content-addressed
cache directory.

`cleanup` removes the socket and PID files *before* closing the listener.
Closing the listener unblocks the accept loop, whose return exits the process,
and that race left the PID file behind on every clean disconnect. The target
record is deliberately kept -- it is what lets the next command start a daemon
without being told the target.

## Deploying the helper

`deployBinaryData` content-addresses the helper by the sha256 of its bytes under
`~/.cache/remote-agent`, so a reconnect finds the identical binary already in
place and skips the multi-megabyte upload. The cache lives under `$HOME` rather
than world-writable `/tmp`, so no other user can pre-plant or swap the file, and
uploads go through a unique temp path plus a rename, so a concurrent connect
never observes a partial binary. With no usable `$HOME` the helper goes to a
random `/tmp` path and is removed again on disconnect.
