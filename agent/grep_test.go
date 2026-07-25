package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// grepTree builds a tree with text, binary and generated files.
func grepTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	}
	write("main.go", "package main\n\nfunc main() {\n\tneedle()\n}\n")
	write("other.go", "package main\n\nfunc needle() {}\nfunc needle2() {}\n")
	write("notes.md", "no match here\n")
	write("NEEDLE.md", "NEEDLE in caps\n")
	write(".git/config", "needle\n")
	write("bin/blob.bin", "prefix\x00needle\n")
	return root
}

func TestGrepFilesContent(t *testing.T) {
	root := grepTree(t)

	result, err := GrepFiles(GrepOptions{Pattern: "needle\\(\\)", Path: root})
	require.NoError(t, err)
	assert.Equal(t, protocol.GrepModeContent, result.Mode)
	require.Len(t, result.Matches, 2)
	for _, m := range result.Matches {
		assert.Contains(t, m.Text, "needle()")
		assert.NotZero(t, m.Line)
	}
}

func TestGrepFilesSkipsBinaryAndGeneratedDirs(t *testing.T) {
	root := grepTree(t)

	result, err := GrepFiles(GrepOptions{Pattern: "needle", Path: root, Mode: protocol.GrepModeFiles})
	require.NoError(t, err)
	for _, f := range result.Files {
		assert.NotContains(t, f, ".git")
		assert.NotContains(t, f, "blob.bin")
	}
	assert.NotEmpty(t, result.Files)
}

func TestGrepFilesCaseInsensitive(t *testing.T) {
	root := grepTree(t)

	sensitive, err := GrepFiles(GrepOptions{Pattern: "NEEDLE", Path: root, Mode: protocol.GrepModeFiles})
	require.NoError(t, err)
	assert.Len(t, sensitive.Files, 1)

	insensitive, err := GrepFiles(GrepOptions{Pattern: "NEEDLE", Path: root, Mode: protocol.GrepModeFiles, CaseInsensitive: true})
	require.NoError(t, err)
	assert.Greater(t, len(insensitive.Files), len(sensitive.Files))
}

func TestGrepFilesInclude(t *testing.T) {
	root := grepTree(t)

	result, err := GrepFiles(GrepOptions{Pattern: "needle", Path: root, Include: "**/*.md", Mode: protocol.GrepModeFiles, CaseInsensitive: true})
	require.NoError(t, err)
	require.Len(t, result.Files, 1)
	assert.True(t, strings.HasSuffix(result.Files[0], "NEEDLE.md"))
}

func TestGrepFilesCountMode(t *testing.T) {
	root := grepTree(t)

	result, err := GrepFiles(GrepOptions{Pattern: "needle", Path: root, Mode: protocol.GrepModeCount})
	require.NoError(t, err)
	require.NotEmpty(t, result.Counts)
	for _, c := range result.Counts {
		if strings.HasSuffix(c.Path, "other.go") {
			assert.Equal(t, 2, c.Count)
		}
	}
}

func TestGrepFilesContextLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(path, []byte("a\nb\nMATCH\nc\nd\n"), 0644))

	result, err := GrepFiles(GrepOptions{Pattern: "MATCH", Path: path, ContextLines: 1})
	require.NoError(t, err)
	require.Len(t, result.Matches, 3)
	assert.True(t, result.Matches[0].IsContext)
	assert.Equal(t, 2, result.Matches[0].Line)
	assert.False(t, result.Matches[1].IsContext)
	assert.Equal(t, 3, result.Matches[1].Line)
	assert.True(t, result.Matches[2].IsContext)
	assert.Equal(t, 4, result.Matches[2].Line)
}

func TestGrepFilesContextNotDuplicatedBetweenNearbyMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(path, []byte("MATCH\nmiddle\nMATCH\n"), 0644))

	result, err := GrepFiles(GrepOptions{Pattern: "MATCH", Path: path, ContextLines: 1})
	require.NoError(t, err)
	// Lines 1, 2 (context, emitted once) and 3.
	require.Len(t, result.Matches, 3)
	lines := []int{result.Matches[0].Line, result.Matches[1].Line, result.Matches[2].Line}
	assert.Equal(t, []int{1, 2, 3}, lines)
}

func TestGrepFilesSingleFileIgnoresInclude(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(path, []byte("needle\n"), 0644))

	result, err := GrepFiles(GrepOptions{Pattern: "needle", Path: path, Include: "**/*.go"})
	require.NoError(t, err)
	assert.Len(t, result.Matches, 1)
	assert.Equal(t, 1, result.FilesScanned)
}

func TestGrepFilesTruncates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("hit\n", 10)), 0644))

	result, err := GrepFiles(GrepOptions{Pattern: "hit", Path: path, Limit: 3})
	require.NoError(t, err)
	assert.Len(t, result.Matches, 3)
	assert.True(t, result.Truncated)
}

func TestGrepFilesTruncatesLongLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "min.js")
	require.NoError(t, os.WriteFile(path, []byte("hit"+strings.Repeat("x", maxGrepLine+100)), 0644))

	result, err := GrepFiles(GrepOptions{Pattern: "hit", Path: path})
	require.NoError(t, err)
	require.Len(t, result.Matches, 1)
	assert.Contains(t, result.Matches[0].Text, "[truncated]")
	assert.Less(t, len(result.Matches[0].Text), maxGrepLine+50)
}

func TestGrepFilesNoMatches(t *testing.T) {
	root := grepTree(t)

	result, err := GrepFiles(GrepOptions{Pattern: "definitely-not-present", Path: root})
	require.NoError(t, err)
	assert.Empty(t, result.Matches)
	assert.Greater(t, result.FilesScanned, 0)
}

func TestGrepFilesErrors(t *testing.T) {
	root := grepTree(t)

	_, err := GrepFiles(GrepOptions{Pattern: "", Path: root})
	assert.Error(t, err)

	_, err = GrepFiles(GrepOptions{Pattern: "x", Path: root, Mode: "bogus"})
	assert.Error(t, err)

	_, err = GrepFiles(GrepOptions{Pattern: "(unclosed", Path: root})
	assert.Error(t, err)

	_, err = GrepFiles(GrepOptions{Pattern: "x", Path: filepath.Join(root, "missing")})
	assert.Error(t, err)

	_, err = GrepFiles(GrepOptions{Pattern: "x", Path: root, Include: "*.{go"})
	assert.Error(t, err)
}

func TestGrepFilesDefaultsToWorkingDirectory(t *testing.T) {
	root := grepTree(t)
	t.Chdir(root)

	result, err := GrepFiles(GrepOptions{Pattern: "package main", Mode: protocol.GrepModeFiles})
	require.NoError(t, err)
	assert.NotEmpty(t, result.Files)
}

func TestServeGrep(t *testing.T) {
	root := grepTree(t)

	output := captureStdout(t, func() {
		ServeGrep(GrepOptions{Pattern: "needle", Path: root, Mode: protocol.GrepModeFiles})
	})

	var result protocol.GrepResult
	require.NoError(t, json.Unmarshal([]byte(output), &result))
	assert.NotEmpty(t, result.Files)
}

func TestServeGrepError(t *testing.T) {
	output := captureStdout(t, func() {
		ServeGrep(GrepOptions{Pattern: "(unclosed", Path: t.TempDir()})
	})

	var result map[string]string
	require.NoError(t, json.Unmarshal([]byte(output), &result))
	assert.NotEmpty(t, result["error"])
}
