//go:build linux || darwin

package remotefs_test

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/agent"
	"github.com/wow-look-at-my/remote-agent/remotefs"
)

// A live mount whose remote is a local directory, so every access travels the
// real path: kernel, FUSE, wire, helper, disk.
type mountFixture struct {
	mnt    string // the mount point: what programs see
	remote string // the backing directory: what the helper serves
}

// newMountFixture mounts a filesystem for the test, skipping when the kernel
// has no FUSE support (some CI sandboxes) rather than failing the run.
func newMountFixture(t *testing.T) *mountFixture {
	t.Helper()
	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Skip("no /dev/fuse on this machine; skipping the mount test")
	}

	remote := t.TempDir()
	mnt := t.TempDir()

	clientEnd, serverEnd := net.Pipe()
	served := make(chan struct{})
	go func() {
		defer close(served)
		agent.ServeFS(remote, serverEnd, serverEnd)
	}()

	client := remotefs.New(clientEnd, clientEnd, clientEnd)
	require.NoError(t, client.Ping())

	mount, err := remotefs.MountClient(mnt, client, remotefs.Options{Name: "remote-agent-test"})
	if err != nil {
		client.Close()
		serverEnd.Close()
		<-served
		if isPermissionish(err) {
			t.Skipf("cannot mount FUSE here (%v); skipping the mount test", err)
		}
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		// Unmount before the helper, and force it: a mount left on a dying helper
		// hangs every later test, and any `df` on the machine.
		if err := mount.ForceUnmount(); err != nil {
			t.Logf("unmount: %v", err)
		}
		serverEnd.Close()
		<-served
	})

	return &mountFixture{mnt: mnt, remote: remote}
}

func isPermissionish(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) ||
		strings.Contains(err.Error(), "permission denied") ||
		strings.Contains(err.Error(), "operation not permitted")
}

func (f *mountFixture) path(rel string) string       { return filepath.Join(f.mnt, rel) }
func (f *mountFixture) remotePath(rel string) string { return filepath.Join(f.remote, rel) }

// TestMountOrdinaryFileIO is the whole point of the mount: a program that has
// never heard of remote-agent (here, the Go standard library) reads and writes
// remote files with plain path operations.
func TestMountOrdinaryFileIO(t *testing.T) {
	f := newMountFixture(t)

	require.NoError(t, os.WriteFile(f.path("hello.txt"), []byte("hello remote\n"), 0o644))

	// It really landed on the "remote" side, not in the mount point's own dir.
	onRemote, err := os.ReadFile(f.remotePath("hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello remote\n", string(onRemote))

	back, err := os.ReadFile(f.path("hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello remote\n", string(back))

	info, err := os.Stat(f.path("hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, int64(13), info.Size())
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestMountAppendAndSeek(t *testing.T) {
	f := newMountFixture(t)
	require.NoError(t, os.WriteFile(f.path("log.txt"), []byte("one\n"), 0o644))

	fh, err := os.OpenFile(f.path("log.txt"), os.O_WRONLY|os.O_APPEND, 0o644)
	require.NoError(t, err)
	_, err = fh.WriteString("two\n")
	require.NoError(t, err)
	require.NoError(t, fh.Close())

	content, err := os.ReadFile(f.path("log.txt"))
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\n", string(content), "O_APPEND must append, not overwrite")

	// Read at an offset, the way a program seeking into a file would.
	fh, err = os.Open(f.path("log.txt"))
	require.NoError(t, err)
	defer fh.Close()
	buf := make([]byte, 3)
	n, err := fh.ReadAt(buf, 4)
	require.NoError(t, err)
	assert.Equal(t, "two", string(buf[:n]))
}

func TestMountLargeFile(t *testing.T) {
	f := newMountFixture(t)

	// Larger than one FUSE write, so the data chunks across many round trips.
	data := bytes.Repeat([]byte("0123456789abcdef"), 200*1024) // 3.2 MiB
	require.NoError(t, os.WriteFile(f.path("big.bin"), data, 0o644))

	back, err := os.ReadFile(f.path("big.bin"))
	require.NoError(t, err)
	assert.Equal(t, len(data), len(back))
	assert.True(t, bytes.Equal(data, back), "large file contents must round-trip exactly")
}

func TestMountBinaryContent(t *testing.T) {
	f := newMountFixture(t)
	data := []byte{0x00, 0xff, 0xfe, 0x01, 0x00, 0x80}
	require.NoError(t, os.WriteFile(f.path("blob.bin"), data, 0o644))

	back, err := os.ReadFile(f.path("blob.bin"))
	require.NoError(t, err)
	assert.Equal(t, data, back, "NUL bytes and invalid UTF-8 must survive")
}

func TestMountDirectoryOperations(t *testing.T) {
	f := newMountFixture(t)

	require.NoError(t, os.MkdirAll(f.path("a/b/c"), 0o755))
	require.NoError(t, os.WriteFile(f.path("a/b/c/leaf.txt"), []byte("leaf"), 0o644))
	require.NoError(t, os.WriteFile(f.path("a/top.txt"), []byte("top"), 0o644))

	entries, err := os.ReadDir(f.path("a"))
	require.NoError(t, err)
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	assert.Equal(t, []string{"b", "top.txt"}, names)

	// A full recursive walk, as a build tool or search would do.
	var walked []string
	require.NoError(t, filepath.WalkDir(f.mnt, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(f.mnt, path)
		walked = append(walked, rel)
		return nil
	}))
	assert.Contains(t, walked, filepath.Join("a", "b", "c", "leaf.txt"))

	require.NoError(t, os.RemoveAll(f.path("a")))
	assert.NoDirExists(t, f.remotePath("a"))
}

func TestMountRenameUnlinkTruncateChmod(t *testing.T) {
	f := newMountFixture(t)
	require.NoError(t, os.WriteFile(f.path("first.txt"), []byte("0123456789"), 0o644))

	require.NoError(t, os.Rename(f.path("first.txt"), f.path("second.txt")))
	assert.FileExists(t, f.remotePath("second.txt"))
	assert.NoFileExists(t, f.remotePath("first.txt"))

	require.NoError(t, os.Truncate(f.path("second.txt"), 4))
	content, err := os.ReadFile(f.path("second.txt"))
	require.NoError(t, err)
	assert.Equal(t, "0123", string(content))

	require.NoError(t, os.Chmod(f.path("second.txt"), 0o600))
	info, err := os.Stat(f.remotePath("second.txt"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	require.NoError(t, os.Remove(f.path("second.txt")))
	assert.NoFileExists(t, f.remotePath("second.txt"))
}

func TestMountSymlinks(t *testing.T) {
	f := newMountFixture(t)
	require.NoError(t, os.WriteFile(f.path("target.txt"), []byte("payload"), 0o644))
	require.NoError(t, os.Symlink("target.txt", f.path("link.txt")))

	target, err := os.Readlink(f.path("link.txt"))
	require.NoError(t, err)
	assert.Equal(t, "target.txt", target)

	info, err := os.Lstat(f.path("link.txt"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "lstat must see a symlink")

	// Following the link reads the target's contents.
	content, err := os.ReadFile(f.path("link.txt"))
	require.NoError(t, err)
	assert.Equal(t, "payload", string(content))
}

func TestMountMissingFileIsENOENT(t *testing.T) {
	f := newMountFixture(t)
	_, err := os.ReadFile(f.path("nope.txt"))
	assert.True(t, os.IsNotExist(err), "a missing remote file must report ENOENT, got %v", err)

	// Negative lookups are not cached, so a file created behind the mount appears at once.
	require.NoError(t, os.WriteFile(f.remotePath("appeared.txt"), []byte("new"), 0o644))
	content, err := os.ReadFile(f.path("appeared.txt"))
	require.NoError(t, err, "a file created on the remote must be visible without waiting for a cache to expire")
	assert.Equal(t, "new", string(content))
}

func TestMountReflectsRemoteChanges(t *testing.T) {
	f := newMountFixture(t)
	require.NoError(t, os.WriteFile(f.remotePath("shared.txt"), []byte("before"), 0o644))

	content, err := os.ReadFile(f.path("shared.txt"))
	require.NoError(t, err)
	assert.Equal(t, "before", string(content))

	// A change on the remote, as a forwarded command makes. It can take a cache window.
	require.NoError(t, os.WriteFile(f.remotePath("shared.txt"), []byte("after-the-change"), 0o644))

	deadline := time.Now().Add(5 * time.Second)
	for {
		content, err = os.ReadFile(f.path("shared.txt"))
		require.NoError(t, err)
		if string(content) == "after-the-change" || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	assert.Equal(t, "after-the-change", string(content),
		"changes made on the remote must become visible through the mount")
}

// TestMountWorksForExternalPrograms is the universality check: a program the
// project has no knowledge of, execed by the OS, operating on remote files
// with no cooperation from remote-agent at all.
func TestMountWorksForExternalPrograms(t *testing.T) {
	f := newMountFixture(t)
	require.NoError(t, os.MkdirAll(f.path("src"), 0o755))
	require.NoError(t, os.WriteFile(f.path("src/main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644))
	require.NoError(t, os.WriteFile(f.path("src/notes.txt"), []byte("nothing here\n"), 0o644))

	grep, err := exec.LookPath("grep")
	if err != nil {
		t.Skip("grep not installed; skipping the external-program check")
	}
	out, err := exec.Command(grep, "-r", "func main", f.path("src")).CombinedOutput()
	require.NoError(t, err, "grep output: %s", out)
	assert.Contains(t, string(out), "main.go")

	// And a program writing through the mount lands on the remote.
	sh, err := exec.LookPath("sh")
	require.NoError(t, err)
	out, err = exec.Command(sh, "-c", "echo written-by-sh > "+f.path("src/from_sh.txt")).CombinedOutput()
	require.NoError(t, err, "sh output: %s", out)

	onRemote, err := os.ReadFile(f.remotePath("src/from_sh.txt"))
	require.NoError(t, err)
	assert.Equal(t, "written-by-sh\n", string(onRemote))
}

func TestMountStatfs(t *testing.T) {
	f := newMountFixture(t)
	var st syscall.Statfs_t
	require.NoError(t, syscall.Statfs(f.mnt, &st))
	assert.NotZero(t, st.Blocks, "df must report the remote filesystem's size")
}

func TestMountDirReportsMountPoint(t *testing.T) {
	f := newMountFixture(t)
	info, err := os.Stat(f.mnt)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}
