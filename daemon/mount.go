package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/wow-look-at-my/remote-agent/protocol"
	"github.com/wow-look-at-my/remote-agent/remotefs"
	"github.com/wow-look-at-my/remote-agent/sshutil"
)

// mountRegistry tracks the filesystem mounts this daemon owns, keyed by their
// local mount point. The daemon holds them because it owns the SSH connection
// they run over: a mount is a long-lived stream on that connection, and it
// must be torn down with it.
type mountRegistry struct {
	mu     sync.Mutex
	mounts map[string]*mountEntry
}

type mountEntry struct {
	mount      *remotefs.Mount
	remotePath string
}

// streamStarter opens a long-lived remote command stream. It is the seam for
// testing the mount plumbing without SSH.
type streamStarter func(command string) (mountStream, error)

// mountStream is the bidirectional transport to the remote helper.
type mountStream interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() error
}

func (d *Daemon) handleMountAction(params map[string]any) *protocol.DaemonResponse {
	localPath, _ := params["local_path"].(string)
	remotePath, _ := params["remote_path"].(string)
	allowOther, _ := params["allow_other"].(bool)
	if localPath == "" {
		return errResponse(fmt.Errorf("missing 'local_path' parameter"))
	}
	if remotePath == "" {
		remotePath = "/"
	}
	abs, err := filepath.Abs(localPath)
	if err != nil {
		return errResponse(fmt.Errorf("resolve %s: %w", localPath, err))
	}

	if err := d.mount(abs, remotePath, allowOther); err != nil {
		return errResponse(err)
	}
	d.auditAsync("mount", fmt.Sprintf("remote=%s local=%s", remotePath, abs))
	return okResponse(protocol.MountResult{LocalPath: abs, RemotePath: remotePath, Mounted: true})
}

// mount starts the remote filesystem helper and mounts it locally.
func (d *Daemon) mount(localPath, remotePath string, allowOther bool) error {
	d.mounts.mu.Lock()
	if _, exists := d.mounts.mounts[localPath]; exists {
		d.mounts.mu.Unlock()
		return fmt.Errorf("%s is already mounted", localPath)
	}
	d.mounts.mu.Unlock()

	if err := prepareMountpoint(localPath); err != nil {
		return err
	}
	if err := refuseSelfMount(d.runner, localPath, remotePath); err != nil {
		return err
	}

	command := fmt.Sprintf("%s serve fs --root %s", d.remotePath, shellEscape(remotePath))
	stream, err := d.startStream(command)
	if err != nil {
		return fmt.Errorf("start remote filesystem helper: %w", err)
	}

	client := remotefs.New(stream, stream, stream)
	// Verify the helper is actually answering before handing the mount to the
	// kernel: a mountpoint backed by a dead helper hangs every process that
	// touches it, which is far worse than failing here.
	if err := client.Ping(); err != nil {
		client.Close()
		return fmt.Errorf("remote filesystem helper did not respond: %w", err)
	}

	name := fmt.Sprintf("remote-agent:%s", remotePath)
	if d.conn != nil {
		name = fmt.Sprintf("%s@%s:%s", d.conn.User, d.conn.Host, remotePath)
	}
	mount, err := remotefs.MountClient(localPath, client, remotefs.Options{
		AllowOther: allowOther,
		Name:       name,
	})
	if err != nil {
		client.Close()
		return err
	}

	d.mounts.mu.Lock()
	d.mounts.mounts[localPath] = &mountEntry{mount: mount, remotePath: remotePath}
	d.mounts.mu.Unlock()
	return nil
}

// startStream opens the transport for a mount, honoring the test seam.
func (d *Daemon) startStream(command string) (mountStream, error) {
	if d.streamFunc != nil {
		return d.streamFunc(command)
	}
	if d.conn == nil {
		return nil, fmt.Errorf("no SSH connection")
	}
	return sshutil.StartStream(d.conn.Client, command)
}

func (d *Daemon) handleUnmountAction(params map[string]any) *protocol.DaemonResponse {
	localPath, _ := params["local_path"].(string)
	if localPath == "" {
		return errResponse(fmt.Errorf("missing 'local_path' parameter"))
	}
	abs, err := filepath.Abs(localPath)
	if err != nil {
		return errResponse(fmt.Errorf("resolve %s: %w", localPath, err))
	}

	d.mounts.mu.Lock()
	entry, ok := d.mounts.mounts[abs]
	if ok {
		delete(d.mounts.mounts, abs)
	}
	d.mounts.mu.Unlock()
	if !ok {
		return errResponse(fmt.Errorf("%s is not mounted by this daemon", abs))
	}

	if err := entry.mount.Unmount(); err != nil {
		// Put it back: the mount is still live, and forgetting it here would
		// leak it past shutdown.
		d.mounts.mu.Lock()
		d.mounts.mounts[abs] = entry
		d.mounts.mu.Unlock()
		return errResponse(fmt.Errorf("unmount %s: %w (is a process still using it?)", abs, err))
	}
	d.auditAsync("unmount", fmt.Sprintf("local=%s", abs))
	return okResponse(protocol.MountResult{LocalPath: abs, RemotePath: entry.remotePath, Mounted: false})
}

func (d *Daemon) handleMountsAction() *protocol.DaemonResponse {
	d.mounts.mu.Lock()
	defer d.mounts.mu.Unlock()

	list := make([]protocol.MountInfo, 0, len(d.mounts.mounts))
	for local, entry := range d.mounts.mounts {
		list = append(list, protocol.MountInfo{LocalPath: local, RemotePath: entry.remotePath})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].LocalPath < list[j].LocalPath })
	return okResponse(protocol.MountList{Mounts: list})
}

// hasMounts reports whether any mount is live. The idle watchdog consults it:
// a mount is state the user is relying on, and unmounting it because nobody
// ran a command for a while would break every open file under it.
func (d *Daemon) hasMounts() bool {
	d.mounts.mu.Lock()
	defer d.mounts.mu.Unlock()
	return len(d.mounts.mounts) > 0
}

// unmountAll detaches every mount during shutdown, so the daemon never exits
// leaving mountpoints whose backing connection is gone.
func (d *Daemon) unmountAll() {
	d.mounts.mu.Lock()
	entries := make(map[string]*mountEntry, len(d.mounts.mounts))
	for k, v := range d.mounts.mounts {
		entries[k] = v
		delete(d.mounts.mounts, k)
	}
	d.mounts.mu.Unlock()

	for path, entry := range entries {
		// Force here: the SSH connection backing these mounts is about to
		// close, and a mount left attached to a dead session hangs every
		// process that touches it -- including ones that merely stat it.
		if err := entry.mount.ForceUnmount(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not unmount %s: %v\n", path, err)
		}
	}
}

// refuseSelfMount rejects mounting a remote directory onto itself, which
// happens when the "remote" is this same machine (a loopback SSH target, a
// container sharing the host's filesystem) and the mount point is the remote
// path. The helper would then serve the mount point through the mount, and
// every access recurses into a hang -- a failure that is both easy to trigger
// while testing and unpleasant to recover from.
//
// The check drops a marker in the local mount point and asks the remote
// whether it can see it. A remote that cannot be asked (no runner) is assumed
// to be a genuinely different machine.
func refuseSelfMount(runner Runner, localPath, remotePath string) error {
	if runner == nil {
		return nil
	}
	marker := filepath.Join(localPath, ".remote-agent-mount-check-"+randomSuffix())
	f, err := os.Create(marker)
	if err != nil {
		// Cannot write the probe: leave the decision to the mount itself
		// rather than refusing a mount that might be perfectly fine.
		return nil
	}
	f.Close()
	defer os.Remove(marker)

	probe := fmt.Sprintf("test -e %s && echo same", shellEscape(filepath.Join(remotePath, filepath.Base(marker))))
	stdout, _, _, err := runner.Run(probe)
	if err != nil {
		return nil
	}
	if strings.TrimSpace(string(stdout)) == "same" {
		return fmt.Errorf("%s on the remote is the same directory as the local mount point %s; "+
			"mounting it over itself would recurse. Use --mount-at (or a different mount point) to keep them apart",
			remotePath, localPath)
	}
	return nil
}

// prepareMountpoint makes sure localPath is a usable mount point: an existing
// empty directory, or one this call creates. Mounting over a directory with
// files in it would hide them for as long as the mount lives, so that is
// refused rather than done quietly.
func prepareMountpoint(localPath string) error {
	info, err := os.Stat(localPath)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(localPath, 0o755); err != nil {
			return fmt.Errorf("create mount point %s: %w", localPath, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("stat mount point %s: %w", localPath, err)
	case !info.IsDir():
		return fmt.Errorf("mount point %s is not a directory", localPath)
	}

	entries, err := os.ReadDir(localPath)
	if err != nil {
		return fmt.Errorf("read mount point %s: %w", localPath, err)
	}
	if len(entries) > 0 {
		names := make([]string, 0, 3)
		for _, e := range entries[:min(len(entries), 3)] {
			names = append(names, e.Name())
		}
		return fmt.Errorf("mount point %s is not empty (contains %s); mounting would hide its contents",
			localPath, strings.Join(names, ", "))
	}
	return nil
}
