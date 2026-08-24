# The `claude` launcher

`remote-agent claude [user@host]` starts (or reuses) a daemon, mounts the
remote filesystem locally, writes the shell-prefix shim, and runs Claude Code
in the mount. `client/launch.go` holds the orchestration.

## The mount point is the same absolute path as the remote directory

Claude's file tools reach files through the mount, with local paths. Bash
commands run over SSH, with remote paths. If the two paths differ, one set of
files has two names, and the model mixes them. `--mount-at` (the `MountAt`
option) allows a different local path, and the launcher prints a warning when
it is used.

When the paths do match, the launcher exports `REMOTE_AGENT_MOUNT`. The shim
then prefixes each forwarded command with `cd <cwd> &&`, so a forwarded
command runs in the directory claude works in rather than the remote home.

## `--no-mount` is the fallback, and it is narrower

`NoMount` skips the mount and serves the remote filesystem as MCP tools
instead. Use it on a platform without FUSE. Only the tools remote-agent itself
provides then reach the remote: a third-party MCP server, an editor, or a tool
written later still hits local disk. `LocalTools`, with `NoMount`, leaves
Claude's built-in file tools in place, so only Bash runs remotely.

## Which tools the launcher takes away, and which it pre-allows

`disabledLocalTools` names the built-in tools that reach the local filesystem
directly. They have no shell-prefix hook: they call Node's `fs` against the
machine claude runs on. The only way to keep a remote session honest is to
take them out of the tool set and hand the model the remote MCP equivalents.

Only names that exist are listed. Claude warns at startup for each deny rule
that "matches no known tool", so a speculative entry (`MultiEdit`,
`NotebookRead`) is startup noise, not insurance.

`preAllowedRemoteTools` names the read-only remote tools allowed up front.
MCP tools always prompt on first use, while the built-in Read, Glob and Grep
never did, so pre-allowing the read-only four keeps the permission behaviour
of the tools they replace. The mutating tools (`write_file`, `edit_file`,
`upload_file`, `download_file`) are deliberately absent and still prompt.

## Claude flags use the `--flag=value` form

Claude declares `--mcp-config`, `--disallowedTools` and `--allowedTools` as
variadic options (`<configs...>`, `<tools...>`). In the space-separated form,
commander keeps consuming following arguments, so the flag would swallow the
user's own prompt and flags. The `=` form binds exactly one value and stops.
Repeated occurrences concatenate, so the user's own copies still apply.

## What the launcher pins in the environment

- `REMOTE_AGENT_TARGET` -- so a forwarded command, or the MCP server, can
  bring the daemon back up by itself when it dies or idles out mid-session.
- `REMOTE_AGENT_SOCKET` -- pinned explicitly, so the tools reach this
  launch's daemon even when the environment is scrubbed or several daemons
  run.
- `REMOTE_AGENT_MOUNT` -- set only when the mount point equals the remote
  directory. See above.

## Finding the daemon to reuse

The launcher prefers a live socket. The target record beside that socket names
the host, so the rest of the session passes the target explicitly instead of
relying on discovery. When nothing listens, the launcher falls back to the
target a daemon last ran for, so a session that idled out resumes without
retyping the target. The socket it then waits on is computed from the record
(`daemon.SocketPath(rec.Target)`), because that is the socket the started
daemon opens.
