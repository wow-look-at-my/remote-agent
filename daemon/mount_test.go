package daemon

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/agent"
	"github.com/wow-look-at-my/remote-agent/protocol"
	"github.com/wow-look-at-my/remote-agent/remotefs"
)

// newMountDaemon returns a daemon whose mount transport is served in-process
// by the real filesystem helper against remoteRoot, so mount handling is
// exercised end to end without SSH.
func newMountDaemon(t *testing.T, remoteRoot string) *Daemon {
	t.Helper()
	d := &Daemon{
		runner:     newMockRunner(),
		remotePath: "/tmp/.remote-agent-test",
		mounts:     mountRegistry{mounts: map[string]*mountEntry{}},
	}
	d.streamFunc = func(command string) (mountStream, error) {
		clientEnd, serverEnd := net.Pipe()
		served := make(chan struct{})
		go func() {
			defer close(served)
			agent.ServeFS(remoteRoot, serverEnd, serverEnd)
		}()
		t.Cleanup(func() {
			serverEnd.Close()
			<-served
		})
		return clientEnd, nil
	}
	t.Cleanup(d.unmountAll)
	return d
}

// requireFUSE skips when the kernel cannot provide a mount here.
func requireFUSE(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Skip("no /dev/fuse on this machine; skipping mount tests")
	}
}

func TestDaemonMountAndUnmount(t *testing.T) {
	requireFUSE(t)
	remote := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(remote, "file.txt"), []byte("remote data"), 0o644))

	d := newMountDaemon(t, remote)
	mnt := filepath.Join(t.TempDir(), "mnt") // does not exist yet; mount creates it

	resp := d.handleMountAction(map[string]any{"local_path": mnt, "remote_path": "/"})
	require.True(t, resp.OK, resp.Error)
	result, ok := resp.Data.(protocol.MountResult)
	require.True(t, ok)
	assert.True(t, result.Mounted)
	assert.Equal(t, mnt, result.LocalPath)

	// The mount serves real content.
	content, err := os.ReadFile(filepath.Join(mnt, "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "remote data", string(content))

	// It shows up in the listing and holds the daemon open.
	listResp := d.handleMountsAction()
	require.True(t, listResp.OK)
	list, ok := listResp.Data.(protocol.MountList)
	require.True(t, ok)
	require.Len(t, list.Mounts, 1)
	assert.Equal(t, mnt, list.Mounts[0].LocalPath)
	assert.True(t, d.hasMounts(), "a live mount must keep the idle watchdog from shutting the daemon down")

	unmountResp := d.handleUnmountAction(map[string]any{"local_path": mnt})
	require.True(t, unmountResp.OK, unmountResp.Error)
	assert.False(t, d.hasMounts())

	// After unmounting, the mount point is an ordinary empty directory again.
	entries, err := os.ReadDir(mnt)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestDaemonMountRejectsDuplicate(t *testing.T) {
	requireFUSE(t)
	d := newMountDaemon(t, t.TempDir())
	mnt := filepath.Join(t.TempDir(), "mnt")

	require.True(t, d.handleMountAction(map[string]any{"local_path": mnt}).OK)
	resp := d.handleMountAction(map[string]any{"local_path": mnt})
	assert.False(t, resp.OK)
	assert.Contains(t, resp.Error, "already mounted")
}

func TestDaemonMountRequiresLocalPath(t *testing.T) {
	d := newMountDaemon(t, t.TempDir())
	resp := d.handleMountAction(map[string]any{"remote_path": "/srv"})
	assert.False(t, resp.OK)
	assert.Contains(t, resp.Error, "local_path")
}

func TestDaemonMountRefusesNonEmptyMountpoint(t *testing.T) {
	d := newMountDaemon(t, t.TempDir())
	mnt := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(mnt, "existing.txt"), []byte("x"), 0o644))

	resp := d.handleMountAction(map[string]any{"local_path": mnt})
	assert.False(t, resp.OK)
	assert.Contains(t, resp.Error, "not empty")
	// The file is still there: a refused mount must not have hidden it.
	assert.FileExists(t, filepath.Join(mnt, "existing.txt"))
}

func TestDaemonMountRefusesFileAsMountpoint(t *testing.T) {
	d := newMountDaemon(t, t.TempDir())
	file := filepath.Join(t.TempDir(), "afile")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))

	resp := d.handleMountAction(map[string]any{"local_path": file})
	assert.False(t, resp.OK)
	assert.Contains(t, resp.Error, "not a directory")
}

func TestDaemonMountFailsWhenHelperSilent(t *testing.T) {
	// Keep the bound short: this test is about the timeout existing at all.
	original := remotefs.PingTimeout
	remotefs.PingTimeout = 300 * time.Millisecond
	defer func() { remotefs.PingTimeout = original }()

	d := &Daemon{
		runner:     newMockRunner(),
		remotePath: "/tmp/.remote-agent-test",
		mounts:     mountRegistry{mounts: map[string]*mountEntry{}},
	}
	// A helper that accepts the stream but never answers: the mount must be
	// refused rather than handed to the kernel, where every access would hang.
	d.streamFunc = func(command string) (mountStream, error) {
		clientEnd, serverEnd := net.Pipe()
		t.Cleanup(func() { serverEnd.Close() })
		go io.Copy(io.Discard, serverEnd)
		return clientEnd, nil
	}

	mnt := filepath.Join(t.TempDir(), "mnt")
	done := make(chan *protocol.DaemonResponse, 1)
	go func() {
		done <- d.handleMountAction(map[string]any{"local_path": mnt})
	}()

	select {
	case resp := <-done:
		assert.False(t, resp.OK)
		assert.Contains(t, resp.Error, "did not respond")
	case <-time.After(remotefs.PingTimeout + 5*time.Second):
		t.Fatal("mount hung waiting for an unresponsive helper")
	}
}

func TestDaemonMountReportsTransportFailure(t *testing.T) {
	d := &Daemon{
		runner:     newMockRunner(),
		remotePath: "/tmp/.remote-agent-test",
		mounts:     mountRegistry{mounts: map[string]*mountEntry{}},
	}
	d.streamFunc = func(command string) (mountStream, error) {
		return nil, assert.AnError
	}

	resp := d.handleMountAction(map[string]any{"local_path": filepath.Join(t.TempDir(), "mnt")})
	assert.False(t, resp.OK)
	assert.Contains(t, resp.Error, "start remote filesystem helper")
}

func TestDaemonMountCommandTargetsRemoteRoot(t *testing.T) {
	requireFUSE(t)
	remote := t.TempDir()

	var gotCommand string
	d := newMountDaemon(t, remote)
	inner := d.streamFunc
	d.streamFunc = func(command string) (mountStream, error) {
		gotCommand = command
		return inner(command)
	}

	mnt := filepath.Join(t.TempDir(), "mnt")
	require.True(t, d.handleMountAction(map[string]any{"local_path": mnt, "remote_path": "/srv/app"}).OK)

	assert.Contains(t, gotCommand, "serve fs")
	assert.Contains(t, gotCommand, "--root '/srv/app'", "the remote path must be quoted, not interpolated raw")
}

func TestDaemonUnmountUnknownPath(t *testing.T) {
	d := newMountDaemon(t, t.TempDir())
	resp := d.handleUnmountAction(map[string]any{"local_path": t.TempDir()})
	assert.False(t, resp.OK)
	assert.Contains(t, resp.Error, "not mounted")

	assert.False(t, d.handleUnmountAction(map[string]any{}).OK)
}

func TestDaemonMountsEmptyByDefault(t *testing.T) {
	d := newMountDaemon(t, t.TempDir())
	resp := d.handleMountsAction()
	require.True(t, resp.OK)
	list, ok := resp.Data.(protocol.MountList)
	require.True(t, ok)
	assert.Empty(t, list.Mounts)
	assert.False(t, d.hasMounts())
}

func TestDaemonUnmountAllDetachesEverything(t *testing.T) {
	requireFUSE(t)
	d := newMountDaemon(t, t.TempDir())
	first := filepath.Join(t.TempDir(), "one")
	second := filepath.Join(t.TempDir(), "two")
	require.True(t, d.handleMountAction(map[string]any{"local_path": first}).OK)
	require.True(t, d.handleMountAction(map[string]any{"local_path": second}).OK)
	require.True(t, d.hasMounts())

	d.unmountAll()
	assert.False(t, d.hasMounts(), "shutdown must leave no mount attached to a dying connection")
}

func TestPrepareMountpoint(t *testing.T) {
	// A missing directory is created.
	fresh := filepath.Join(t.TempDir(), "a", "b")
	require.NoError(t, prepareMountpoint(fresh))
	assert.DirExists(t, fresh)

	require.NoError(t, prepareMountpoint(t.TempDir()))
}

func TestDaemonRefusesToMountADirectoryOverItself(t *testing.T) {
	requireFUSE(t)
	shared := t.TempDir()
	d := newMountDaemon(t, shared)
	runner := d.runner.(*mockRunner)
	runner.defaultResponse = mockResponse{stdout: []byte("same\n")}

	resp := d.handleMountAction(map[string]any{"local_path": shared, "remote_path": shared})
	require.False(t, resp.OK)
	assert.Contains(t, resp.Error, "same directory")
	assert.Contains(t, resp.Error, "--mount-at")

	// The probe file is cleaned up either way.
	entries, err := os.ReadDir(shared)
	require.NoError(t, err)
	assert.Empty(t, entries, "the self-mount probe must not be left behind")
}

func TestDaemonMountProceedsWhenRemoteIsDifferent(t *testing.T) {
	requireFUSE(t)
	remote := t.TempDir()
	d := newMountDaemon(t, remote)
	// The runner answers nothing, so the probe file is invisible: a different
	mnt := filepath.Join(t.TempDir(), "mnt")
	assert.True(t, d.handleMountAction(map[string]any{"local_path": mnt, "remote_path": "/"}).OK)
}
