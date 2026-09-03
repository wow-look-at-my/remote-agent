package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// globTree builds a small tree and returns its root.
func globTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := []string{
		"main.go",
		"go.mod",
		"README.md",
		"src/app.ts",
		"src/app.tsx",
		"src/deep/nested/util.go",
		".git/config",
		"node_modules/pkg/index.js",
	}
	for _, f := range files {
		path := filepath.Join(root, filepath.FromSlash(f))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte("x\n"), 0644))
	}
	return root
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "src/deep/main.go", true}, // slashless patterns match the base name at any depth
		{"*.go", "main.rs", false},
		{"**/*.go", "main.go", true},
		{"**/*.go", "a/b/main.go", true},
		{"src/*.ts", "src/app.ts", true},
		{"src/*.ts", "src/deep/app.ts", false},
		{"src/**/*.go", "src/deep/nested/util.go", true},
		{"src/**", "src/app.ts", true},
		{"?.go", "a.go", true},
		{"?.go", "ab.go", false},
		{"[ab].go", "a.go", true},
		{"[ab].go", "c.go", false},
		{"", "a.go", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, matchGlob(c.pattern, c.name), "matchGlob(%q, %q)", c.pattern, c.name)
	}
}

func TestExpandBraces(t *testing.T) {
	got, err := expandBraces("*.{go,mod}")
	require.NoError(t, err)
	assert.Equal(t, []string{"*.go", "*.mod"}, got)

	got, err = expandBraces("src/{a,{b,c}}/x")
	require.NoError(t, err)
	assert.Equal(t, []string{"src/a/x", "src/b/x", "src/c/x"}, got)

	got, err = expandBraces("plain/*.go")
	require.NoError(t, err)
	assert.Equal(t, []string{"plain/*.go"}, got)

	_, err = expandBraces("*.{go")
	assert.Error(t, err)

	_, err = expandBraces("*.go}")
	assert.Error(t, err)
}

func TestGlobFiles(t *testing.T) {
	root := globTree(t)

	result, err := GlobFiles(GlobOptions{Pattern: "**/*.go", Path: root})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		filepath.Join(root, "main.go"),
		filepath.Join(root, "src", "deep", "nested", "util.go"),
	}, result.Files)
	assert.False(t, result.Truncated)
}

func TestGlobFilesBraceAlternatives(t *testing.T) {
	root := globTree(t)

	result, err := GlobFiles(GlobOptions{Pattern: "src/*.{ts,tsx}", Path: root})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		filepath.Join(root, "src", "app.ts"),
		filepath.Join(root, "src", "app.tsx"),
	}, result.Files)
}

func TestGlobFilesSkipsGeneratedDirs(t *testing.T) {
	root := globTree(t)

	result, err := GlobFiles(GlobOptions{Pattern: "**/*", Path: root})
	require.NoError(t, err)
	for _, f := range result.Files {
		assert.NotContains(t, f, ".git")
		assert.NotContains(t, f, "node_modules")
	}
}

func TestGlobFilesOrdersByModTimeDescending(t *testing.T) {
	root := t.TempDir()
	for i, name := range []string{"old.txt", "mid.txt", "new.txt"} {
		path := filepath.Join(root, name)
		require.NoError(t, os.WriteFile(path, []byte("x"), 0644))
		stamp := time.Now().Add(time.Duration(i-3) * time.Hour)
		require.NoError(t, os.Chtimes(path, stamp, stamp))
	}

	result, err := GlobFiles(GlobOptions{Pattern: "*.txt", Path: root})
	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.Join(root, "new.txt"),
		filepath.Join(root, "mid.txt"),
		filepath.Join(root, "old.txt"),
	}, result.Files)
}

func TestGlobFilesTruncates(t *testing.T) {
	root := globTree(t)

	result, err := GlobFiles(GlobOptions{Pattern: "**/*", Path: root, Limit: 2})
	require.NoError(t, err)
	assert.Len(t, result.Files, 2)
	assert.True(t, result.Truncated)
}

func TestGlobFilesNoMatches(t *testing.T) {
	root := globTree(t)

	result, err := GlobFiles(GlobOptions{Pattern: "**/*.rs", Path: root})
	require.NoError(t, err)
	assert.Empty(t, result.Files)
	assert.Equal(t, root, result.Path)
}

func TestGlobFilesErrors(t *testing.T) {
	_, err := GlobFiles(GlobOptions{Pattern: "", Path: t.TempDir()})
	assert.Error(t, err)

	_, err = GlobFiles(GlobOptions{Pattern: "*", Path: filepath.Join(t.TempDir(), "missing")})
	assert.Error(t, err)

	// A file (rather than a directory) is not a valid search root.
	file := filepath.Join(t.TempDir(), "f.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0644))
	_, err = GlobFiles(GlobOptions{Pattern: "*", Path: file})
	assert.Error(t, err)

	_, err = GlobFiles(GlobOptions{Pattern: "*.{go", Path: t.TempDir()})
	assert.Error(t, err)
}

func TestGlobFilesDefaultsToWorkingDirectory(t *testing.T) {
	root := globTree(t)
	t.Chdir(root)

	result, err := GlobFiles(GlobOptions{Pattern: "go.mod"})
	require.NoError(t, err)
	assert.Equal(t, ".", result.Path)
	assert.Equal(t, []string{filepath.Join(".", "go.mod")}, result.Files)
}

func TestServeGlob(t *testing.T) {
	root := globTree(t)

	output := captureStdout(t, func() {
		ServeGlob(GlobOptions{Pattern: "*.mod", Path: root})
	})

	var result protocol.GlobResult
	require.NoError(t, json.Unmarshal([]byte(output), &result))
	assert.Equal(t, []string{filepath.Join(root, "go.mod")}, result.Files)
}

func TestServeGlobError(t *testing.T) {
	output := captureStdout(t, func() {
		ServeGlob(GlobOptions{Pattern: "", Path: t.TempDir()})
	})

	var result map[string]string
	require.NoError(t, json.Unmarshal([]byte(output), &result))
	assert.NotEmpty(t, result["error"])
}
