//go:build (!linux && !darwin) || cosmo

package remotefs

import "errors"

// Options configures a mount. In a build without FUSE support it exists only
// so callers compile.
type Options struct {
	AllowOther bool
	Debug      bool
	Name       string
}

// Mount is a live mount. Unreachable in this build.
type Mount struct{}

// MountClient reports that mounting is unavailable. Two builds land here: a
// platform where FUSE does not exist, and the portable APE, whose toolchain
// has no libc constants to link go-fuse against. Everything else works, so
// this is a missing feature rather than a broken build. see docs/ape.md
func MountClient(dir string, c *Client, opts Options) (*Mount, error) {
	return nil, errors.New("this build cannot mount a remote filesystem: it has no FUSE support " +
		"(a portable build, or a platform without FUSE). Run with --no-mount to reach the remote " +
		"through remote-agent's own tools instead")
}

// Dir returns the local mount point.
func (m *Mount) Dir() string { return "" }

// Unmount is a no-op: no mount can exist on this platform.
func (m *Mount) Unmount() error { return nil }

// ForceUnmount is a no-op: no mount can exist on this platform.
func (m *Mount) ForceUnmount() error { return nil }

// Wait returns immediately: no mount can exist on this platform.
func (m *Mount) Wait() {}
