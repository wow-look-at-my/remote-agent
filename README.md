# remote-agent

`remote-agent` is a single Go binary that exposes a small, structured toolset for
operating on a **remote host over SSH** — run commands, read/write/edit files, list
directories and processes, and gather system info — with optional JSON output.

It runs a local **daemon** that holds one SSH connection and deploys a copy of
itself to the remote to service operations that need structured output there.

## Build

Use the [`go-toolchain`](https://github.com/wow-look-at-my/go-toolchain) wrapper
(recommended), or a recent Go toolchain via the `Makefile`:

```sh
go-toolchain          # tidy + vet + test + build -> build/remote-agent
# or, with plain Go:
make build            # -> ./remote-agent
make build-linux      # -> ./remote-agent-linux-amd64  (for deploying to Linux remotes)
make build-all        # -> dist/ (linux/amd64 + darwin/arm64)
```

The daemon deploys a **linux/amd64** helper to the remote, so build that variant
(`make build-linux` or `make build-all`) and keep it next to the binary — unless the
local binary is itself linux/amd64, in which case it deploys itself.

## Usage

```sh
# 1. Connect — starts a background daemon that holds the SSH connection
remote-agent connect user@host           # --port N for a non-standard SSH port

# 2. Operate on the remote
remote-agent ping
remote-agent exec "uname -a"
remote-agent ls /var/log --recursive
remote-agent read /etc/hostname
echo "hi" | remote-agent write /tmp/greeting --mode 0644
remote-agent edit /tmp/greeting --old hi --new hello
remote-agent ps --filter nginx
remote-agent sysinfo
remote-agent upload ./local.txt /tmp/remote.txt
remote-agent download /tmp/remote.txt ./local.txt
remote-agent readlink /usr/bin/python

# 3. Disconnect — stops the daemon (the helper stays cached for fast reconnects)
remote-agent disconnect
```

Add `--json` to any command for machine-readable output instead of compact text.

`exec` mirrors the remote command's **exit code** as its own, and forwards remote
stdout to stdout and stderr to stderr — so it behaves like a transparent local
shell command (e.g. `remote-agent exec false` exits 1).

### Run Claude Code against a remote host

`remote-agent claude` launches [Claude Code](https://claude.com/claude-code) with
its shell wired to run **every Bash command on the remote host** instead of
locally. It starts (or reuses) a daemon for the target and points Claude's
`CLAUDE_CODE_SHELL_PREFIX` at a shim that forwards each command through the daemon.
The model just runs ordinary shell commands — it never has to think about SSH.

```sh
remote-agent claude user@host                 # start daemon, launch claude wired to it
remote-agent claude user@host --port 2222     # non-standard SSH port
remote-agent claude user@host -- --model opus # args after -- are passed to claude
remote-agent claude                           # reuse the single running daemon
```

A daemon started by this command is stopped again when claude exits; pass
`--keep-daemon` to leave it running, or `--claude-bin` to point at a specific
`claude` executable.

> Note: each Bash call runs as a one-shot command on the remote, so shell state
> (e.g. `cd`, exported variables) does not persist between calls — use absolute
> paths or combine steps in a single command (`cd /x && make`).

### Commands

| Command | Description |
|---------|-------------|
| `connect <user@host>` | Start the daemon and SSH session. `--port` sets the SSH port (default 22). |
| `claude [user@host]` | Launch Claude Code with its Bash shell wired to run on the remote. `--port`, `--keep-daemon`, `--claude-bin`; args after `--` pass through to claude. |
| `disconnect` | Stop the daemon. The helper binary stays cached in `~/.cache/remote-agent` on the remote, so the next connect skips the upload. |
| `ping` | Check that the daemon and remote are alive. |
| `exec <command...>` | Run a shell command on the remote. |
| `ls [path]` | List a remote directory. `--recursive` walks subdirectories. |
| `read <path>` | Print a remote file (binary-safe). |
| `write <path>` | Write stdin to a remote file (binary-safe, any size). `--mode` sets permissions (octal, default 0644). |
| `edit <path>` | Find/replace in a remote file. `--old` (required) and `--new`. |
| `ps` | List remote processes. `--filter` matches by name. |
| `sysinfo` | Host, CPU, memory, disk, network, and GPU summary. |
| `upload <local> <remote>` | Copy a local file to the remote. |
| `download <remote> <local>` | Copy a remote file to the local host. |
| `readlink <path>` | Resolve a symlink target on the remote. |

## How it works

```
 remote-agent ls /tmp        ┌─ local daemon ──┐         ┌─ remote host ──┐
 (CLI client) ─JSON/unix──▶  │ handler → ops   │ ──SSH──▶ │ shell / serve  │
              ◀─JSON resp──   │ (one SSH conn)  │ ◀──────  │   subcommands  │
                             └─────────────────┘         └────────────────┘
```

- The **client** sends a JSON request over a per-target Unix socket.
- The **daemon** (`remote-agent connect`) keeps a single SSH connection open and
  runs requests concurrently, multiplexing each command onto its own SSH channel
  (bounded to stay within the server's per-connection session limit, with spare
  sessions pre-opened to cut per-command latency). File contents stream as raw
  bytes over the SSH channel; only non-UTF-8 payloads are base64-framed, and
  only across the local JSON socket hop.
- A copy of the binary is **deployed to the remote** and invoked as a hidden
  `serve` subcommand for operations that need structured output there (`sysinfo`,
  `ps`, `edit`), with start/stop/action **audit logging** to the remote's syslog.
  The helper is content-addressed and cached in `~/.cache/remote-agent`, so
  reconnects skip the multi-megabyte upload when the binary is unchanged.

Authentication uses your SSH agent and `~/.ssh` keys. Host keys follow OpenSSH
`accept-new` semantics: the first connection to an unknown host is trusted and its
key recorded in `~/.ssh/known_hosts`, and every later connection must match the
recorded key. The fingerprint is printed on connect — verify it on first contact.

There is **one daemon per target host** (the socket path is derived from the
target), so multiple terminals targeting the same host share a connection. When
more than one daemon is running, set `REMOTE_AGENT_TARGET=user@host` (or
`REMOTE_AGENT_SOCKET=/path/to.sock`) to pick which one a command talks to;
`remote-agent claude` exports this automatically so its forwarded commands always
reach the right daemon.

## Development

```sh
go-toolchain          # tidy + vet + test (with coverage) + build
gofmt -l .            # must print nothing
```

See [`CLAUDE.md`](./CLAUDE.md) for the architecture map and conventions. CI runs the
same `go-toolchain` on every push.
