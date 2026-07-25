# CLAUDE.md

Guidance for Claude Code (and other agents) working in this repository.

## What this is

`remote-agent` is a single Go binary that exposes a small, structured toolset
(`exec`, `read`, `write`, `edit`, `ls`, `glob`, `grep`, `ps`, `sysinfo`,
`upload`, `download`, `readlink`) against a **remote host over SSH**. It is built
around a long-lived local **daemon** that holds one SSH connection, plus a copy of
the same binary **deployed to the remote** to service operations that need
structured output there.

It also ships a `claude` launcher (`remote-agent claude [user@host]`) that runs
Claude Code with `CLAUDE_CODE_SHELL_PREFIX` pointed at a one-line shim
(`client/shellprefix.sh`, embedded via `//go:embed`). The shim execs the hidden
`claude-shim` subcommand (`client/claudeshim.go`), which routes each prefix
invocation: Bash tool commands are laundered and forwarded to the remote
through the daemon, while hook commands and MCP stdio servers run **locally**
(see Gotchas). The shim must be a **single-token** program path — Claude's
prefix wrapper shell-quotes the program name, so a multi-word prefix like
`remote-agent claude-shim` would be treated as one executable name and fail.

The launcher also swaps Claude's **file tools** onto the remote host: it
registers `remote-agent mcp` (`mcpserver/`) as an MCP stdio server and disables
the built-in local-filesystem tools (see Gotchas). `--local-tools` opts out.

## Build, test, lint

This is a `wow-look-at-my` Go project: **always use `go-toolchain`** (no arguments)
from the repo root. It runs `go mod tidy`, `go vet`, tests with coverage, and the
build in one step, and downloads the pinned Go version itself.

```
go-toolchain                 # tidy + vet + test (coverage) + build -> build/remote-agent
go-toolchain --no-benchmark  # skip the benchmark phase (faster inner loop)
```

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
public/private visibility to the repo's visibility on OIDC publishes. Do not
add a `publish` job to build.yml — it would duplicate autorelease.

A `Makefile` exists for plain-`go` users (`make build`, `make build-linux`,
`make build-all`), but `go-toolchain` is the source of truth.

## Architecture

Three roles, one binary:

```
 CLI client             local daemon                      remote agent
 (cmd/, client/)        (daemon/)                         (agent/)
 ───────────────  unix socket  ───────────────────  SSH  ────────────────────
 remote-agent ls ─JSON req──▶  handler → ops          ─cmd─▶ ~/.cache/remote-agent/agent-xxx
                ◀─JSON resp──   (one SSH connection)  ◀────  (hidden serve subcommands)
```

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

`connect` dials SSH, prints the host-key fingerprint, deploys the helper to a
content-addressed path (`~/.cache/remote-agent/agent-<sha256[:8]>`) — reusing an
identical cached copy when present, so reconnects skip the multi-MB upload — then
writes a startup audit entry and listens. When `$HOME` is unusable it falls back
to a random `/tmp/.remote-agent-<rand>` path (removed again on disconnect). Each
command: the client locates the socket → the daemon runs either a plain shell
command or `<remote-binary> serve <sub>` on its own SSH channel (the audit entry
for exec/write/upload runs concurrently on another channel) → returns a
`protocol.DaemonResponse`. The idle watchdog never fires while operations are in
flight. `disconnect` drains pending audits, writes a shutdown audit entry, removes
the socket/PID files, and exits; cached helpers stay in place for the next connect.

### Packages

| Path | Responsibility |
|------|----------------|
| `cmd/` | Cobra commands, one per file, each self-registering in its own `init()`. `serve*.go` are the hidden remote-helper entry points. |
| `client/` | Unix-socket client (`client.go`; `Call` returns typed results, the printers in `print.go` render text). `launch.go` orchestrates the `claude` launcher (start/reuse daemon, write shim + MCP config, run claude); `shellprefix.sh` is the embedded forwarding shim; `claudeshim.go` classifies and launders what the shim receives (remote Bash tool commands vs local hooks/MCP). |
| `daemon/` | Long-lived daemon, action router (`handler.go`), operation handlers (`ops.go`, plus `search.go` for glob/grep). |
| `mcpserver/` | MCP stdio server (JSON-RPC 2.0 over stdin/stdout) exposing the remote filesystem as tools. `server.go` is the protocol layer, `tools.go` the tool declarations and their daemon calls. Depends only on a `Backend` interface, satisfied by `client.DaemonBackend`. |
| `agent/` | Remote-side system/process/file collectors and the search implementations (`glob.go`, `grep.go`); platform files selected by build tag. |
| `sshutil/` | SSH connect, auth (agent + `~/.ssh` keys), host-key callback, keepalive, command execution. `CommandRunner` runs commands concurrently (bounded) with pre-opened spare sessions. |
| `protocol/` | Shared request/response/result structs (JSON-tagged). No logic. |

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

## Gotchas

- The daemon is **stateful and long-lived**: `connect` must run before any other
  command, and there is **one daemon per target** (the socket path is
  `sha256(target)`, so two terminals targeting the same host share it).
- Tests use **mock daemons and an in-process SSH test server** — there is no real
  network or SSH in the suite. The `Runner` interface (`daemon/ops.go`) is the seam
  for mocking SSH execution, and `exitFunc` (also `ops.go`) is the seam for
  `os.Exit` during `disconnect`.
- `exec ls [path]` is rewritten to the structured `ls` handler; `ls` with other
  flags falls through to raw `exec` (`parseLsCommand` in `daemon/ops.go`). A
  leading `--` argument to `exec` is dropped rather than joined into the remote
  command string (`sh -c '-- cmd'` errors on every shell).
- **Claude Code v2.1.185+ wraps hooks and MCP stdio servers with
  CLAUDE_CODE_SHELL_PREFIX too** — not just Bash tool commands. Through a
  forward-everything shim, hooks broke (claude injects CLAUDE_PROJECT_DIR /
  CLAUDE_ENV_FILE into the LOCAL spawn env only, and the hook script exists
  only locally) and MCP stdio servers could never handshake (the remote exec
  path is buffered request/response with no stdin bridge). `claude-shim`
  therefore runs hooks and MCP servers **locally by design** (`/bin/sh -c`
  with inherited env and stdio; exec(2) on Unix), restoring pre-2.1.185
  semantics. Classification is by the machine-generated glob-setup clause
  (`{ shopt -u extglob || setopt NO_EXTENDED_GLOB NO_BARE_GLOB_QUAL; }
  >/dev/null 2>&1 || true`) that v2.1.185 embeds in every Bash tool wrapper
  iff a prefix is set — user hook/MCP command lines cannot plausibly contain
  it. Misclassification risk is asymmetric: forwarding a hook merely breaks
  the hook, but running a Bash tool command locally would silently execute it
  on the wrong machine.
- **Bash tool wrappers are laundered before forwarding**
  (`launderBashToolWrapper` in `client/claudeshim.go`). Stripped: (a) the
  leading `source <local shell-snapshot> 2>/dev/null || true &&` — the
  snapshot file exists only locally, sourcing a local bash state dump on the
  remote is wrong on every shell, and on BusyBox ash `source` is the POSIX
  special builtin `.` whose failed open is a FATAL, silenced abort that made
  every command return "(No output)" (exit 2, zero bytes); (b) the trailing
  `&& pwd -P >| <local tmp path>` — it littered the remote /tmp while the
  local cwd file Claude reads went unwritten ("Shell cwd was reset" after
  every command). On success, claude-shim writes the local cwd file itself
  with its own working directory. Everything between the two clauses (the
  inlined session-env preamble, the glob marker, the eval) is forwarded
  verbatim. Paths in both clauses may be bare OR single-quoted (Claude quotes
  only outside `[A-Za-z0-9_./:=@+,-]`); non-matching clauses are left
  untouched.
- **Claude's file tools cannot be redirected — they are replaced.** Read,
  Write, Edit, Glob and Grep call Node's `fs` against the machine claude runs
  on (cli.js: the Read tool reads through the process-local fs accessor); there
  is no file-tool equivalent of CLAUDE_CODE_SHELL_PREFIX, no env var, and no
  hook that can rewrite where they land. `remote-agent claude` therefore passes
  `--disallowedTools=Read,Write,Edit,NotebookEdit,Glob,Grep` (claude filters
  denied tools out of the advertised tool set, so the model never sees them)
  and registers `remote-agent mcp` via `--mcp-config`, whose tools carry the
  `mcp__remote__` prefix. Only tools that exist are listed: an unknown name
  produces a "matches no known tool" warning at startup.
- **The launcher's claude flags must use the `--flag=value` form.**
  `--mcp-config`, `--disallowedTools` and `--allowedTools` are declared
  variadic (`<configs...>`, `<tools...>`), so in the space-separated form
  commander keeps consuming following arguments — it would swallow the user's
  own prompt and flags. The `=` form binds exactly one value and stops
  (commander's `/^--[^=]+=/` branch does not set the variadic accumulator).
  Repeated occurrences concatenate, so the user's own copies of these flags
  still apply.
- **Read-only remote tools are pre-allowed, mutating ones are not**
  (`preAllowedRemoteTools` in `client/launch.go`). MCP tools always prompt on
  first use, while the built-in Read/Glob/Grep never did; pre-allowing the
  read-only four restores the permission behaviour of the tools being replaced,
  and leaving write_file/edit_file/upload_file/download_file out keeps writes
  gated exactly as Write/Edit were.
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
- `cd` does not persist between Bash tool calls through the launcher: each
  forwarded command runs as a one-shot exec on the remote (fresh login shell
  in the remote home). Documented limitation — combine steps in a single
  command (`cd /x && make`).
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
- Socket selection: `findSocket` honors `client.SocketOverride`, then
  `REMOTE_AGENT_SOCKET`, then `REMOTE_AGENT_TARGET` (hashed via `daemon.SocketPath`),
  before falling back to globbing a single socket in `TempDir`. The `claude`
  launcher exports `REMOTE_AGENT_SOCKET` so forwarded commands hit the right daemon
  even when several are running.

## Adding a command

1. New `cmd/<name>.go` with a `cobra.Command` registered in its own `init()`.
2. A thin `client.<Name>(...)` in `client/client.go` that sends a
   `protocol.DaemonRequest{Action: "<name>", ...}` and prints the result.
3. A `handle<Name>` handler in `daemon/ops.go`, wired into the switch in
   `daemon/handler.go`.
4. Any new result type in `protocol/types.go`. Then run `go-toolchain`.
5. If the model should be able to use it through `remote-agent claude`, add a
   tool for it in `mcpserver/tools.go` (declaration + handler calling
   `s.backend.Call`).
