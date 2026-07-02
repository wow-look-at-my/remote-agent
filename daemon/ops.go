package daemon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

// Runner abstracts SSH command execution for testability.
type Runner interface {
	Run(command string) (stdout, stderr []byte, exitCode int, err error)
	RunStdin(command string, stdin []byte) (stdout, stderr []byte, exitCode int, err error)
}

func (h *Handler) handlePing() *protocol.DaemonResponse {
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

	// Audit log the command (concurrently, on its own SSH channel).
	h.daemon.auditAsync("exec", command)

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

// parseLsCommand checks if a command is a simple "ls [path]" that the native
// ls handler can answer faithfully, returning the path and whether it is
// recursive. Anything the structured handler cannot reproduce falls through
// to real exec (ok=false): unsupported flags, multiple paths, and — crucially
// — paths with characters the shell would transform. The handler quotes the
// path literally, so rewriting "ls *.go" would search for a file literally
// named "*.go", find nothing, and silently print an empty listing.
func parseLsCommand(command string) (path string, recursive bool, ok bool) {
	parts := strings.Fields(command)
	if len(parts) == 0 || parts[0] != "ls" {
		return "", false, false
	}
	path = "."
	seenPath := false
	for _, p := range parts[1:] {
		switch {
		case p == "-R":
			recursive = true
		case strings.HasPrefix(p, "-"):
			// Has flags we don't support natively — let exec handle it
			return "", false, false
		case seenPath || !plainLsPath(p):
			return "", false, false
		default:
			path = p
			seenPath = true
		}
	}
	return path, recursive, true
}

// plainLsPath reports whether p is a literal path with no characters the
// shell (globbing, expansion, quoting, escaping, operators) would transform.
func plainLsPath(p string) bool {
	return !strings.ContainsAny(p, "*?[]{}~$`\"'\\!()<>|&;#")
}

// stripTrailingRedirect removes a trailing "2>&1" from commands since stderr
// is already captured separately by the SSH transport. It leaves the command
// untouched when any other '>' redirect is present: in "cmd > log 2>&1" the
// trailing redirect sends stderr into the log file, and stripping it would
// change what lands in the file.
func stripTrailingRedirect(command string) string {
	trimmed := strings.TrimSpace(command)
	if !strings.HasSuffix(trimmed, "2>&1") {
		return command
	}
	rest := strings.TrimSpace(trimmed[:len(trimmed)-4])
	if strings.Contains(rest, ">") {
		return command
	}
	return rest
}

func (h *Handler) handleRead(params map[string]any) *protocol.DaemonResponse {
	path, _ := params["path"].(string)
	if path == "" {
		return errResponse(fmt.Errorf("missing 'path' parameter"))
	}

	// cat the raw bytes: the SSH channel is binary-safe, so remote base64
	// encoding (33% wire inflation plus an extra remote process) is wasted
	// work, and it kept read from working on remotes without a base64 binary.
	cmd := fmt.Sprintf("cat %s", shellEscape(path))
	stdout, stderr, exitCode, err := h.daemon.runner.Run(cmd)
	if err != nil {
		return errResponse(fmt.Errorf("read failed: %w", err))
	}
	if exitCode != 0 {
		return errResponse(fmt.Errorf("read %s: %s", path, strings.TrimSpace(string(stderr))))
	}

	info := protocol.FileInfo{Size: int64(len(stdout))}
	// JSON strings cannot carry invalid UTF-8 (encoding/json substitutes
	// U+FFFD), so binary content is base64-framed across the socket hop and
	// decoded again by the client. Text stays human-readable in --json.
	if utf8.Valid(stdout) {
		info.Content = string(stdout)
	} else {
		info.ContentB64 = base64.StdEncoding.EncodeToString(stdout)
	}
	return okResponse(info)
}

func (h *Handler) handleWrite(params map[string]any) *protocol.DaemonResponse {
	path, _ := params["path"].(string)
	mode, _ := params["mode"].(string)
	if path == "" {
		return errResponse(fmt.Errorf("missing 'path' parameter"))
	}
	data, err := contentParam(params)
	if err != nil {
		return errResponse(err)
	}
	if mode == "" {
		mode = "0644"
	}
	if !validChmodMode(mode) {
		return errResponse(fmt.Errorf("invalid mode %q: expected an octal mode like 0644", mode))
	}

	// Audit (concurrently, on its own SSH channel)
	h.daemon.auditAsync("write", fmt.Sprintf("path=%s size=%d", path, len(data)))

	// Stream the content over stdin. The SSH channel is binary-safe, and
	// unlike the previous echo-base64-in-the-command-line approach this is
	// immune to the kernel's per-argument size cap (MAX_ARG_STRLEN, 128 KiB),
	// which made every write over ~96 KiB fail with "Argument list too long".
	cmd := fmt.Sprintf("cat > %s && chmod %s %s", shellEscape(path), mode, shellEscape(path))
	_, stderr, exitCode, err := h.daemon.runner.RunStdin(cmd, data)
	if err != nil {
		return errResponse(fmt.Errorf("write failed: %w", err))
	}
	if exitCode != 0 {
		return errResponse(fmt.Errorf("write %s: %s", path, strings.TrimSpace(string(stderr))))
	}

	return okResponse(protocol.WriteResult{BytesWritten: int64(len(data))})
}

// contentParam extracts a write payload from request params. content_b64
// (base64, binary-safe across the JSON socket hop) is preferred; the plain
// content string is the fallback used for valid-UTF-8 payloads.
func contentParam(params map[string]any) ([]byte, error) {
	if b64, ok := params["content_b64"].(string); ok && b64 != "" {
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("decode content_b64: %w", err)
		}
		return data, nil
	}
	content, _ := params["content"].(string)
	return []byte(content), nil
}

// validChmodMode reports whether mode is a plain octal chmod mode (like 644
// or 0755), the only form handleWrite splices into the remote shell command.
func validChmodMode(mode string) bool {
	if len(mode) < 3 || len(mode) > 4 {
		return false
	}
	for _, c := range mode {
		if c < '0' || c > '7' {
			return false
		}
	}
	return true
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

	// Audit (concurrently, on its own SSH channel)
	h.daemon.auditAsync("upload", fmt.Sprintf("path=%s size=%d", remotePath, len(data)))

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

	// Use find for both modes; %y gives type (d/f/l), %l gives symlink target.
	// find is present on BusyBox (and other minimal images) where GNU
	// `stat --format` is not. The non-recursive case just caps depth at 1.
	var cmd string
	if recursive {
		cmd = fmt.Sprintf("find %s -printf '%%y\\t%%s\\t%%m\\t%%T@\\t%%l\\t%%p\\n'", shellEscape(path))
	} else {
		cmd = fmt.Sprintf("find %s -maxdepth 1 -printf '%%y\\t%%s\\t%%m\\t%%T@\\t%%l\\t%%p\\n'", shellEscape(path))
	}

	stdout, stderr, exitCode, err := h.daemon.runner.Run(cmd)
	if err != nil {
		return errResponse(fmt.Errorf("ls failed: %w", err))
	}

	entries := parseFindOutput(string(stdout))
	// find exits non-zero on errors like a missing directory. A partial
	// listing (e.g. permission-denied subtrees during a recursive walk) still
	// returns what was found, but a failure with no entries at all used to be
	// silently reported as an empty directory — surface the error instead.
	if exitCode != 0 && len(entries) == 0 {
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = fmt.Sprintf("find exited %d", exitCode)
		}
		return errResponse(fmt.Errorf("ls %s: %s", path, msg))
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

func (h *Handler) handleReadlink(params map[string]any) *protocol.DaemonResponse {
	path, _ := params["path"].(string)
	if path == "" {
		return errResponse(fmt.Errorf("missing 'path' parameter"))
	}

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
