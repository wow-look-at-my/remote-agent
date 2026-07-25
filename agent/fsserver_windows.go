package agent

import (
	"errors"
	"io"
)

// ServeFS is unavailable on Windows: the mount protocol is built on POSIX
// filesystem semantics (lstat, symlinks, uid/gid, device nodes) that the
// helper cannot service faithfully there. The rest of the toolset works
// normally, so only mounts are refused.
func ServeFS(root string, in io.Reader, out io.Writer) error {
	return errors.New("serving a filesystem mount is not supported on windows remotes")
}
