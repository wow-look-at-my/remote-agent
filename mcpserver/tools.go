package mcpserver

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

const (
	// defaultReadLines is how many lines read_file returns when the caller
	// does not ask for a specific window.
	defaultReadLines = 2000
	// maxReadLine caps the length of a single returned line.
	maxReadLine = 2000
	// maxImageBytes is the largest image returned inline; anything bigger is
	// refused rather than blown up into megabytes of base64.
	maxImageBytes = 5 << 20
)

// tool is one MCP tool: the wire-facing declaration plus its handler.
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`

	handler func(args map[string]any) ([]contentBlock, error) `json:"-"`
}

// buildTools declares the toolset. The descriptions matter as much as the
// schemas: with the built-in file tools switched off, these are the only file
// tools the model sees, so each one says plainly that it acts on the remote
// host.
func (s *Server) buildTools() []tool {
	return []tool{
		{
			Name: "read_file",
			Description: "Read a file on the REMOTE host (the same machine Bash commands run on). " +
				"Returns the contents with line numbers, so text can be quoted back to edit_file exactly. " +
				"Images are returned visually. Use this instead of a local file-reading tool -- there is none in this session.",
			InputSchema: schema(
				props{
					"path":   prop("string", "Absolute path on the remote host."),
					"offset": prop("integer", "1-based line number to start from (for reading a window of a large file)."),
					"limit":  prop("integer", fmt.Sprintf("Maximum lines to return (default %d).", defaultReadLines)),
				},
				"path",
			),
			handler: s.readFile,
		},
		{
			Name: "write_file",
			Description: "Write a file on the REMOTE host, creating it or overwriting it entirely. " +
				"Prefer edit_file for changing part of an existing file.",
			InputSchema: schema(
				props{
					"path":    prop("string", "Absolute path on the remote host."),
					"content": prop("string", "Full contents to write."),
					"mode":    prop("string", "Octal file mode (default 0644)."),
				},
				"path", "content",
			),
			handler: s.writeFile,
		},
		{
			Name: "edit_file",
			Description: "Replace exact text in a file on the REMOTE host. " +
				"old_string must appear exactly once unless replace_all is true; include surrounding lines to make it unique. " +
				"Read the file first so the text matches byte for byte.",
			InputSchema: schema(
				props{
					"path":        prop("string", "Absolute path on the remote host."),
					"old_string":  prop("string", "Exact text to replace (must be unique unless replace_all)."),
					"new_string":  prop("string", "Replacement text."),
					"replace_all": prop("boolean", "Replace every occurrence instead of requiring a unique match."),
				},
				"path", "old_string", "new_string",
			),
			handler: s.editFile,
		},
		{
			Name:        "list_dir",
			Description: "List a directory on the REMOTE host.",
			InputSchema: schema(
				props{
					"path":      prop("string", "Directory on the remote host (default the remote home directory)."),
					"recursive": prop("boolean", "Walk the whole tree instead of one level."),
				},
			),
			handler: s.listDir,
		},
		{
			Name: "glob",
			Description: "Find files on the REMOTE host by glob pattern, most recently modified first. " +
				"Supports '**' for any number of directories and brace alternatives, e.g. '**/*.{go,mod}'. " +
				"Use this to locate files by name; use grep to search their contents.",
			InputSchema: schema(
				props{
					"pattern": prop("string", "Glob pattern, matched against paths relative to path."),
					"path":    prop("string", "Directory to search (default the remote working directory)."),
					"limit":   prop("integer", "Maximum paths to return."),
				},
				"pattern",
			),
			handler: s.glob,
		},
		{
			Name: "grep",
			Description: "Search file contents on the REMOTE host with a regular expression (RE2 syntax). " +
				"Binary files, .git and node_modules are skipped.",
			InputSchema: schema(
				props{
					"pattern":          prop("string", "Regular expression to search for."),
					"path":             prop("string", "File or directory to search (default the remote working directory)."),
					"include":          prop("string", "Glob limiting which files are searched, e.g. '**/*.go'."),
					"output_mode":      enumProp("string", "content returns matching lines (default), files_with_matches returns paths, count returns per-file counts.", protocol.GrepModeContent, protocol.GrepModeFiles, protocol.GrepModeCount),
					"case_insensitive": prop("boolean", "Ignore case."),
					"context_lines":    prop("integer", "Lines of context around each match (content mode)."),
					"limit":            prop("integer", "Maximum results to return."),
				},
				"pattern",
			),
			handler: s.grep,
		},
		{
			Name: "upload_file",
			Description: "Copy a file from the LOCAL machine (where Claude Code runs) to the REMOTE host. " +
				"The only way to move a local file across; every other tool here works remote-side only.",
			InputSchema: schema(
				props{
					"local_path":  prop("string", "Path on the local machine."),
					"remote_path": prop("string", "Destination path on the remote host."),
				},
				"local_path", "remote_path",
			),
			handler: s.uploadFile,
		},
		{
			Name:        "download_file",
			Description: "Copy a file from the REMOTE host to the LOCAL machine (where Claude Code runs).",
			InputSchema: schema(
				props{
					"remote_path": prop("string", "Path on the remote host."),
					"local_path":  prop("string", "Destination path on the local machine."),
				},
				"remote_path", "local_path",
			),
			handler: s.downloadFile,
		},
	}
}

func (s *Server) readFile(args map[string]any) ([]contentBlock, error) {
	path, err := requiredString(args, "path")
	if err != nil {
		return nil, err
	}
	offset := intArg(args, "offset")
	limit := intArg(args, "limit")
	if limit <= 0 {
		limit = defaultReadLines
	}

	var info protocol.FileInfo
	if err := s.backend.Call("read", map[string]any{"path": path}, &info); err != nil {
		return nil, err
	}
	data, err := fileBytes(info)
	if err != nil {
		return nil, err
	}

	if mime := imageMIME(path); mime != "" {
		if len(data) > maxImageBytes {
			return nil, fmt.Errorf("image %s is %d bytes, too large to return inline (limit %d)", path, len(data), maxImageBytes)
		}
		return []contentBlock{{Type: "image", Data: base64.StdEncoding.EncodeToString(data), MimeType: mime}}, nil
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%s is a binary file (%d bytes); use download_file to fetch it", path, len(data))
	}
	if len(data) == 0 {
		return text(fmt.Sprintf("%s exists but is empty.", path)), nil
	}
	return text(numberLines(string(data), offset, limit)), nil
}

// numberLines renders a window of a file the way a reader expects to quote it
// back: 1-based line numbers, tab-separated, with an explicit note when the
// window does not cover the whole file.
func numberLines(content string, offset, limit int) string {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if offset < 1 {
		offset = 1
	}
	if offset > len(lines) {
		return fmt.Sprintf("(offset %d is past the end of the file; it has %d lines)", offset, len(lines))
	}

	start := offset - 1
	end := min(start+limit, len(lines))

	var b strings.Builder
	for i := start; i < end; i++ {
		line := lines[i]
		if len(line) > maxReadLine {
			line = strings.ToValidUTF8(line[:maxReadLine], "") + "... [line truncated]"
		}
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, line)
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "\n[showing lines %d-%d of %d; read further with offset=%d]\n", offset, end, len(lines), end+1)
	}
	return b.String()
}

func (s *Server) writeFile(args map[string]any) ([]contentBlock, error) {
	path, err := requiredString(args, "path")
	if err != nil {
		return nil, err
	}
	content, err := requiredString(args, "content")
	if err != nil {
		return nil, err
	}
	params := map[string]any{"path": path, "content": content}
	if mode := stringArg(args, "mode"); mode != "" {
		params["mode"] = mode
	}

	var result protocol.WriteResult
	if err := s.backend.Call("write", params, &result); err != nil {
		return nil, err
	}
	return text(fmt.Sprintf("Wrote %d bytes to %s on the remote host.", result.BytesWritten, path)), nil
}

func (s *Server) editFile(args map[string]any) ([]contentBlock, error) {
	path, err := requiredString(args, "path")
	if err != nil {
		return nil, err
	}
	oldString, err := requiredString(args, "old_string")
	if err != nil {
		return nil, err
	}
	newString, ok := args["new_string"].(string)
	if !ok {
		return nil, fmt.Errorf("missing required argument: new_string")
	}

	var result protocol.EditResult
	err = s.backend.Call("edit", map[string]any{
		"path":        path,
		"old":         oldString,
		"new":         newString,
		"replace_all": boolArg(args, "replace_all"),
	}, &result)
	if err != nil {
		return nil, err
	}
	replacements := max(result.Replacements, 1)
	return text(fmt.Sprintf("Replaced %d occurrence(s) in %s on the remote host.", replacements, path)), nil
}

func (s *Server) listDir(args map[string]any) ([]contentBlock, error) {
	params := map[string]any{"recursive": boolArg(args, "recursive")}
	if path := stringArg(args, "path"); path != "" {
		params["path"] = path
	}

	var listing protocol.DirListing
	if err := s.backend.Call("ls", params, &listing); err != nil {
		return nil, err
	}
	if len(listing.Entries) == 0 {
		return text(fmt.Sprintf("%s is empty.", listing.Path)), nil
	}

	var b strings.Builder
	for _, e := range listing.Entries {
		switch {
		case e.IsLink:
			fmt.Fprintf(&b, "l %s -> %s\n", e.Name, e.Target)
		case e.IsDir:
			fmt.Fprintf(&b, "d %s\n", e.Name)
		default:
			fmt.Fprintf(&b, "f %s (%d bytes)\n", e.Name, e.Size)
		}
	}
	return text(b.String()), nil
}

func (s *Server) glob(args map[string]any) ([]contentBlock, error) {
	pattern, err := requiredString(args, "pattern")
	if err != nil {
		return nil, err
	}
	params := map[string]any{"pattern": pattern, "limit": intArg(args, "limit")}
	if path := stringArg(args, "path"); path != "" {
		params["path"] = path
	}

	var result protocol.GlobResult
	if err := s.backend.Call("glob", params, &result); err != nil {
		return nil, err
	}
	if len(result.Files) == 0 {
		return text(fmt.Sprintf("No files matching %q under %s.", pattern, result.Path)), nil
	}
	out := strings.Join(result.Files, "\n")
	if result.Truncated {
		out += "\n\n[truncated: more files matched; narrow the pattern or raise limit]"
	}
	return text(out), nil
}

func (s *Server) grep(args map[string]any) ([]contentBlock, error) {
	pattern, err := requiredString(args, "pattern")
	if err != nil {
		return nil, err
	}
	params := map[string]any{
		"pattern":     pattern,
		"include":     stringArg(args, "include"),
		"mode":        stringArg(args, "output_mode"),
		"ignore_case": boolArg(args, "case_insensitive"),
		"context":     intArg(args, "context_lines"),
		"limit":       intArg(args, "limit"),
	}
	if path := stringArg(args, "path"); path != "" {
		params["path"] = path
	}

	var result protocol.GrepResult
	if err := s.backend.Call("grep", params, &result); err != nil {
		return nil, err
	}
	return text(formatGrep(&result)), nil
}

// formatGrep renders a grep result in the familiar grep layout: "path:line:text"
// for matches and "path-line-text" for context lines.
func formatGrep(result *protocol.GrepResult) string {
	var b strings.Builder
	switch result.Mode {
	case protocol.GrepModeFiles:
		for _, f := range result.Files {
			b.WriteString(f + "\n")
		}
	case protocol.GrepModeCount:
		for _, c := range result.Counts {
			fmt.Fprintf(&b, "%s:%d\n", c.Path, c.Count)
		}
	default:
		for _, m := range result.Matches {
			sep := ":"
			if m.IsContext {
				sep = "-"
			}
			fmt.Fprintf(&b, "%s%s%d%s%s\n", m.Path, sep, m.Line, sep, m.Text)
		}
	}
	if b.Len() == 0 {
		return fmt.Sprintf("No matches for %q (searched %d files).", result.Pattern, result.FilesScanned)
	}
	if result.Truncated {
		b.WriteString("\n[truncated: more matches exist; narrow the search or raise limit]\n")
	}
	return b.String()
}

func (s *Server) uploadFile(args map[string]any) ([]contentBlock, error) {
	localPath, err := requiredString(args, "local_path")
	if err != nil {
		return nil, err
	}
	remotePath, err := requiredString(args, "remote_path")
	if err != nil {
		return nil, err
	}

	var result protocol.WriteResult
	err = s.backend.Call("upload", map[string]any{"local_path": localPath, "remote_path": remotePath}, &result)
	if err != nil {
		return nil, err
	}
	return text(fmt.Sprintf("Uploaded %d bytes to %s on the remote host.", result.BytesWritten, remotePath)), nil
}

func (s *Server) downloadFile(args map[string]any) ([]contentBlock, error) {
	remotePath, err := requiredString(args, "remote_path")
	if err != nil {
		return nil, err
	}
	localPath, err := requiredString(args, "local_path")
	if err != nil {
		return nil, err
	}

	var result protocol.WriteResult
	err = s.backend.Call("download", map[string]any{"remote_path": remotePath, "local_path": localPath}, &result)
	if err != nil {
		return nil, err
	}
	return text(fmt.Sprintf("Downloaded %d bytes to %s on the local machine.", result.BytesWritten, localPath)), nil
}

// fileBytes returns the file content from a read response, decoding the
// base64 framing used for bytes that JSON strings cannot carry.
func fileBytes(info protocol.FileInfo) ([]byte, error) {
	if info.ContentB64 != "" {
		data, err := base64.StdEncoding.DecodeString(info.ContentB64)
		if err != nil {
			return nil, fmt.Errorf("decode file content: %w", err)
		}
		return data, nil
	}
	return []byte(info.Content), nil
}

// imageMIME returns the MIME type for image extensions that can be returned
// inline, or "" for anything else.
func imageMIME(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

func text(s string) []contentBlock {
	return []contentBlock{{Type: "text", Text: s}}
}

// Argument helpers. MCP arguments arrive as decoded JSON, so numbers are
// float64 and anything may be missing.

func requiredString(args map[string]any, key string) (string, error) {
	v, _ := args[key].(string)
	if v == "" {
		return "", fmt.Errorf("missing required argument: %s", key)
	}
	return v, nil
}

func stringArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func boolArg(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

func intArg(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

// Schema helpers keep the tool declarations readable.

type props map[string]any

func schema(p props, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": map[string]any(p)}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func prop(typ, description string) map[string]any {
	return map[string]any{"type": typ, "description": description}
}

func enumProp(typ, description string, values ...string) map[string]any {
	p := prop(typ, description)
	p["enum"] = values
	return p
}
