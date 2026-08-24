# The shell-prefix shim

`client/claudeshim.go` implements the hidden `claude-shim` command -- the
program `CLAUDE_CODE_SHELL_PREFIX` invokes, through `client/shellprefix.sh`,
when Claude Code is launched by `remote-agent claude`.

## Three kinds of spawn arrive through one prefix

Claude Code v2.1.185 and later wrap three kinds of spawn with the shell
prefix, and pass each full command line as one argument.

1. **Bash tool commands**, wrapped in machine-generated scaffolding (below).
   These must run on the remote host. That is the whole point of the launcher.
2. **Hook commands** (SessionStart and the rest), passed bare. Claude injects
   `CLAUDE_PROJECT_DIR`, `CLAUDE_ENV_FILE` and the rest into the *local* spawn
   environment only, and the hook script exists on the local machine. So hooks
   run locally -- exactly as they did before v2.1.185, when a hook command was
   not prefix-wrapped at all.
3. **MCP stdio servers**, long-lived bidirectional JSON-RPC processes. The
   remote exec path is buffered request and response, with no stdin bridge, so
   it can never carry one. Like hooks, they rely on local environment and
   local files, so they run locally too.

## The Bash tool wrapper

For a user command `<cmd>`, v2.1.185 builds one `&&`-joined string:

```
source <local snapshot path> 2>/dev/null || true &&   [iff the snapshot exists locally]
<session-env preamble text>\n: &&                      [iff a hook wrote session env]
{ shopt -u extglob || setopt NO_EXTENDED_GLOB NO_BARE_GLOB_QUAL; } >/dev/null 2>&1 || true &&
eval '<cmd>' < /dev/null &&
pwd -P >| <local tmpdir>/claude-<id>-cwd
```

Two of those clauses are poison on a remote host.

- **The leading `source` of the local shell-snapshot file.** The file does not
  exist remotely, and sourcing a local bash state dump would be wrong even if
  it did. On BusyBox ash, `source` is the POSIX special builtin `.`, so the
  failed open is a fatal shell abort that `|| true` cannot rescue and
  `2>/dev/null` silences. Every command then returns zero bytes with exit 2,
  which Claude renders as "(No output)". A bash remote survives only because
  bash's `source` is non-fatal; dash survives only because it has no `source`
  at all.
- **The trailing `pwd -P >| <path>`.** It writes Claude's cwd-tracking file
  into the *remote* `/tmp`. The local file Claude actually reads is never
  written, so Claude reports "Shell cwd was reset" after every command.

`launderBashToolWrapper` strips exactly those two clauses before forwarding.
Everything between them -- the inlined session-env text, the glob-setup
marker, the eval -- is remote-safe and goes over verbatim. After a successful
remote run, the shim writes the local cwd file itself with its own working
directory, which is what `pwd -P` would have printed for a local run that did
not `cd`. That keeps Claude's cwd tracker quiet.

A clause that does not match exactly is left untouched: unrecognized input is
forwarded unchanged rather than mangled. `stripCwdTail` uses the *last*
separator occurrence and requires the path token to consume the whole
remainder, so a lookalike sequence inside the quoted eval body never matches.

## How the shim tells the three apart

`bashToolMarker` is the glob-setup clause v2.1.185 embeds in every Bash tool
wrapper when `CLAUDE_CODE_SHELL_PREFIX` is set, and only then. (An unset
prefix gets a shell-specific `shopt`/`setopt` form instead, but then no shim
runs at all.) Hook and MCP command lines are user configuration and cannot
plausibly contain this machine-generated text, so its presence is the
classification signal: marker means a Bash tool command, to forward; no marker
means a hook, an MCP server or something else, to run locally.

The misclassification risks are asymmetric. Forwarding a hook merely breaks
the hook, through missing environment and missing files. Running a Bash tool
command locally would silently execute the user's command on the wrong
machine, which is worse. The marker is the most conservative signal available.

`snapshotPathMarker` identifies Claude's shell-snapshot files: they always sit
in a `shell-snapshots` directory and are named `snapshot-<shell>-...`. The
leading source clause is stripped only when the sourced path matches, so
nothing that is not a Claude snapshot reference is ever removed.

## The working directory

`prefixWorkingDirectory` makes a forwarded command run in the directory claude
works in, rather than the remote home. It applies only when the session
mounted the remote at the identical local path (`REMOTE_AGENT_MOUNT`), because
only then is the local working directory also a valid remote directory.
Without it, `cat main.go` after a `cd` into a project looks for the file in
the remote home -- the mismatch that made the launcher feel like two machines
instead of one.
