# The mount

`remotefs/` is the local half of a mount: a request multiplexer (`client.go`)
and the go-fuse filesystem (`fuse_unix.go`). `agent/fsserver_unix.go` is the
remote half, and `fswire/` is the protocol between them.

## Caching is short on purpose

Attributes and directory entries are cached briefly, so a tree walk does not
pay a round trip per stat. The window stays short because the remote changes
underneath the mount: shell commands the launcher forwards run there, and
their output has to be visible to the next read.

Negative lookups are not cached at all. A file a build command just created
has to appear immediately, and "it did not exist a second ago" is exactly the
wrong thing to remember.

`maxWrite` is 1 MiB, against the 128 KiB default. That cuts the round trips a
large file copy costs by eight.

## Mount options

- `DirectMount` tries `mount(2)` first and falls back to `fusermount`, so the
  mount works both as root (containers, CI) and as an ordinary user.
- `DisableXAttrs` is set because extended attributes are not part of the wire
  protocol. Saying so up front stops the kernel asking on every file
  operation.

A `node` is one file or directory in the mounted tree. Its path comes from its
position in the tree, so renaming a parent directory moves its children with
it for free.

## Unmounting, and why force is the shutdown path

`Unmount` detaches the filesystem and ends the session with the remote helper.
It fails while a process still has the mount as its working directory, or has
files open on it. A caller that asked to unmount deserves to hear that, rather
than have its shell yanked out from under it. The session closes either way:
leaving the helper attached to a mount that is going away only leaks a remote
process.

`ForceUnmount` detaches even a busy mount, and it is what every shutdown path
uses. A mount whose backing session has ended is not merely broken, it is
contagious: every process that so much as stats it blocks in the kernel
forever, so `df`, a shell completing a path, or an unrelated test's statfs all
hang. Detaching a busy mount is strictly better than leaving that behind. On
Linux the flag is `MNT_DETACH`, which unlinks the mount from the namespace and
lets existing users finish against it.

## The handshake is the one bounded call

`PingTimeout` bounds the handshake with the remote helper. Ordinary filesystem
calls are deliberately unbounded, because a large read over a slow link
legitimately takes a while. The handshake must not be: a helper that accepts
the stream and never answers would hang the mount request, and the caller
waiting on it, forever.

`Client` multiplexes requests over one stream. Calls from many FUSE threads
are in flight at once and replies arrive in whatever order the remote finishes
them, so each reply is matched back to its caller by request ID.

## The frame

One frame is `uint32 headerLen | header JSON | uint32 payloadLen | payload`.
File data stays out of the JSON: base64 would inflate every read and write by
a third, and JSON strings cannot carry arbitrary bytes at all.

`MaxPayload` bounds what a peer can make the other end allocate, and sits well
above FUSE's 1 MiB write. `MaxHeader` exists only to stop a corrupt length
prefix allocating wildly, because a header is small even for a large readdir.

## `O_APPEND` and open flags

`O_APPEND` is stripped from the remote open (`agent/fsserver_unix.go`). Every
read and write carries an explicit offset -- the kernel resolves append
offsets before it sends the request -- and pwrite on an `O_APPEND` descriptor
ignores that offset. Go rejects the combination outright, which surfaced as
EIO on every append through the mount.

Open flags are translated rather than passed through (`fswire/openflags.go`).
`O_APPEND` is `0x8` on darwin and `0x400` on Linux, and `O_TRUNC` and
`O_EXCL` differ too, so raw flags on a cross-platform mount would silently
truncate files.
