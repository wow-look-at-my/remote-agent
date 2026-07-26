//go:build !linux && !darwin

package remotefs

import "errors"

// Options configures a mount. On platforms without FUSE support it exists
// only so callers compile.
type Options struct {
	AllowOther bool
	Debug      bool
	Name       string
}

// Mount is a live mount. Unreachable on this platform.
type Mount struct{}

// MountClient reports that mounting is unavailable. FUSE exists only on Linux
// and macOS; the rest of remote-agent works normally here, so this is a
// missing feature rather than a broken build.
func MountClient(dir string, c *Client, opts Options) (*Mount, error) {
	return nil, errors.New("mounting a remote filesystem is only supported on linux and macos")
}

// Dir returns the local mount point.
func (m *Mount) Dir() string { return "" }

// Unmount is a no-op: no mount can exist on this platform.
func (m *Mount) Unmount() error { return nil }

// ForceUnmount is a no-op: no mount can exist on this platform.
func (m *Mount) ForceUnmount() error { return nil }

// Wait returns immediately: no mount can exist on this platform.
func (m *Mount) Wait() {}
