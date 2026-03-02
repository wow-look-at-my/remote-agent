package daemon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/wow-look-at-my/remote-agent/protocol"
	"github.com/wow-look-at-my/remote-agent/sshutil"
)

func (h *Handler) handlePing() *protocol.DaemonResponse {
	h.daemon.mu.Lock()
	defer h.daemon.mu.Unlock()

	stdout, _, _, err := sshutil.RunCommand(h.daemon.conn.Client, "echo pong")
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

	h.daemon.mu.Lock()
	defer h.daemon.mu.Unlock()

	// Audit log the command
	auditCmd := fmt.Sprintf("%s serve audit --action exec --detail %s",
		h.daemon.remotePath, shellEscape(command))
	sshutil.RunCommand(h.daemon.conn.Client, auditCmd)

	stdout, stderr, exitCode, err := sshutil.RunCommand(h.daemon.conn.Client, command)
	if err != nil {
		return errResponse(fmt.Errorf("exec failed: %w", err))
	}
	return okResponse(protocol.ExecResult{
		Stdout:   string(stdout),
		Stderr:   string(stderr),
		ExitCode: exitCode,
	})
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
	stdout, stderr, exitCode, err := sshutil.RunCommand(h.daemon.conn.Client, cmd)
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
	sshutil.RunCommand(h.daemon.conn.Client, auditCmd)

	// Write via cat, using base64 to avoid binary issues
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	cmd := fmt.Sprintf("echo %s | base64 -d > %s && chmod %s %s",
		shellEscape(encoded), shellEscape(path), mode, shellEscape(path))
	_, stderr, exitCode, err := sshutil.RunCommand(h.daemon.conn.Client, cmd)
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
	sshutil.RunCommand(h.daemon.conn.Client, auditCmd)

	// Write via stdin pipe (handles binary data correctly)
	cmd := fmt.Sprintf("cat > %s", shellEscape(remotePath))
	_, stderr, exitCode, err := sshutil.RunCommandWithStdin(h.daemon.conn.Client, cmd, data)
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
	stdout, stderr, exitCode, err := sshutil.RunCommand(h.daemon.conn.Client, cmd)
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
	stdout, stderr, exitCode, err := sshutil.RunCommand(h.daemon.conn.Client, cmd)
	if err != nil {
		return errResponse(fmt.Errorf("edit failed: %w", err))
	}
	if exitCode != 0 {
		return errResponse(fmt.Errorf("edit %s: %s", path, strings.TrimSpace(string(stderr))))
	}

	var result protocol.EditResult
	if err := json.Unmarshal(stdout, &result); err != nil {
		// Check if it's an error response
		var errResp map[string]string
		if json.Unmarshal(stdout, &errResp) == nil {
			if e, ok := errResp["error"]; ok {
				return errResponse(fmt.Errorf("%s", e))
			}
		}
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
		// Use find for recursive listing
		cmd = fmt.Sprintf("find %s -printf '%%y\\t%%s\\t%%m\\t%%T@\\t%%p\\n' 2>/dev/null", shellEscape(path))
	} else {
		cmd = fmt.Sprintf("stat --format='%%F\\t%%s\\t%%a\\t%%Y\\t%%n' %s/* %s/.* 2>/dev/null || true", shellEscape(path), shellEscape(path))
	}

	stdout, _, _, err := sshutil.RunCommand(h.daemon.conn.Client, cmd)
	if err != nil {
		return errResponse(fmt.Errorf("ls failed: %w", err))
	}

	var entries []protocol.DirEntry
	for _, line := range strings.Split(strings.TrimSpace(string(stdout)), "\n") {
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
		modTime := parseInt64(strings.Split(fields[3], ".")[0]) // truncate fractional seconds
		name := fields[4]

		// Skip . and .. entries
		baseName := name
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			baseName = name[idx+1:]
		}
		if baseName == "." || baseName == ".." {
			continue
		}

		isDir := typeField == "d" || typeField == "directory"
		entries = append(entries, protocol.DirEntry{
			Name:    name,
			Size:    size,
			Mode:    mode,
			IsDir:   isDir,
			ModTime: modTime,
		})
	}

	return okResponse(protocol.DirListing{Path: path, Entries: entries})
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
	stdout, stderr, exitCode, err := sshutil.RunCommand(h.daemon.conn.Client, cmd)
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
	stdout, stderr, exitCode, err := sshutil.RunCommand(h.daemon.conn.Client, cmd)
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

func (h *Handler) handleDisconnect() *protocol.DaemonResponse {
	go func() {
		h.daemon.shutdown()
		os.Exit(0)
	}()
	return okResponse(map[string]string{"status": "disconnecting"})
}

func parseInt64(s string) int64 {
	var result int64
	fmt.Sscanf(s, "%d", &result)
	return result
}
