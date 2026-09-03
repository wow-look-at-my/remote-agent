# CLAUDE.md

Guidance for Claude Code (and other agents) working in this repository.

## What this is

`remote-agent` is a single Go binary that exposes a small, structured toolset
(`exec`, `read`, `write`, `edit`, `ls`, `glob`, `grep`, `ps`, `sysinfo`,
`upload`, `download`, `readlink`) against a **remote host over SSH**. It is built
around a long-lived local **daemon** that holds one SSH connection, plus a copy of
the same binary **deployed to the remote** to service operations that need
structured output there.

A model reaches all of it through `remote-agent mcp` (`mcpserver/`), an MCP
stdio server whose every tool names the host it acts on.

It can also **mount the remote filesystem locally** (`remotefs/`, `fswire/`,
`agent/fsserver_unix.go`), so that ordinary programs -- an editor, a build, a
tool written after this one -- reach remote files by path, through the kernel
rather than a tool-specific bridge. That half needs FUSE and a native build; a
published APE gets the stub (see docs/ape.md).

## Build, test, lint

This is a `wow-look-at-my` Go project: **always use `go-toolchain`** (no arguments)
from the repo root. It runs `go mod tidy`, `go vet`, tests with coverage, and the
build in one step, and downloads the pinned Go version itself.

```
go-toolchain                 # tidy + vet + test (coverage) + build + dats -> build/remote-agent
go-toolchain --no-benchmark  # skip the benchmark phase (faster inner loop)
```

`dats/cli.dats` is the CLI-level suite, run by the same command after the build
(it needs `bubblewrap`, or docker, for its sandbox). It asserts what the binary
does with a target -- which daemon socket a target picks, which address
`connect` names, what the MCP tools advertise -- so behaviour that only shows up
through the CLI has a test that runs on every build. A check of that kind goes
there, never into a throwaway script.

The one dependency that needs care is `github.com/hanwen/go-fuse/v2`: the org
module proxy does not serve its checksum-database entry, so `go mod tidy`
through go-toolchain fails on a *new* version until go.sum has the hashes.
Populate them once from the public proxy
(`GOPROXY='https://proxy.golang.org,direct' GOSUMDB=sum.golang.org go mod tidy`)
and commit go.sum; go-toolchain then never needs the sumdb.

Do **not** run bare `go build` / `go test` / `go mod tidy`. Keep the tree
gofmt-clean — `gofmt -l .` must print nothing. CI (`.github/workflows/build.yml`)
runs the same toolchain via `wow-look-at-my/go-toolchain@v1`, and nothing merges
without it passing.

CI also **publishes on every push**: `go-toolchain@v1`'s `autorelease` input
(default `'true'`) runs the stock buildhost-publish action after the build,
uploading the six-binary matrix (linux/darwin/windows x amd64/arm64) to the
buildhost project `remote-agent` with the git branch attached. Branch pushes
create branch-scoped releases only; a **master** publish is what advances the
bare "latest" download URL and the Homebrew formula
(`brew install pazer/build/remote-agent`). buildhost also syncs the project's
public/private visibility to the repo's visibility on OIDC publishes. The
workflow's `permissions` block must carry `deployments: write` and
`artifact-metadata: write` alongside `id-token: write` -- autorelease registers
every publish as a GitHub Deployment and posts an artifact storage record, with
no opt-out for either, and fails the build without both. Do not
add a `publish` job to build.yml — it would duplicate autorelease.

A `Makefile` exists for plain-`go` users (`make build`, `make build-linux`,
`make build-all`), but `go-toolchain` is the source of truth.

## Docs

Depth the code comments point at, rather than carry:

- `docs/ape.md` -- why a shell starts this binary, which sites do it, why the remote does not need it.
- `docs/daemon/timeouts.md` -- the deadline on exec, what aborting does, where a caller sets one.
- `docs/daemon/lifecycle.md` -- target identity and port, ssh_config, idle accounting, shutdown order.
- `docs/mount/behaviour.md` -- cache windows, mount options, force unmount, open flags.
- `docs/ssh/connection.md` -- ssh_config resolution, keepalive, the session pool, streams.
- `docs/ssh/control-sockets.md` -- the control-master protocol and how to test it live.

## Architecture

Three roles, one binary:

```
 CLI client             local daemon                      remote agent
 (cmd/, client/)        (daemon/)                         (agent/)
 ───────────────  unix socket  ───────────────────  SSH  ────────────────────
 remote-agent ls ─JSON req──▶  handler → ops          ─cmd─▶ ~/.cache/remote-agent/agent-xxx
                ◀─JSON resp──   (one SSH connection)  ◀────  (hidden serve subcommands)
```

A fourth path exists alongside these three: a **filesystem mount**. The daemon
opens one long-lived SSH channel running `<remote-binary> serve fs`, speaks a
framed protocol over it (`fswire/`), and exposes the result as a local FUSE
mount (`remotefs/`). Ordinary programs then reach remote files by path.

1. **CLI client** — `cmd/` (cobra, one command per file) builds a
   `protocol.DaemonRequest`; `client/client.go` sends it as JSON over a per-target
   Unix socket and prints the reply (compact text, or indented JSON with `--json`).
2. **Local daemon** — `daemon/daemon.go`, started by `remote-agent connect`. Opens
   one SSH connection (`sshutil/`), deploys the binary to the remote, listens on
   `/tmp/remote-agent-<sha>.sock`, and handles requests concurrently — each command
   runs on its own multiplexed SSH channel via `sshutil.CommandRunner`, which
   bounds in-flight commands and keeps spare sessions pre-opened.
   `daemon/handler.go` routes an action to a handler in `daemon/ops.go`.
3. **Remote agent** — the same binary, copied to the remote and run as the hidden
   `serve` subcommand (`cmd/serve*.go` → `agent/`) for operations that need
   structured output on the remote: `sysinfo`, `ps`, `edit`, and `audit` logging.
   Platform specifics live in `*_linux.go` / `*_darwin.go` / `*_windows.go`.

### Request lifecycle

`connect` -- run explicitly, or started in the background by the first command
that finds no daemon -- dials SSH, prints the host-key fingerprint, deploys the
helper to a content-addressed path (`~/.cache/remote-agent/agent-<sha256[:8]>`) — reusing an
identical cached copy when present, so reconnects skip the multi-MB upload — then
writes a startup audit entry and listens. It also records the target beside the
socket so a later command can restart it unaided. When `$HOME` is unusable it
falls back to a random `/tmp/.remote-agent-<rand>` path (removed again on disconnect). Each
command: the client locates the socket → the daemon runs either a plain shell
command or `<remote-binary> serve <sub>` on its own SSH channel (the audit entry
for exec/write/upload runs concurrently on another channel) → returns a
`protocol.DaemonResponse`. The idle watchdog never fires while operations are in
flight. `disconnect` drains pending audits, writes a shutdown audit entry, removes
the socket/PID files (before closing the listener, whose close races the process
exit) and exits; the target record and cached helpers stay in place for the next
connect.

### Packages

| Path | Responsibility |
|------|----------------|
| `cmd/` | Cobra commands, one per file, each self-registering in its own `init()`. `serve*.go` are the hidden remote-helper entry points. |
| `client/` | Unix-socket client (`client.go`; `autostart.go` resolves the target and starts a daemon when none answers; `Call` returns typed results, the printers in `print.go` render text). `daemonproc.go` starts a daemon and waits for it; `selfexec.go` renders the argv that starts this binary again. |
| `daemon/` | Long-lived daemon, action router (`handler.go`), operation handlers (`ops.go`, plus `search.go` for glob/grep). |
| `mcpserver/` | MCP stdio server (JSON-RPC 2.0 over stdin/stdout) exposing remote shells and filesystems as tools. `server.go` is the protocol layer, `tools.go` the declarations and schemas, `handlers.go` the daemon calls behind them. Depends only on a `Backend` interface, satisfied by `client.DaemonBackend`. |
| `agent/` | Remote-side system/process/file collectors, the search implementations (`glob.go`, `grep.go`), and the mount helper (`fsserver_unix.go`, with `fsstat_*.go` for per-platform stat/statfs); platform files selected by build tag. |
| `fswire/` | The mount's wire protocol: request/response types, length-prefixed framing with binary payloads, and portable open-flag translation. Standard library only, so both ends share it. |
| `remotefs/` | The local half of a mount: a request multiplexer (`client.go`, portable) and the go-fuse filesystem (`fuse_unix.go`; `fuse_other.go` is a build stub for platforms without FUSE). |
| `sshutil/` | SSH connect, auth (agent + `~/.ssh` keys), host-key callback, keepalive, command execution. `CommandRunner` runs commands concurrently (bounded) with pre-opened spare sessions. `mux_unix.go` is the other transport: a client for OpenSSH's control-master protocol (`mux_other.go` refuses on non-unix). Both satisfy `Conn`. |
| `protocol/` | Shared request/response/result structs (JSON-tagged). No logic. |
| `dats/` | CLI-level test suite (`cli.dats`), run by `go-toolchain` after the build against the staged binary. |

## Conventions

- **CLI**: cobra; one top-level command per file in `cmd/`; each command registers
  itself in its own `init()`. Never centralize registration in `main` or `root`.
- **Platform code**: split by build-tag filename suffix (`_linux`, `_darwin`,
  `_windows`), not `runtime.GOOS` branches inside a shared file.
- **Modern Go (1.24)**: prefer `any` over `interface{}`, `strings.Cut` for two-way
  splits, `math/rand/v2`, and `os.Getpagesize()` over a hardcoded page size.
- **Remote file I/O** streams raw bytes over the SSH channel (stdin for writes,
  stdout for reads — the channel is binary-safe). Only the local JSON socket hop
  base64-frames non-UTF-8 payloads (`content_b64` in `read`/`write`), because
  JSON strings cannot carry invalid UTF-8. Shell arguments are quoted with
  `shellEscape` (`daemon/daemon.go`).
- **Errors** wrap with `%w`; handlers return `errResponse(err)` / `okResponse(data)`
  (`daemon/handler.go`).
- **Every capability has to be reachable per call, from the MCP tools.** A
  model is handed a host, a control socket, a directory *mid-session* and
  cannot restart its own MCP server, so a capability that exists only as a
  flag or environment variable the server was started with does not exist for
  it. Anything the daemon can do gets a tool argument (routing on every tool
  via `s.tool`, or an argument on the tool it belongs to) — not a setup step
  in a README. This shipped as three separate fixes (`target`, `run_command`,
  `control_path`) that were all this same defect; check it before adding a
  capability, not after someone reports being stuck.

## Gotchas

- The daemon is **stateful and long-lived**, and there is **one daemon per
  target** (the socket path is `sha256(target)`, so two terminals targeting the
  same host share it). `connect` is optional: any command starts a daemon when
  none answers (`client/autostart.go`), which is why the target has to be
  discoverable -- see the next entry.
- **The port is part of the target, and the target is the identity**
  (`daemon/target.go`). `[user@]host[:port]` parses everywhere a target is
  accepted -- CLI, `--target`, `REMOTE_AGENT_TARGET`, every MCP tool's `target`
  argument -- and `--port` / `REMOTE_AGENT_PORT` fold into it
  (`client.TargetKey`, `daemon.CanonicalTarget`) before anything hashes it.
  Without that, several endpoints behind one `root@127.0.0.1` on different
  ports shared one socket, one target record and one SSH connection, so every
  call after the first landed on whichever port connected first. Two ports for
  one target that disagree are an error, never a silent choice. A port
  `ssh_config` resolves stays out of the identity: the client cannot see it
  without running `ssh -G`, so folding it in would key the daemon to a socket
  the caller never waits on.
- **The daemon starts itself, so the target must survive it.** Socket paths are
  a one-way hash of the target, so `daemon.Start` writes a target record next to
  the socket (`daemon/record.go`) and, unlike the socket and PID files, that
  record is deliberately **kept on shutdown** -- it is the only way a later
  command can restart a daemon that idled out. Resolution order is `--target`,
  `REMOTE_AGENT_TARGET`, then a single remembered record (several are ambiguous
  and error). `REMOTE_AGENT_NO_AUTOSTART=1` opts out; `disconnect` and `ping`
  never auto-start, since both exist to ask whether a daemon is there.
- **Auto-start waits on the daemon process, not just the clock**
  (`awaitDaemon` in `client/daemonproc.go`). A bad host or rejected key makes
  `connect` exit in under a second; polling only the socket would hide that
  behind the full 30s readiness timeout, so the process is watched too and its
  log tail is quoted in the error.
- Tests use **mock daemons and an in-process SSH test server** — there is no real
  network or SSH in the suite. The `Runner` interface (`daemon/ops.go`) is the seam
  for mocking SSH execution, and `exitFunc` (also `ops.go`) is the seam for
  `os.Exit` during `disconnect`.
- `exec ls [path]` is rewritten to the structured `ls` handler; `ls` with other
  flags falls through to raw `exec` (`parseLsCommand` in `daemon/ops.go`). A
  leading `--` argument to `exec` is dropped rather than joined into the remote
  command string (`sh -c '-- cmd'` errors on every shell).
- **A shell starts this binary, never an execve** (`client.SelfCommand`). A
  release is a Cosmopolitan APE, whose header is a shell script rather than an
  ELF header: a shell runs it anywhere (execve answers ENOEXEC and POSIX makes
  the shell read the file as the script it is), while `os/exec` and an MCP
  client's own spawn report "exec format error" on a host with no APE binfmt
  entry -- which a developer machine quietly acquires, because Cosmopolitan
  registers one when it runs as root. So daemon auto-start and the MCP server
  command in the launcher's config both name `/bin/sh`, with the binary as its
  argument. The remote needs nothing: sshd hands the helper's command line to
  the login shell already. The same build carries no mount -- go-fuse needs
  Linux-only syscall constants a portable libc cannot define, so `remotefs`'s
  FUSE half is `!cosmo` and an APE session runs as `--no-mount` does. The
  remote half is unaffected: `serve fs` is plain file I/O. see docs/ape.md
- **A remote command carries a deadline, because one that never ends wedges an
  MCP session** -- the server answers in arrival order, so every later call
  queues behind it, and a client cannot cancel. `exec` is the only action whose
  text a caller wrote, so it is the only one bounded; the deadline aborts the
  SSH session (`sshutil.bounded`) rather than leaking the slot, and says the
  remote process may outlive the call. `timeout` on `run_command`,
  `REMOTE_AGENT_TIMEOUT` for the CLI. see docs/daemon/timeouts.md
- **A mount and a shell disagree about paths unless the mount point is the
  remote path.** A program reads a mounted file by its LOCAL path while a
  command runs against the REMOTE one, so a mount at a different local path
  gives one file two names, and whoever is driving both mixes them. Mount at
  the same absolute path when anything will do both.
- **A mount whose session has died is contagious.** Any process that so much
  as stats it blocks in the kernel forever — `df`, shell tab-completion, an
  unrelated test's statfs (this bit during development: a leaked mount from a
  failed test hung the whole suite). Hence `ForceUnmount` (lazy detach via
  `fusermount -uz`, falling back to `MNT_DETACH`) on every shutdown path, and
  a bounded handshake (`remotefs.PingTimeout`) before a mount is ever handed
  to the kernel.
- **`O_APPEND` is stripped from the remote open** (`agent/fsserver_unix.go`).
  Every read and write carries an explicit offset — the kernel resolves append
  offsets before it sends the request — and pwrite on an O_APPEND descriptor
  ignores that offset; Go rejects the combination outright, which surfaced as
  EIO on every append through the mount.
- **Open flags are translated, not passed through** (`fswire/openflags.go`).
  O_APPEND is 0x8 on darwin and 0x400 on Linux, and O_TRUNC/O_EXCL differ too,
  so raw flags on a cross-platform mount would silently truncate files.
- **Mount caching is deliberately short**: 1s for attributes and entries, and
  negative lookups are not cached at all, because a file a forwarded build
  command just created has to be visible to the next read.
- **`run_command` is the MCP server's shell** (`mcpserver/handlers.go`). It
  reaches the daemon's `exec`, so it inherits the `ls` rewrite: a plain
  `ls <path>` comes back as a `DirListing`, not an `ExecResult`, and `execReply`
  decodes both -- reading only the command shape turned `ls /srv` into an empty
  success. A non-zero exit is returned as a tool error carrying the output, each
  stream is capped at 64 KiB with the drop stated, and `cwd` is prefixed as a
  shell-quoted `cd` because each call is a fresh one-shot shell.
- **A control socket is named per call too**: `control_path` on every MCP tool,
  `--control-path` (global flag) and `REMOTE_AGENT_CONTROL_PATH` for the CLI
  and the launcher, resolved by `client.ControlPathFor` in that order. It
  rides `protocol.Route` with the target into `client.CallRoute`, which starts
  that target's daemon through that master. Naming one makes it **mandatory**
  (the daemon fails rather than dialing its own connection), the daemon's
  target record remembers it so a restart reuses it, and a call naming a
  *different* socket than the running daemon uses is refused rather than
  silently answered over the wrong connection (`checkControlPath`).
  docs/ssh/control-sockets.md has the worked examples.
- **Every MCP tool call carries its own target** (`target` argument; `s.tool`
  in `mcpserver/tools.go` adds it, `client.CallTarget` routes it). Selecting
  the daemon from process state instead -- a pinned socket, `--target`, a lone
  socket in TempDir -- made a client wired up without those fail every call
  with "no daemon running", curable only by setting a daemon up by hand. A
  named target goes straight to `daemon.SocketPath(target)`, starting a daemon
  there if none answers, so one server serves several hosts and a daemon that
  idles out mid-session comes back on the next call. `remote-agent mcp
  [user@host]` sets a default; without one the argument is required, because
  the alternative is routing a call to a host nobody named.
- **The MCP server answers requests strictly in arrival order**
  (`mcpserver/server.go`). Concurrency would speed up batched reads but
  reorders dependent writes — a client sending write_file then edit_file for
  one path without waiting would edit the pre-write contents (observed before
  it was serialized). Claude Code never pipelines MCP calls anyway: MCP tools
  inherit `isConcurrencySafe = false`, so its executor runs them one at a time.
- **glob/grep run on the remote, not through `find`/`grep`** (`agent/glob.go`,
  `agent/grep.go`, invoked as `serve glob` / `serve grep`). Matching, ordering
  and limiting happen remote-side: one round trip per query, no dependency on
  the remote's find/grep dialect (BusyBox included), and consistent semantics
  (`**`, brace alternatives, RE2 regexes, binary-file and `.git`/`node_modules`
  skipping).
- **`edit` requires a unique match** unless `--replace-all` / `replace_all`.
  Silently replacing the first of several occurrences is unusable for a model
  that cannot see which one changed; the error names the occurrence count so
  the caller knows to add context.
- `cd` does not *persist* between calls: each command is a one-shot exec on the
  remote. `run_command` takes a `cwd` to start in, and a `cd` inside one command
  still does not carry into the next.
- **Commands ride an OpenSSH control master when one answers** on the
  ControlPath `ssh -G` reports for the host, instead of dialing and
  authenticating a second connection -- which is what makes a host behind a
  one-time password or a touched hardware key usable at all. `sshutil.Conn` is
  the seam (dialed client or control socket); `--control-path` /
  `REMOTE_AGENT_CONTROL_PATH` makes a specific master mandatory, and an
  auto-detected socket that does not answer prints why before falling back to
  dialing. remote-agent never *starts* a master: creating one can need a
  password, which a backgrounded daemon cannot ask for.
  See docs/ssh/control-sockets.md for the protocol and how to test it live.
- **`exec` parses the global flags itself** (`applyGlobalFlags` in
  `cmd/root.go`). It sets `DisableFlagParsing` so `exec ls -la` reaches the
  remote intact, and cobra then hands it the global flags unparsed: before this,
  `remote-agent --target host exec ls` ignored the target (running on whatever
  daemon socket discovery found -- silently the wrong host) and pasted
  "--target host" into the remote command string.
- Host-key verification has OpenSSH `accept-new` semantics (`sshutil/ssh.go`):
  an unknown host is trusted on first use and its key recorded in
  `~/.ssh/known_hosts`; a recorded host must present the same key or the
  connection fails. The fingerprint is printed on connect for verification.
- **The SSH keepalive must not want a reply.** `sshutil.keepAlive` pings with
  `wantReply=false` and watches `client.Wait()`. With x/crypto v0.52 a
  reply-wanting global request spins forever after a disconnect: SendRequest
  drains buffered responses with `select { case <-m.globalResponses: default: }`
  and that channel is *closed* on teardown, so the receive is always ready
  (ssh/mux.go:158). That hung `TestKeepAliveDisconnect` for its full timeout —
  invisible for a while because cached test results masked it.
- `client.Exec` returns `(exitCode, err)`: `err` is a transport/daemon failure,
  while a non-zero remote exit is reported via `exitCode`. `cmd/exec.go` mirrors
  that code as the process exit code (via the `osExit` test seam), so callers like
  Claude's Bash tool see real success/failure. `exec` no longer prints an
  `[exit N]` marker.
- Socket selection, for requests that name **no** target (`client.Call`; a
  request that names one bypasses all of this): `findSocket` honors
  `client.SocketOverride`, a socket this
  process auto-started, then `REMOTE_AGENT_SOCKET`, `--target`
  (`client.TargetOverride`) and `REMOTE_AGENT_TARGET` (hashed via
  `daemon.SocketPath`), before falling back to globbing a single socket in
  `TempDir`. It returns a path whether or not anything listens there --
  `sendRequest` starts a daemon when nothing answers. Export
  `REMOTE_AGENT_SOCKET` (or `REMOTE_AGENT_TARGET`) to pin a command to one
  daemon when several are running.

## Adding a command

1. New `cmd/<name>.go` with a `cobra.Command` registered in its own `init()`.
2. A thin `client.<Name>(...)` in `client/client.go` that sends a
   `protocol.DaemonRequest{Action: "<name>", ...}` and prints the result.
3. A `handle<Name>` handler in `daemon/ops.go`, wired into the switch in
   `daemon/handler.go`.
4. Any new result type in `protocol/types.go`. Then run `go-toolchain`.
5. Add a tool for it in `mcpserver/tools.go` (declaration) plus its handler in
   `mcpserver/handlers.go` (`s.route(args)`, then `s.backend.Call(route, ...)`)
   — that is how a model reaches it through `remote-agent mcp` in any client.
   Anything the command needs to be told is an argument on the tool, per the
   per-call rule in Conventions.
