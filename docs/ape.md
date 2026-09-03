# Starting this binary again: why a shell is in front of it

A release of remote-agent is a Cosmopolitan **Actually Portable Executable**:
one file that runs on Linux, macOS and Windows, on amd64 and arm64. That is
what makes `deployBinary` sane -- the helper shipped to the remote is this same
binary, whatever the remote turns out to be.

The header is the catch. An APE begins `MZqFpD='`, which is a shell script and
a PE header and not an ELF header. Two things follow from that:

- **A shell runs it anywhere.** `execve` answers `ENOEXEC`, and POSIX then
  requires the shell to run the file as a script. The APE header *is* that
  script, and it re-execs the right architecture's payload.
- **A raw `execve` does not.** Go's `os/exec` performs no such fallback, and
  neither does Node's `spawn`, which is how an MCP client starts a server. On a
  host with an APE `binfmt_misc` entry the kernel loads it directly and both
  work; without one -- an ordinary server, an unprivileged user, a container --
  the call fails with `exec format error`.

The entry is not something remote-agent can rely on. Cosmopolitan registers it
on first run when it is root, so a developer machine acquires one and hides the
problem, while the host anyone else uses does not have it.

## Where this binary starts itself

Both sites go through `client.SelfCommand`, which returns `/bin/sh -c 'exec
"$0" "$@"' <self> <args...>` on Unix (Windows loads the PE header directly, and
has no `/bin/sh` to name):

- **Daemon auto-start** (`client/daemonproc.go`). Any command may find no
  daemon and start one, so this is every command's failure when it breaks.
  `exec` replaces the shell, so the process the caller waits on and signals is
  still the daemon itself -- `ps` shows no shell at all.
- **The MCP server command** in the config the `claude` launcher writes
  (`client/launch.go`). Claude spawns that itself, and a server that cannot
  start leaves the client waiting on a handshake that never arrives.

Use `SelfCommand` for any further site. The form also works for an ordinary
ELF binary, so nothing has to know which kind of build it is.

## The remote host needs nothing extra

The deployed helper is invoked as `<path> serve <sub>`, and sshd hands that
line to the remote user's login shell -- the same shell that runs every other
command remote-agent sends. The ENOEXEC fallback above is therefore already in
play, and an APE helper runs on a remote with no binfmt entry.

What the path does need is quoting, like every other argument: a remote home
directory with a space in it splits into two words otherwise. `Daemon.helper()`
is the one place that renders it.

## Checking a host by hand

```sh
# Does this host load an APE without a shell?
ls /proc/sys/fs/binfmt_misc/APE            # present: the kernel does it
env ./remote-agent --help                  # "exec format error" means it does not
sh -c './remote-agent --help'              # this works either way
```
