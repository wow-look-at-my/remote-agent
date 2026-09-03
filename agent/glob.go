package agent

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// Paths returned when the caller asks for no limit.
const DefaultGlobLimit = 500

// Machine-generated trees the walk never enters.
var skipDirs = set.Of(".git", "node_modules")

// GlobOptions configures a glob search.
type GlobOptions struct {
	Pattern string // glob pattern, matched against paths relative to Path
	Path    string // directory to search (default ".")
	Limit   int    // maximum paths to return (default DefaultGlobLimit)
}

// Directories are not returned -- only files and symlinks, matching what a
// file-oriented caller expects.
func GlobFiles(opts GlobOptions) (*protocol.GlobResult, error) {
	if opts.Pattern == "" {
		return nil, fmt.Errorf("glob pattern is required")
	}
	root := opts.Path
	if root == "" {
		root = "."
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultGlobLimit
	}
	patterns, err := expandBraces(opts.Pattern)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("glob %s: %w", root, err)
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("glob %s: not a directory", root)
	}

	type hit struct {
		path    string
		modTime int64
	}
	var hits []hit

	// Walk errors on individual entries (unreadable directories) are skipped
	// rather than aborting: a partial listing is far more useful than none.
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != root && skipDirs.Contains(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !matchAny(patterns, rel) {
			return nil
		}
		var modTime int64
		if info, ierr := d.Info(); ierr == nil {
			modTime = info.ModTime().Unix()
		}
		hits = append(hits, hit{path: p, modTime: modTime})
		return nil
	})

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].modTime != hits[j].modTime {
			return hits[i].modTime > hits[j].modTime
		}
		return hits[i].path < hits[j].path
	})

	result := &protocol.GlobResult{Pattern: opts.Pattern, Path: root, Files: []string{}}
	if len(hits) > limit {
		hits = hits[:limit]
		result.Truncated = true
	}
	for _, h := range hits {
		result.Files = append(result.Files, h.path)
	}
	return result, nil
}

// matchAny reports whether rel matches any of the alternative patterns.
func matchAny(patterns []string, rel string) bool {
	for _, p := range patterns {
		if matchGlob(p, rel) {
			return true
		}
	}
	return false
}

func matchGlob(pattern, name string) bool {
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "/") {
		return segmentMatch(pattern, path.Base(name))
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

// matchSegments matches pattern segments against path segments, expanding
// "**" to any number of segments.
func matchSegments(pat, seg []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// Trailing "**" matches whatever is left, including nothing.
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(seg); i++ {
				if matchSegments(pat[1:], seg[i:]) {
					return true
				}
			}
			return false
		}
		if len(seg) == 0 {
			return false
		}
		if !segmentMatch(pat[0], seg[0]) {
			return false
		}
		pat, seg = pat[1:], seg[1:]
	}
	return len(seg) == 0
}

// A malformed pattern matches nothing, and never fails the walk.
func segmentMatch(pattern, name string) bool {
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}

// expandBraces expands brace alternatives -- "*.{ts,tsx}" becomes "*.ts" and
// "*.tsx" -- so patterns written in the common shell/ripgrep style work.
// Nested braces expand recursively.
func expandBraces(pattern string) ([]string, error) {
	open := strings.IndexByte(pattern, '{')
	if open < 0 {
		if strings.IndexByte(pattern, '}') >= 0 {
			return nil, fmt.Errorf("unbalanced '}' in pattern %q", pattern)
		}
		return []string{pattern}, nil
	}

	depth := 0
	closeIdx := -1
	for i := open; i < len(pattern); i++ {
		switch pattern[i] {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				closeIdx = i
			}
		}
		if closeIdx >= 0 {
			break
		}
	}
	if closeIdx < 0 {
		return nil, fmt.Errorf("unbalanced '{' in pattern %q", pattern)
	}

	prefix, suffix := pattern[:open], pattern[closeIdx+1:]
	var out []string
	for _, alt := range splitAlternatives(pattern[open+1 : closeIdx]) {
		expanded, err := expandBraces(prefix + alt + suffix)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded...)
	}
	return out, nil
}

func splitAlternatives(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}
