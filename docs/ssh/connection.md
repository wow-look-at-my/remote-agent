# The SSH connection

`sshutil/` holds the one connection a daemon owns, the session pool that runs
commands on it, and the two transports that can carry them. See
`docs/ssh/control-sockets.md` for the control-master protocol itself.

## Resolving ssh_config

`sshGCommand` builds the `ssh -G <host>` command the resolver runs. It passes
the login and the port along whenever the target names them, because ssh
expands the `%r` and `%p` tokens of a ControlPath with the values it is given.
Resolving a host on port 2201 without `-p` reports the socket of the host on
port 22. The resolved `ControlPath` comes back already expanded -- `ssh -G`
resolves `~` and the tokens -- so it is a path that can be dialed as it is.

A port ssh_config resolves stays out of the daemon's identity. The client
cannot see it without running `ssh -G` itself, so folding it in would key the
daemon to a socket the caller never waits on.

## Control master or dialed connection

`ControlPath` in `ConnectOptions` names a control master to run commands
through, instead of dialing and authenticating a second connection. Empty
dials. `RequireControl` makes an unusable ControlPath an error rather than a
reason to dial: a caller that asked for one specific socket gets that socket
or a failure, never a silent second connection to the host.

An auto-detected socket from ssh_config that does not answer is different. The
connection falls back to dialing, and says so on stderr first. Silently
opening a second connection to a host the user expected to be shared is
exactly what confuses them.

The address is built with `net.JoinHostPort`, not `"%s:%d"`. An IPv6 literal
needs brackets, and `knownhosts` normalizes the same bracketed form when it
records the key.

## Keepalive

`keepAlive` pings the remote on an interval, so idle NAT or firewall state
does not drop the connection, and stops as soon as the connection is gone.

The ping deliberately does **not** request a reply, and the loop watches
`client.Wait()` rather than relying on `SendRequest` to report the disconnect.
A reply-wanting global request is unusable here. `golang.org/x/crypto/ssh`
v0.52 drains buffered responses with `select { case <-m.globalResponses:
default: }`, and once the connection closes that channel is closed, so the
receive is always ready and the drain loop spins forever at 100% CPU instead
of returning an error (`ssh/mux.go:158`). A no-reply ping returns the write
error directly, and `client.Wait()` covers a write that lands in a dead
socket's buffer.

## The session pool

Opening a session costs a full network round trip
(`SSH_MSG_CHANNEL_OPEN` then `CONFIRMATION`). `CommandRunner` keeps a small
pool of pre-opened sessions warm, so that round trip leaves every command's
latency: the next sessions open in the background while the current command
runs.

- `spareTarget` is 2. That covers the daemon's steady state, where each
  operation consumes one session for the command and one for its concurrent
  audit write.
- `maxConcurrentSessions` is 8. With the spare pool, that stays inside
  OpenSSH's default `MaxSessions` of 10 channels per connection.

`Run` and `RunStdin` are safe for concurrent use: SSH multiplexes each command
onto its own channel, and the runner bounds in-flight commands. When a
pre-opened session goes stale before the exec request is accepted, the command
never began, so the runner retries on a fresh session. If the connection
itself is down, that fails too.

`Close` releases the spares and stops replenishing them. It does not close the
SSH client, and `Run`/`RunStdin` still work afterwards by opening sessions on
demand.

## Streams

A `Stream` is a long-lived remote command whose stdin and stdout stay open as
a bidirectional byte stream. It is the transport for the filesystem mount: one
process on the remote serving thousands of operations, rather than the
request-and-response commands `CommandRunner` issues. It holds its own SSH
channel for its whole life, outside the pool, so ordinary commands keep
running alongside a mount.

`ControlConn` (`sshutil/mux_unix.go`) is the control-master transport. Each
command opens its own connection to the control socket and its own session on
the master's SSH transport, so commands run concurrently exactly as they do
over a direct connection. `DialControlMaster` probes the master with an alive
check, so a stale socket file left by a dead master fails there rather than on
the first command. `Close` releases only this side: the master belongs to
whoever started it.
