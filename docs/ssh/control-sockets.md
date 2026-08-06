# Riding an OpenSSH control master

remote-agent normally dials the remote host and authenticates itself. When the
host is configured with a **ControlPath** and a master is listening on it, it
runs its commands through that master instead: sessions on a connection
somebody else already opened and authenticated.

That matters where remote-agent cannot authenticate on its own — a one-time
password, a hardware key that has already been touched, an agent held by
another login, a `ProxyJump` chain the user set up interactively. The user runs
one `ssh` by hand; every remote-agent command after that rides it.

## Using it

Nothing to configure if `~/.ssh/config` already has a ControlPath for the host:

```
Host prod
    HostName prod.example.com
    ControlMaster auto
    ControlPath ~/.ssh/cm-%r@%h:%p
    ControlPersist 10m
```

```sh
ssh prod true                 # opens the master (authenticate once, here)
remote-agent connect prod     # rides it: "Connected through control master ..."
```

`remote-agent connect` resolves the ControlPath the same way `ssh` does, by
reading `ssh -G <host>` — which expands `~` and the `%r`/`%h`/`%p` tokens, so
the value needs no further interpretation.

- **A master is used when one answers.** If the socket is absent or stale,
  remote-agent says so on stderr and dials the host itself.
- **`--control-path <socket>` makes it mandatory.** Naming a socket explicitly
  means the connection must go through it; if that master is not answering the
  connect fails rather than quietly opening a second connection to the host.
  `REMOTE_AGENT_CONTROL_PATH` does the same for daemons started automatically.
- **remote-agent never starts a master.** Creating one may need a password or a
  security key, which a daemon starting in the background cannot ask for. Start
  it yourself; `ControlPersist` keeps it alive between uses.

Everything works the same over a master: concurrent commands, the helper-binary
deploy, and the FUSE mount (a mount is one long-lived session on the master).
There is no per-command `ssh` process — remote-agent speaks the control
socket's protocol directly.

## What it does not do

- **No host-key fingerprint of ours.** The master verified the host key when it
  connected; remote-agent has no key exchange to report. `connect` prints which
  master it used instead of a fingerprint, and the startup audit entry records
  the same thing.
- **Windows is out.** OpenSSH on Windows implements no ControlMaster, and the
  protocol needs Unix-domain sockets and descriptor passing. `mux_other.go`
  refuses rather than pretending.
- **The master's own limits apply.** `MaxSessions` on the server (default 10)
  bounds the channels a master can hold, so remote-agent bounds itself to the
  same 8 concurrent commands it uses on a direct connection.

## How it works

`sshutil/mux_unix.go` implements the client half of OpenSSH's multiplexing
protocol, documented in [PROTOCOL.mux][] (version 4), in "passenger" mode:

1. Connect to the control socket, exchange `MUX_MSG_HELLO` (version 4).
2. `MUX_C_ALIVE_CHECK` at connect time — that is what distinguishes a live
   master from a socket file left behind by a dead one.
3. Per command: a fresh connection to the socket, then `MUX_C_NEW_SESSION`
   naming the command, followed by three descriptors passed as `SCM_RIGHTS`
   messages (one byte of payload each, exactly as OpenSSH's `mm_send_fd`
   sends them) to serve as the command's stdin, stdout and stderr. They are
   ends of `socketpair(2)`s, so the daemon reads and writes the command's
   stdio directly.
4. The master replies `MUX_S_SESSION_OPENED`, and when the command finishes,
   `MUX_S_EXIT_MESSAGE` with its exit status.

The client stays connected until the master closes the control connection:
PROTOCOL.mux notes that hanging up early makes the master tear the session
down, which can discard output still in flight. A session that ends with no
exit message is reported as a failure — an exit status the daemon did not
receive must never be reported as a zero.

Why speak the protocol rather than run `ssh` per command: the master owns the
SSH transport and there is no way to borrow it as a raw connection for
`x/crypto/ssh`, so the socket protocol is the only in-process route. It also
keeps the daemon's latency profile — no fork/exec on the path of every command.

## Testing it

`sshutil/mux_test.go` runs the client against a fake master that speaks the
protocol and runs each command locally on the descriptors it is passed. That
covers the wire format, descriptor passing, exit codes, stderr separation,
concurrency and the failure paths, with no SSH host involved.

It cannot prove the client matches *OpenSSH's* implementation, so the same file
has `TestLiveControlMaster`, skipped unless a socket is named:

```sh
ssh -M -N -f -o ControlPath=/tmp/cm.sock user@host
REMOTE_AGENT_LIVE_CONTROL_PATH=/tmp/cm.sock go test ./sshutil/ -run TestLiveControlMaster -v
```

CI has no SSH host, so that check is manual — run it when changing anything in
`mux_unix.go`.

[PROTOCOL.mux]: https://github.com/openssh/openssh-portable/blob/master/PROTOCOL.mux
