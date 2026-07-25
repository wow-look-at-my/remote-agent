package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

const (
	// DefaultGrepLimit caps how many results a grep returns when the caller
	// does not ask for a different limit.
	DefaultGrepLimit = 200
	// binarySniffBytes is how much of a file's head is inspected for NUL
	// bytes before deciding it is binary and skipping it.
	binarySniffBytes = 8192
	// maxGrepLine caps how much of a single matching line is returned;
	// minified bundles otherwise produce megabyte-long "lines".
	maxGrepLine = 2000
)

// GrepOptions configures a grep search.
type GrepOptions struct {
	Pattern         string // regular expression (RE2 syntax)
	Path            string // file or directory to search (default ".")
	Include         string // optional glob limiting which files are searched
	CaseInsensitive bool
	Mode            string // protocol.GrepMode* (default content)
	ContextLines    int    // lines of context around each match (content mode)
	Limit           int    // maximum results (default DefaultGrepLimit)
}

// GrepFiles searches a remote file or directory tree for a regular
// expression. Binary files are skipped, as are the generated directories in
// skipDirs, so results stay to source the caller cares about.
func GrepFiles(opts GrepOptions) (*protocol.GrepResult, error) {
	if opts.Pattern == "" {
		return nil, fmt.Errorf("grep pattern is required")
	}
	root := opts.Path
	if root == "" {
		root = "."
	}
	mode := opts.Mode
	if mode == "" {
		mode = protocol.GrepModeContent
	}
	switch mode {
	case protocol.GrepModeContent, protocol.GrepModeFiles, protocol.GrepModeCount:
	default:
		return nil, fmt.Errorf("unknown grep mode %q (want %s, %s or %s)",
			mode, protocol.GrepModeContent, protocol.GrepModeFiles, protocol.GrepModeCount)
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultGrepLimit
	}

	expr := opts.Pattern
	if opts.CaseInsensitive {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern %q: %w", opts.Pattern, err)
	}

	var includes []string
	if opts.Include != "" {
		if includes, err = expandBraces(opts.Include); err != nil {
			return nil, err
		}
	}

	files, err := grepTargets(root, includes)
	if err != nil {
		return nil, err
	}

	result := &protocol.GrepResult{Pattern: opts.Pattern, Mode: mode}
	for _, file := range files {
		matches, count, scanned, err := grepFile(file, re, mode, opts.ContextLines)
		if err != nil || !scanned {
			continue
		}
		result.FilesScanned++
		if count == 0 {
			continue
		}
		switch mode {
		case protocol.GrepModeFiles:
			if len(result.Files) >= limit {
				result.Truncated = true
				return result, nil
			}
			result.Files = append(result.Files, file)
		case protocol.GrepModeCount:
			if len(result.Counts) >= limit {
				result.Truncated = true
				return result, nil
			}
			result.Counts = append(result.Counts, protocol.GrepFileCount{Path: file, Count: count})
		default:
			for _, m := range matches {
				if len(result.Matches) >= limit {
					result.Truncated = true
					return result, nil
				}
				result.Matches = append(result.Matches, m)
			}
		}
	}
	return result, nil
}

// grepTargets resolves the search root to the list of files to scan. A file
// path searches just that file (the include filter does not apply -- the
// caller named it explicitly); a directory is walked.
func grepTargets(root string, includes []string) ([]string, error) {
	fi, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("grep %s: %w", root, err)
	}
	if !fi.IsDir() {
		return []string{root}, nil
	}

	var files []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != root && skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if len(includes) > 0 {
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				return nil
			}
			if !matchAny(includes, filepath.ToSlash(rel)) {
				return nil
			}
		}
		files = append(files, p)
		return nil
	})
	sort.Strings(files)
	return files, nil
}

// grepFile scans one file. It reports the matches (content mode only), the
// total match count, and whether the file was scanned at all -- an unreadable
// or binary file returns scanned=false so it is not counted.
func grepFile(path string, re *regexp.Regexp, mode string, contextLines int) (matches []protocol.GrepMatch, count int, scanned bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer f.Close()

	head := make([]byte, binarySniffBytes)
	n, _ := f.Read(head)
	if bytes.IndexByte(head[:n], 0) >= 0 {
		return nil, 0, false, nil
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, 0, false, err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	// Ring of preceding lines so "before" context can be emitted once a match
	// is found without holding the whole file in memory.
	before := make([]string, 0, contextLines)
	after := 0
	lineNo := 0
	firstContext := 0

	for scanner.Scan() {
		line := scanner.Text()
		lineNo++
		switch {
		case re.MatchString(line):
			count++
			if mode == protocol.GrepModeContent {
				start := lineNo - len(before)
				for i, b := range before {
					ln := start + i
					if ln > firstContext {
						matches = append(matches, protocol.GrepMatch{Path: path, Line: ln, Text: truncateLine(b), IsContext: true})
					}
				}
				matches = append(matches, protocol.GrepMatch{Path: path, Line: lineNo, Text: truncateLine(line)})
				firstContext = lineNo
			}
			before = before[:0]
			after = contextLines
		case after > 0 && mode == protocol.GrepModeContent:
			matches = append(matches, protocol.GrepMatch{Path: path, Line: lineNo, Text: truncateLine(line), IsContext: true})
			firstContext = lineNo
			after--
		default:
			if contextLines > 0 {
				if len(before) == contextLines {
					before = before[1:]
				}
				before = append(before, line)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		// A line too long for the scanner buffer (or an I/O failure) leaves
		// whatever was found so far usable; the file still counts as scanned.
		return matches, count, true, nil
	}
	return matches, count, true, nil
}

// truncateLine caps a returned line, marking where it was cut.
func truncateLine(s string) string {
	if len(s) <= maxGrepLine {
		return s
	}
	return strings.ToValidUTF8(s[:maxGrepLine], "") + "... [truncated]"
}
