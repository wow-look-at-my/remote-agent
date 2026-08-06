package mcpserver

import (
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

// The tool handlers: each turns one call into a daemon request against the
// target the call named, then renders the reply for a model to read. The
// declarations they are attached to live in tools.go.

func (s *Server) readFile(args map[string]any) ([]contentBlock, error) {
	target, err := s.target(args)
	if err != nil {
		return nil, err
	}
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
	if err := s.backend.Call(target, "read", map[string]any{"path": path}, &info); err != nil {
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
	target, err := s.target(args)
	if err != nil {
		return nil, err
	}
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
	if err := s.backend.Call(target, "write", params, &result); err != nil {
		return nil, err
	}
	return text(fmt.Sprintf("Wrote %d bytes to %s on %s.", result.BytesWritten, path, target)), nil
}

func (s *Server) editFile(args map[string]any) ([]contentBlock, error) {
	target, err := s.target(args)
	if err != nil {
		return nil, err
	}
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
	err = s.backend.Call(target, "edit", map[string]any{
		"path":        path,
		"old":         oldString,
		"new":         newString,
		"replace_all": boolArg(args, "replace_all"),
	}, &result)
	if err != nil {
		return nil, err
	}
	replacements := max(result.Replacements, 1)
	return text(fmt.Sprintf("Replaced %d occurrence(s) in %s on %s.", replacements, path, target)), nil
}

func (s *Server) listDir(args map[string]any) ([]contentBlock, error) {
	target, err := s.target(args)
	if err != nil {
		return nil, err
	}
	params := map[string]any{"recursive": boolArg(args, "recursive")}
	if path := stringArg(args, "path"); path != "" {
		params["path"] = path
	}

	var listing protocol.DirListing
	if err := s.backend.Call(target, "ls", params, &listing); err != nil {
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
	target, err := s.target(args)
	if err != nil {
		return nil, err
	}
	pattern, err := requiredString(args, "pattern")
	if err != nil {
		return nil, err
	}
	params := map[string]any{"pattern": pattern, "limit": intArg(args, "limit")}
	if path := stringArg(args, "path"); path != "" {
		params["path"] = path
	}

	var result protocol.GlobResult
	if err := s.backend.Call(target, "glob", params, &result); err != nil {
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
	target, err := s.target(args)
	if err != nil {
		return nil, err
	}
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
	if err := s.backend.Call(target, "grep", params, &result); err != nil {
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
	target, err := s.target(args)
	if err != nil {
		return nil, err
	}
	localPath, err := requiredString(args, "local_path")
	if err != nil {
		return nil, err
	}
	remotePath, err := requiredString(args, "remote_path")
	if err != nil {
		return nil, err
	}

	var result protocol.WriteResult
	err = s.backend.Call(target, "upload", map[string]any{"local_path": localPath, "remote_path": remotePath}, &result)
	if err != nil {
		return nil, err
	}
	return text(fmt.Sprintf("Uploaded %d bytes to %s on %s.", result.BytesWritten, remotePath, target)), nil
}

func (s *Server) downloadFile(args map[string]any) ([]contentBlock, error) {
	target, err := s.target(args)
	if err != nil {
		return nil, err
	}
	remotePath, err := requiredString(args, "remote_path")
	if err != nil {
		return nil, err
	}
	localPath, err := requiredString(args, "local_path")
	if err != nil {
		return nil, err
	}

	var result protocol.WriteResult
	err = s.backend.Call(target, "download", map[string]any{"remote_path": remotePath, "local_path": localPath}, &result)
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
