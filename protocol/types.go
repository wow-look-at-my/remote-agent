package protocol

import "time"

// The bound on an exec: the daemon enforces it, and the CLI flag and the MCP
// tool schema describe it, so all three read one definition rather than three
// numbers that drift apart. see docs/daemon/timeouts.md
const (
	// ExecDefaultTimeout applies when a call names no timeout of its own.
	ExecDefaultTimeout = 10 * time.Minute
	// ExecMaxTimeout is the longest a call may ask for.
	ExecMaxTimeout = 6 * time.Hour
)

// DaemonRequest is sent from CLI to the local daemon over the Unix socket.
type DaemonRequest struct {
	Action string         `json:"action"`
	Params map[string]any `json:"params,omitempty"`
}

// Route says which host an action runs on. It travels with the call, because one MCP
// server serves several hosts and is handed each one mid-session.
type Route struct {
	// Target is the SSH target: user@host, or a ~/.ssh/config Host alias.
	Target string `json:"target"`
	// A control master to run through. Naming one makes it mandatory.
	ControlPath string `json:"control_path,omitempty"`
}

// DaemonResponse is sent from the daemon back to the CLI.
type DaemonResponse struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// ExecResult is returned by the exec action.
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

// What read answers with. Exactly one of Content and ContentB64 is set, because JSON
// cannot carry invalid UTF-8.
type FileInfo struct {
	Content    string `json:"content,omitempty"`
	ContentB64 string `json:"content_b64,omitempty"`
	Size       int64  `json:"size"`
}

// WriteResult is returned by write/upload actions.
type WriteResult struct {
	BytesWritten int64 `json:"bytes_written"`
}

// EditResult is returned by the edit action.
type EditResult struct {
	Modified bool   `json:"modified"`
	Message  string `json:"message,omitempty"`
	// Occurrences replaced. Always 1 unless the edit was a replace-all.
	Replacements int `json:"replacements,omitempty"`
}

// GlobResult is returned by the glob action: remote paths matching a glob
// pattern, most recently modified first.
type GlobResult struct {
	Pattern string   `json:"pattern"`
	Path    string   `json:"path"`
	Files   []string `json:"files"`
	// Truncated reports that more files matched than the requested limit.
	Truncated bool `json:"truncated,omitempty"`
}

// Grep output modes.
const (
	GrepModeContent = "content"            // matching lines
	GrepModeFiles   = "files_with_matches" // paths only
	GrepModeCount   = "count"              // per-file match counts
)

// One line from a content-mode grep. IsContext separates context from a real match.
type GrepMatch struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Text      string `json:"text"`
	IsContext bool   `json:"is_context,omitempty"`
}

// GrepFileCount is a per-file match count emitted by a count-mode grep.
type GrepFileCount struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// GrepResult is returned by the grep action. Which field carries the payload
// depends on Mode: Matches for content, Files for files_with_matches, Counts
// for count.
type GrepResult struct {
	Pattern string          `json:"pattern"`
	Mode    string          `json:"mode"`
	Matches []GrepMatch     `json:"matches,omitempty"`
	Files   []string        `json:"files,omitempty"`
	Counts  []GrepFileCount `json:"counts,omitempty"`
	// Files actually searched. A binary or excluded file does not count.
	FilesScanned int `json:"files_scanned"`
	// Truncated reports that output was cut off at the requested limit.
	Truncated bool `json:"truncated,omitempty"`
}

// MountResult is returned by the mount and unmount actions.
type MountResult struct {
	LocalPath  string `json:"local_path"`
	RemotePath string `json:"remote_path"`
	Mounted    bool   `json:"mounted"`
}

// MountInfo describes one live mount.
type MountInfo struct {
	LocalPath  string `json:"local_path"`
	RemotePath string `json:"remote_path"`
}

// MountList is returned by the mounts action.
type MountList struct {
	Mounts []MountInfo `json:"mounts"`
}

// DirEntry represents a single directory listing entry.
type DirEntry struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	IsDir   bool   `json:"is_dir"`
	IsLink  bool   `json:"is_link,omitempty"`
	Target  string `json:"target,omitempty"` // symlink target
	ModTime int64  `json:"mod_time"`         // unix timestamp
}

// DirListing is returned by the ls action.
type DirListing struct {
	Path    string     `json:"path"`
	Entries []DirEntry `json:"entries"`
}

// ProcessInfo represents a single process.
type ProcessInfo struct {
	PID     int    `json:"pid"`
	PPID    int    `json:"ppid"`
	User    string `json:"user"`
	State   string `json:"state"`
	RSS     int64  `json:"rss_bytes"`
	Command string `json:"command"`
}

// ProcessList is returned by the ps action.
type ProcessList struct {
	Processes []ProcessInfo `json:"processes"`
}

// CPUInfo holds CPU details.
type CPUInfo struct {
	Model   string  `json:"model"`
	Cores   int     `json:"cores"`
	Threads int     `json:"threads"`
	MHz     float64 `json:"mhz"`
}

// MemoryInfo holds memory details.
type MemoryInfo struct {
	TotalBytes     int64 `json:"total_bytes"`
	AvailableBytes int64 `json:"available_bytes"`
	UsedBytes      int64 `json:"used_bytes"`
	SwapTotalBytes int64 `json:"swap_total_bytes"`
	SwapUsedBytes  int64 `json:"swap_used_bytes"`
}

// DiskInfo holds disk usage for a mount point.
type DiskInfo struct {
	Device     string  `json:"device"`
	MountPoint string  `json:"mount_point"`
	FSType     string  `json:"fs_type"`
	TotalBytes int64   `json:"total_bytes"`
	UsedBytes  int64   `json:"used_bytes"`
	AvailBytes int64   `json:"avail_bytes"`
	UsePct     float64 `json:"use_pct"`
}

// NetworkInterface holds info about a network interface.
type NetworkInterface struct {
	Name  string   `json:"name"`
	MAC   string   `json:"mac"`
	State string   `json:"state"`
	IPv4  []string `json:"ipv4"`
	IPv6  []string `json:"ipv6"`
}

// GPUInfo holds GPU details.
type GPUInfo struct {
	Index      int     `json:"index"`
	Name       string  `json:"name"`
	Vendor     string  `json:"vendor"`
	MemTotalMB int     `json:"mem_total_mb"`
	MemUsedMB  int     `json:"mem_used_mb"`
	UtilPct    float64 `json:"util_pct"`
	TempC      int     `json:"temp_celsius,omitempty"`
}

// SystemInfo holds all system stats.
type SystemInfo struct {
	Hostname string             `json:"hostname"`
	OS       string             `json:"os"`
	Arch     string             `json:"arch"`
	Uptime   string             `json:"uptime"`
	CPU      CPUInfo            `json:"cpu"`
	Memory   MemoryInfo         `json:"memory"`
	Disk     []DiskInfo         `json:"disk"`
	Network  []NetworkInterface `json:"network"`
	GPU      []GPUInfo          `json:"gpu"`
}

// ReadlinkResult is returned by the readlink action.
type ReadlinkResult struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

// PingResult is returned by the ping action.
type PingResult struct {
	Pong bool `json:"pong"`
}

// AuditEntry represents an audit log entry.
type AuditEntry struct {
	Action      string `json:"action"`
	Detail      string `json:"detail,omitempty"`
	User        string `json:"user,omitempty"`
	ClientIP    string `json:"client_ip,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}
