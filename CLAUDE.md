# CLAUDE.md

Guidance for Claude Code (and other agents) working in this repository.

## What this is

`remote-agent` is a single Go binary that exposes a small, structured toolset
(`exec`, `read`, `write`, `edit`, `ls`, `ps`, `sysinfo`, `upload`, `download`,
`readlink`) against a **remote host over SSH**. It is built around a long-lived
local **daemon** that holds one SSH connection, plus a copy of the same binary
**deployed to the remote** to service operations that need structured output there.

It also ships a `claude` launcher (`remote-agent claude [user@host]`) that runs
Claude Code with `CLAUDE_CODE_SHELL_PREFIX` pointed at a one-line shim
(`client/shellprefix.sh`, embedded via `//go:embed`) so every Bash tool command
Claude issues is forwarded to the remote through the daemon. The shim must be a
**single-token** program path — Claude's prefix wrapper shell-quotes the program
name, so a multi-word prefix like `remote-agent exec` would be treated as one
executable name and fail.

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

A `Makefile` exists for plain-`go` users (`make build`, `make build-linux`,
`make build-all`), but `go-toolchain` is the source of truth.

## Architecture

Three roles, one binary:

```
 CLI client             local daemon                      remote agent
 (cmd/, client/)        (daemon/)                         (agent/)
 ───────────────  unix socket  ───────────────────  SSH  ────────────────────
 remote-agent ls ─JSON req──▶  handler → ops          ─cmd─▶ /tmp/.remote-agent-xxx
                ◀─JSON resp──   (one SSH connection)  ◀────  (hidden serve subcommands)
```

1. **CLI client** — `cmd/` (cobra, one command per file) builds a
   `protocol.DaemonRequest`; `client/client.go` sends it as JSON over a per-target
   Unix socket and prints the reply (compact text, or indented JSON with `--json`).
2. **Local daemon** — `daemon/daemon.go`, started by `remote-agent connect`. Opens
   one SSH connection (`sshutil/`), deploys the binary to the remote, listens on
   `/tmp/remote-agent-<sha>.sock`, and serializes all SSH use behind a mutex.
   `daemon/handler.go` routes an action to a handler in `daemon/ops.go`.
3. **Remote agent** — the same binary, copied to the remote and run as the hidden
   `serve` subcommand (`cmd/serve*.go` → `agent/`) for operations that need
   structured output on the remote: `sysinfo`, `ps`, `edit`, and `audit` logging.
   Platform specifics live in `*_linux.go` / `*_darwin.go` / `*_windows.go`.

### Request lifecycle

`connect` dials SSH, prints the host-key fingerprint, deploys
`/tmp/.remote-agent-<rand>`, writes a startup audit entry, then listens. Each
command: the client locates the socket → the daemon locks the mutex → runs either
a plain shell command or `<remote-binary> serve <sub>` → returns a
`protocol.DaemonResponse`. `disconnect` writes a shutdown audit entry, `rm`s the
remote binary, removes the socket/PID files, and exits.

### Packages

| Path | Responsibility |
|------|----------------|
| `cmd/` | Cobra commands, one per file, each self-registering in its own `init()`. `serve*.go` are the hidden remote-helper entry points. |
| `client/` | Unix-socket client and the human-readable printer for each action. `launch.go` orchestrates the `claude` launcher (start/reuse daemon, write shim, run claude); `shellprefix.sh` is the embedded forwarding shim. |
| `daemon/` | Long-lived daemon, action router (`handler.go`), operation handlers (`ops.go`). |
| `agent/` | Remote-side system/process/file collectors; platform files selected by build tag. |
| `sshutil/` | SSH connect, auth (agent + `~/.ssh` keys), host-key callback, keepalive, command execution. |
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
  flags falls through to raw `exec` (`parseLsCommand` in `daemon/ops.go`).
- Host-key verification is trust-on-first-use when `~/.ssh/known_hosts` is absent
  (`sshutil/ssh.go`); the fingerprint is printed on connect so it can be verified.
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
