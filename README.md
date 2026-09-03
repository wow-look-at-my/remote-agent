# remote-agent

`remote-agent` is a single Go binary that exposes a small, structured toolset for
operating on a **remote host over SSH** — run commands, read/write/edit files, list
directories and processes, search, and gather system info — with optional JSON output.

It can also **mount the remote filesystem locally**, so that every program on your
machine — editors, compilers, `grep`, and AI agent tools alike — works on remote
files through ordinary paths.

It runs a local **daemon** that holds one SSH connection and deploys a copy of
itself to the remote to service operations that need structured output there.

## Install

Homebrew (macOS or Linux):

```sh
brew tap pazer/build https://brew.pazer.build/tap.git
brew trust pazer/build
brew install pazer/build/remote-agent
```

Or download a prebuilt binary directly:
`https://dl.pazer.build/remote-agent?os=<linux|darwin|windows>&arch=<amd64|arm64>`.

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
# Just run a command — the daemon starts itself on first use
remote-agent ls /var/log --target user@host

# ...and the target is remembered, so later commands need no flag
remote-agent read /etc/hostname

# Connecting up front is optional; it just moves the SSH setup out of the
# first command
remote-agent connect user@host           # user@host:2222, or --port N, for a non-standard SSH port

remote-agent ping
remote-agent exec "uname -a"
remote-agent ls /var/log --recursive
remote-agent read /etc/hostname
echo "hi" | remote-agent write /tmp/greeting --mode 0644
remote-agent edit /tmp/greeting --old hi --new hello
remote-agent glob '**/*.go' /srv/app
remote-agent grep 'func main' /srv/app --include '**/*.go'
remote-agent ps --filter nginx
remote-agent sysinfo
remote-agent upload ./local.txt /tmp/remote.txt
remote-agent download /tmp/remote.txt ./local.txt
remote-agent readlink /usr/bin/python

# Mount the remote filesystem locally; any program can then use these paths
remote-agent mount /mnt/remote /srv/app
grep -r TODO /mnt/remote        # ordinary tools, remote files
remote-agent unmount /mnt/remote

# Disconnect — stops the daemon (the helper stays cached for fast reconnects)
remote-agent disconnect
```

Add `--json` to any command for machine-readable output instead of compact text,
and `--target user@host` to choose which host it runs against.

`exec` mirrors the remote command's **exit code** as its own, and forwards remote
stdout to stdout and stderr to stderr — so it behaves like a transparent local
shell command (e.g. `remote-agent exec false` exits 1).

### Run Claude Code against a remote host

`remote-agent claude` launches [Claude Code](https://claude.com/claude-code) with
**both its shell and its files on the remote host** instead of the local machine.
It starts (or reuses) a daemon for the target, points Claude's
`CLAUDE_CODE_SHELL_PREFIX` at a shim that forwards each Bash command through the
daemon, and mounts the remote working directory at the same local path. The model
just reads, edits and runs things — it never has to think about SSH.

```sh
remote-agent claude user@host                 # start daemon, launch claude wired to it
remote-agent claude user@host:2222            # non-standard SSH port (--port 2222 does the same)
remote-agent claude user@host -- --model opus # args after -- are passed to claude
remote-agent claude                           # reuse the single running daemon
```

A daemon started by this command is stopped again when claude exits; pass
`--keep-daemon` to leave it running, or `--claude-bin` to point at a specific
`claude` executable.

Claude's file tools (Read, Write, Edit, Glob, Grep) call the local filesystem
directly and cannot be redirected — so instead of replacing them, the filesystem
itself moves: the mount makes those tools, third-party MCP servers, and any other
program operate on remote files unchanged. Claude runs with the mount as its
working directory.

```sh
remote-agent claude user@host --dir /srv/app        # mount /srv/app, work there
remote-agent claude user@host --mount-at /mnt/app   # different local mount point
remote-agent claude user@host --no-mount            # no FUSE: use MCP tools instead
```

The mount point defaults to the *same absolute path* as the remote directory, so
a path means the same thing to a file tool (local, through the mount) and to a
shell command (remote, over SSH). Where FUSE is unavailable, `--no-mount` falls
back to serving the remote host as MCP tools (`run_command`, `read_file`,
`write_file`, `edit_file`, `list_dir`, `glob`, `grep`, `upload_file`,
`download_file`) and disabling the built-in file tools — that mode only covers
the tools remote-agent provides.

> Note: each Bash call runs as a one-shot command on the remote. With a mount at
> the matching path, commands start in Claude's working directory, so relative
> paths work; shell state (a `cd` inside one command, exported variables) still
> does not carry into the next call.

Only Bash tool commands go to the remote. Claude Code v2.1.185+ also passes
**hook commands and MCP stdio servers** through `CLAUDE_CODE_SHELL_PREFIX`;
the shim recognizes Claude's machine-generated Bash tool wrapper and forwards
only that (with its local-only scaffolding — the shell-snapshot `source` and
the cwd-tracking tail — stripped), while hooks and MCP servers run on the
local machine, where the scripts and environment variables Claude prepared
for them actually exist.

### Serve a remote host to any MCP client

`remote-agent mcp` exposes the remote host's shell and files (`run_command`,
`read_file`, `write_file`, `edit_file`, `list_dir`, `glob`, `grep`,
`upload_file`, `download_file`) over the MCP stdio transport, to Claude Code or
any other client.

```json
{ "mcpServers": { "remote": { "command": "remote-agent", "args": ["mcp"] } } }
```

Every tool takes the `target` it acts on (`user@host`, `user@host:2222` for a
non-standard SSH port, or a `Host` alias from
`~/.ssh/config`), and the SSH connection is opened on demand — nothing has to be
started first, and one server can act on several hosts at once. Naming a target
(`args: ["mcp", "user@host"]`, `--target`, or `REMOTE_AGENT_TARGET`) makes it the
default for calls that omit one; without a default, every call must carry its own.

**Given a control socket for a host, pass it as `control_path`** — every tool
takes it, alongside `target`:

```json
{"name": "run_command",
 "arguments": {"target": "root@10.0.0.7",
               "control_path": "/tmp/cm-root@10.0.0.7:22",
               "command": "systemctl status nginx"}}
```

The call borrows the connection that master already authenticated, which is
what makes a host behind a one-time password or a hardware key reachable at
all. Naming one makes it mandatory — the call fails rather than opening its
own connection. Omit it when `~/.ssh/config` already sets a `ControlPath` for
the host (used automatically) or when the host needs no master.

### Commands

| Command | Description |
|---------|-------------|
| `connect <user@host[:port]>` | Start the daemon and SSH session up front. Optional: any command starts one on demand. The port can also be `--port` (default: the ssh_config port, else 22); `--control-path` names an OpenSSH control master to run through. |
| `claude [user@host[:port]]` | Launch Claude Code with its shell and its files on the remote. `--dir`, `--mount-at`, `--no-mount`, `--port`, `--keep-daemon`, `--claude-bin`; args after `--` pass through to claude. |
| `mount <mountpoint> [remote-path]` | Mount the remote filesystem locally. `--allow-other` shares it with other local users. |
| `unmount <mountpoint>` | Detach a mount. |
| `mounts` | List live mounts. |
| `disconnect` | Stop the daemon. The helper binary stays cached in `~/.cache/remote-agent` on the remote, so the next connect skips the upload. |
| `ping` | Check that the daemon and remote are alive. |
| `exec <command...>` | Run a shell command on the remote. |
| `ls [path]` | List a remote directory. `--recursive` walks subdirectories. |
| `read <path>` | Print a remote file (binary-safe). |
| `write <path>` | Write stdin to a remote file (binary-safe, any size). `--mode` sets permissions (octal, default 0644). |
| `edit <path>` | Find/replace in a remote file. `--old` (required) and `--new`; the text must be unique unless `--replace-all`. |
| `glob <pattern> [path]` | List remote files matching a glob (`**`, braces), newest first. `--limit` caps results. |
| `grep <pattern> [path]` | Search remote file contents by regex. `--include`, `--mode`, `-i`, `-C`, `--limit`. |
| `mcp [user@host[:port]]` | Serve remote shells and filesystems to an MCP client over stdio (used by `claude`, usable by any MCP client). Every tool takes the target it acts on; a target given here is the default. |
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
- A **mount** (`remote-agent mount`, and `remote-agent claude` by default) adds a
  long-lived SSH channel running the helper's `serve fs` mode. Filesystem
  operations travel over it as length-prefixed frames — file data as raw bytes
  beside the JSON header — and a local FUSE filesystem turns them into ordinary
  paths. Attributes and directory entries are cached for a second; negative
  lookups are not cached at all, so files a remote command just created appear
  immediately. Requires FUSE (Linux, or macOS with macFUSE).
- A copy of the binary is **deployed to the remote** and invoked as a hidden
  `serve` subcommand for operations that need structured output there (`sysinfo`,
  `ps`, `edit`, `glob`, `grep`), with start/stop/action **audit logging** to the
  remote's syslog. Searching remote-side means one round trip per query and no
  dependency on the remote's `find`/`grep` dialect.
  The helper is content-addressed and cached in `~/.cache/remote-agent`, so
  reconnects skip the multi-megabyte upload when the binary is unchanged.

Authentication uses your SSH agent and `~/.ssh` keys. Host keys follow OpenSSH
`accept-new` semantics: the first connection to an unknown host is trusted and its
key recorded in `~/.ssh/known_hosts`, and every later connection must match the
recorded key. The fingerprint is printed on connect — verify it on first contact.

**Control sockets.** If `~/.ssh/config` sets a `ControlPath` for the host and a
master is listening on it, commands ride that connection instead — no second
authentication, so hosts behind a one-time password or a hardware key work by
opening the master yourself first (`ssh host true`). A stale socket falls back
to dialing, and says so. `--control-path <socket>` (or
`REMOTE_AGENT_CONTROL_PATH`) makes a specific master mandatory: the connect
fails rather than quietly opening its own. See
[docs/ssh/control-sockets.md](docs/ssh/control-sockets.md).

There is **one daemon per target** (the socket path is derived from the
target), so multiple terminals targeting the same host share a connection. The
port is part of the target, so `root@127.0.0.1:2201` and `root@127.0.0.1:2202`
are two separate daemons and two separate connections. When
more than one daemon is running, pass `--target user@host[:port]` (or set
`REMOTE_AGENT_TARGET` / `REMOTE_AGENT_SOCKET`) to pick which one a command talks
to; `remote-agent claude` exports this automatically so its forwarded commands
always reach the right daemon.

Any command starts a daemon when none is running, and restarts one that died or
idled out — `connect` is an optimization, not a prerequisite. The target comes
from `--target`, then `REMOTE_AGENT_TARGET`, then the last target a daemon ran
for (remembered in a small file beside the socket). Set
`REMOTE_AGENT_NO_AUTOSTART=1` to require an explicit `connect` instead;
`disconnect` and `ping` never start one.

A remote command that never ends would otherwise hold its caller forever, so
`exec` gives up after ten minutes and says so. Set `REMOTE_AGENT_TIMEOUT` (in
seconds) for a longer or shorter one; through MCP it is the `timeout` argument
on `run_command`. See [docs/daemon/timeouts.md](docs/daemon/timeouts.md).

## Development

```sh
go-toolchain          # tidy + vet + test (with coverage) + build
gofmt -l .            # must print nothing
```

See [`CLAUDE.md`](./CLAUDE.md) for the architecture map and conventions. CI runs the
same `go-toolchain` on every push.
