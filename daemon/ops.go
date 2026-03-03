package daemon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

// Runner abstracts SSH command execution for testability.
type Runner interface {
	Run(command string) (stdout, stderr []byte, exitCode int, err error)
	RunStdin(command string, stdin []byte) (stdout, stderr []byte, exitCode int, err error)
}

func (h *Handler) handlePing() *protocol.DaemonResponse {
	h.daemon.mu.Lock()
	defer h.daemon.mu.Unlock()

	stdout, _, _, err := h.daemon.runner.Run("echo pong")
	if err != nil {
		return errResponse(fmt.Errorf("ping failed: %w", err))
	}
	return okResponse(protocol.PingResult{Pong: strings.TrimSpace(string(stdout)) == "pong"})
}

func (h *Handler) handleExec(params map[string]any) *protocol.DaemonResponse {
	command, _ := params["command"].(string)
	if command == "" {
		return errResponse(fmt.Errorf("missing 'command' parameter"))
	}

	// Rewrite "exec ls [path]" to native ls handler for structured output
	if lsPath, recursive, ok := parseLsCommand(command); ok {
		return h.handleLs(map[string]any{"path": lsPath, "recursive": recursive})
	}

	// Strip pointless trailing 2>&1 — stderr is already captured separately
	command = stripTrailingRedirect(command)

	h.daemon.mu.Lock()
	defer h.daemon.mu.Unlock()

	// Audit log the command
	auditCmd := fmt.Sprintf("%s serve audit --action exec --detail %s",
		h.daemon.remotePath, shellEscape(command))
	h.daemon.runner.Run(auditCmd)

	stdout, stderr, exitCode, err := h.daemon.runner.Run(command)
	if err != nil {
		return errResponse(fmt.Errorf("exec failed: %w", err))
	}
	return okResponse(protocol.ExecResult{
		Stdout:   string(stdout),
		Stderr:   string(stderr),
		ExitCode: exitCode,
	})
}

// parseLsCommand checks if a command is a simple "ls [path]" and returns
// the path and whether it's recursive. Returns ok=false for complex ls
// invocations with unsupported flags.
func parseLsCommand(command string) (path string, recursive bool, ok bool) {
	parts := strings.Fields(command)
	if len(parts) == 0 || parts[0] != "ls" {
		return "", false, false
	}
	path = "."
	for _, p := range parts[1:] {
		if p == "-R" {
			recursive = true
		} else if strings.HasPrefix(p, "-") {
			// Has flags we don't support natively — let exec handle it
			return "", false, false
		} else {
			path = p
		}
	}
	return path, recursive, true
}

// stripTrailingRedirect removes a trailing "2>&1" from commands since
// stderr is already captured separately by the SSH transport.
func stripTrailingRedirect(command string) string {
	trimmed := strings.TrimSpace(command)
	if strings.HasSuffix(trimmed, "2>&1") {
		return strings.TrimSpace(trimmed[:len(trimmed)-4])
	}
	return command
}

func (h *Handler) handleRead(params map[string]any) *protocol.DaemonResponse {
	path, _ := params["path"].(string)
	if path == "" {
		return errResponse(fmt.Errorf("missing 'path' parameter"))
	}

	h.daemon.mu.Lock()
	defer h.daemon.mu.Unlock()

	// Use cat to read the file, base64 encode to avoid binary issues
	cmd := fmt.Sprintf("base64 %s", shellEscape(path))
	stdout, stderr, exitCode, err := h.daemon.runner.Run(cmd)
	if err != nil {
		return errResponse(fmt.Errorf("read failed: %w", err))
	}
	if exitCode != 0 {
		return errResponse(fmt.Errorf("read %s: %s", path, strings.TrimSpace(string(stderr))))
	}

	// Decode base64 to get actual content
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(stdout)))
	if err != nil {
		return errResponse(fmt.Errorf("decode file content: %w", err))
	}

	return okResponse(protocol.FileInfo{
		Content: string(decoded),
		Size:    int64(len(decoded)),
	})
}

func (h *Handler) handleWrite(params map[string]any) *protocol.DaemonResponse {
	path, _ := params["path"].(string)
	content, _ := params["content"].(string)
	mode, _ := params["mode"].(string)
	if path == "" {
		return errResponse(fmt.Errorf("missing 'path' parameter"))
	}
	if mode == "" {
		mode = "0644"
	}

	h.daemon.mu.Lock()
	defer h.daemon.mu.Unlock()

	// Audit
	auditCmd := fmt.Sprintf("%s serve audit --action write --detail %s",
		h.daemon.remotePath, shellEscape(fmt.Sprintf("path=%s size=%d", path, len(content))))
	h.daemon.runner.Run(auditCmd)

	// Write via cat, using base64 to avoid binary issues
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	cmd := fmt.Sprintf("echo %s | base64 -d > %s && chmod %s %s",
		shellEscape(encoded), shellEscape(path), mode, shellEscape(path))
	_, stderr, exitCode, err := h.daemon.runner.Run(cmd)
	if err != nil {
		return errResponse(fmt.Errorf("write failed: %w", err))
	}
	if exitCode != 0 {
		return errResponse(fmt.Errorf("write %s: %s", path, strings.TrimSpace(string(stderr))))
	}

	return okResponse(protocol.WriteResult{BytesWritten: int64(len(content))})
}

func (h *Handler) handleUpload(params map[string]any) *protocol.DaemonResponse {
	localPath, _ := params["local_path"].(string)
	remotePath, _ := params["remote_path"].(string)
	if localPath == "" || remotePath == "" {
		return errResponse(fmt.Errorf("missing 'local_path' or 'remote_path' parameter"))
	}

	// Read local file
	data, err := os.ReadFile(localPath)
	if err != nil {
		return errResponse(fmt.Errorf("read local file %s: %w", localPath, err))
	}

	h.daemon.mu.Lock()
	defer h.daemon.mu.Unlock()

	// Audit
	auditCmd := fmt.Sprintf("%s serve audit --action upload --detail %s",
		h.daemon.remotePath, shellEscape(fmt.Sprintf("path=%s size=%d", remotePath, len(data))))
	h.daemon.runner.Run(auditCmd)

	// Write via stdin pipe (handles binary data correctly)
	cmd := fmt.Sprintf("cat > %s", shellEscape(remotePath))
	_, stderr, exitCode, err := h.daemon.runner.RunStdin(cmd, data)
	if err != nil {
		return errResponse(fmt.Errorf("upload failed: %w", err))
	}
	if exitCode != 0 {
		return errResponse(fmt.Errorf("upload to %s: %s", remotePath, strings.TrimSpace(string(stderr))))
	}

	return okResponse(protocol.WriteResult{BytesWritten: int64(len(data))})
}

func (h *Handler) handleDownload(params map[string]any) *protocol.DaemonResponse {
	remotePath, _ := params["remote_path"].(string)
	localPath, _ := params["local_path"].(string)
	if remotePath == "" || localPath == "" {
		return errResponse(fmt.Errorf("missing 'remote_path' or 'local_path' parameter"))
	}

	h.daemon.mu.Lock()
	defer h.daemon.mu.Unlock()

	// Read remote file via cat
	cmd := fmt.Sprintf("cat %s", shellEscape(remotePath))
	stdout, stderr, exitCode, err := h.daemon.runner.Run(cmd)
	if err != nil {
		return errResponse(fmt.Errorf("download failed: %w", err))
	}
	if exitCode != 0 {
		return errResponse(fmt.Errorf("download %s: %s", remotePath, strings.TrimSpace(string(stderr))))
	}

	// Write to local file
	if err := os.WriteFile(localPath, stdout, 0644); err != nil {
		return errResponse(fmt.Errorf("write local file %s: %w", localPath, err))
	}

	return okResponse(protocol.WriteResult{BytesWritten: int64(len(stdout))})
}

func (h *Handler) handleEdit(params map[string]any) *protocol.DaemonResponse {
	path, _ := params["path"].(string)
	oldText, _ := params["old"].(string)
	newText, _ := params["new"].(string)
	if path == "" || oldText == "" {
		return errResponse(fmt.Errorf("missing 'path' or 'old' parameter"))
	}

	h.daemon.mu.Lock()
	defer h.daemon.mu.Unlock()

	// Use remote helper for atomic edit
	cmd := fmt.Sprintf("%s serve edit --path %s --old %s --new %s",
		h.daemon.remotePath, shellEscape(path), shellEscape(oldText), shellEscape(newText))
	stdout, stderr, exitCode, err := h.daemon.runner.Run(cmd)
	if err != nil {
		return errResponse(fmt.Errorf("edit failed: %w", err))
	}
	if exitCode != 0 {
		return errResponse(fmt.Errorf("edit %s: %s", path, strings.TrimSpace(string(stderr))))
	}

	// First check if it's an error response
	var raw map[string]any
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return errResponse(fmt.Errorf("parse edit result: %w", err))
	}
	if e, ok := raw["error"]; ok {
		return errResponse(fmt.Errorf("%v", e))
	}

	var result protocol.EditResult
	if err := json.Unmarshal(stdout, &result); err != nil {
		return errResponse(fmt.Errorf("parse edit result: %w", err))
	}
	return okResponse(result)
}

func (h *Handler) handleLs(params map[string]any) *protocol.DaemonResponse {
	path, _ := params["path"].(string)
	recursive, _ := params["recursive"].(bool)
	if path == "" {
		path = "."
	}

	h.daemon.mu.Lock()
	defer h.daemon.mu.Unlock()

	var cmd string
	if recursive {
		// Use find for recursive listing; %y gives type (d/f/l), %l gives symlink target
		cmd = fmt.Sprintf("find %s -printf '%%y\\t%%s\\t%%m\\t%%T@\\t%%l\\t%%p\\n' 2>/dev/null", shellEscape(path))
	} else {
		// Use stat with dereferenced info + readlink for symlink targets
		cmd = fmt.Sprintf("stat --format='%%F\\t%%s\\t%%a\\t%%Y\\t%%n' %s/* %s/.* 2>/dev/null || true", shellEscape(path), shellEscape(path))
	}

	stdout, _, _, err := h.daemon.runner.Run(cmd)
	if err != nil {
		return errResponse(fmt.Errorf("ls failed: %w", err))
	}

	var entries []protocol.DirEntry
	if recursive {
		entries = parseFindOutput(string(stdout))
	} else {
		entries = parseStatOutput(string(stdout))
	}
	return okResponse(protocol.DirListing{Path: path, Entries: entries})
}

// parseFindOutput parses output from find with format: type\tsize\tmode\ttime\tlinktarget\tpath
func parseFindOutput(output string) []protocol.DirEntry {
	var entries []protocol.DirEntry
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 6)
		if len(fields) < 6 {
			continue
		}

		typeField := fields[0]
		size := parseInt64(fields[1])
		mode := fields[2]
		modTime := parseInt64(strings.Split(fields[3], ".")[0])
		linkTarget := fields[4]
		name := fields[5]

		baseName := name
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			baseName = name[idx+1:]
		}
		if baseName == "." || baseName == ".." {
			continue
		}

		entry := protocol.DirEntry{
			Name:    name,
			Size:    size,
			Mode:    mode,
			IsDir:   typeField == "d",
			ModTime: modTime,
		}
		if typeField == "l" {
			entry.IsLink = true
			entry.Target = linkTarget
		}
		entries = append(entries, entry)
	}
	return entries
}

// parseStatOutput parses output from stat with format: type\tsize\tmode\ttime\tpath
func parseStatOutput(output string) []protocol.DirEntry {
	var entries []protocol.DirEntry
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 5)
		if len(fields) < 5 {
			continue
		}

		typeField := fields[0]
		size := parseInt64(fields[1])
		mode := fields[2]
		modTime := parseInt64(strings.Split(fields[3], ".")[0])
		name := fields[4]

		baseName := name
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			baseName = name[idx+1:]
		}
		if baseName == "." || baseName == ".." {
			continue
		}

		entry := protocol.DirEntry{
			Name:    name,
			Size:    size,
			Mode:    mode,
			IsDir:   typeField == "d" || typeField == "directory",
			ModTime: modTime,
		}
		if typeField == "symbolic link" {
			entry.IsLink = true
		}
		entries = append(entries, entry)
	}
	return entries
}

func (h *Handler) handleReadlink(params map[string]any) *protocol.DaemonResponse {
	path, _ := params["path"].(string)
	if path == "" {
		return errResponse(fmt.Errorf("missing 'path' parameter"))
	}

	h.daemon.mu.Lock()
	defer h.daemon.mu.Unlock()

	cmd := fmt.Sprintf("readlink -f %s", shellEscape(path))
	stdout, stderr, exitCode, err := h.daemon.runner.Run(cmd)
	if err != nil {
		return errResponse(fmt.Errorf("readlink failed: %w", err))
	}
	if exitCode != 0 {
		return errResponse(fmt.Errorf("readlink %s: %s", path, strings.TrimSpace(string(stderr))))
	}

	return okResponse(protocol.ReadlinkResult{
		Path:   path,
		Target: strings.TrimSpace(string(stdout)),
	})
}

func (h *Handler) handlePs(params map[string]any) *protocol.DaemonResponse {
	filter, _ := params["filter"].(string)

	h.daemon.mu.Lock()
	defer h.daemon.mu.Unlock()

	// Use remote helper for structured output
	cmd := h.daemon.remotePath + " serve ps"
	if filter != "" {
		cmd += " --filter " + shellEscape(filter)
	}
	stdout, stderr, exitCode, err := h.daemon.runner.Run(cmd)
	if err != nil {
		return errResponse(fmt.Errorf("ps failed: %w", err))
	}
	if exitCode != 0 {
		return errResponse(fmt.Errorf("ps: %s", strings.TrimSpace(string(stderr))))
	}

	var result protocol.ProcessList
	if err := json.Unmarshal(stdout, &result); err != nil {
		return errResponse(fmt.Errorf("parse ps result: %w", err))
	}
	return okResponse(result)
}

func (h *Handler) handleSysinfo() *protocol.DaemonResponse {
	h.daemon.mu.Lock()
	defer h.daemon.mu.Unlock()

	// Use remote helper
	cmd := h.daemon.remotePath + " serve sysinfo"
	stdout, stderr, exitCode, err := h.daemon.runner.Run(cmd)
	if err != nil {
		return errResponse(fmt.Errorf("sysinfo failed: %w", err))
	}
	if exitCode != 0 {
		return errResponse(fmt.Errorf("sysinfo: %s", strings.TrimSpace(string(stderr))))
	}

	var result protocol.SystemInfo
	if err := json.Unmarshal(stdout, &result); err != nil {
		return errResponse(fmt.Errorf("parse sysinfo result: %w", err))
	}
	return okResponse(result)
}

// exitFunc can be overridden in tests to prevent os.Exit during testing.
var exitFunc = os.Exit

func (h *Handler) handleDisconnect() *protocol.DaemonResponse {
	go func() {
		h.daemon.shutdown()
		exitFunc(0)
	}()
	return okResponse(map[string]string{"status": "disconnecting"})
}

func parseInt64(s string) int64 {
	var result int64
	fmt.Sscanf(s, "%d", &result)
	return result
}
