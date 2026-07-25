package agent

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEditFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	result, err := EditFile(path, "hello", "goodbye", false)
	require.Nil(t, err)
	assert.True(t, result.Modified)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "goodbye world", string(data))

}

func TestEditFileNotFound(t *testing.T) {
	_, err := EditFile("/nonexistent/path/file.txt", "a", "b", false)
	assert.NotNil(t, err)

}

func TestEditFileTextNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	_, err := EditFile(path, "nonexistent", "replacement", false)
	assert.NotNil(t, err)

}

func TestEditFilePreservesPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0755)

	EditFile(path, "hello", "goodbye", false)

	fi, _ := os.Stat(path)
	assert.
		// Compare just the permission bits
		Equal(t, fs.FileMode(0755), fi.Mode().Perm())

}

func TestEditFileRejectsAmbiguousMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("aaa"), 0644)

	_, err := EditFile(path, "a", "b", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "3 occurrences")

	// The file is left untouched when the edit is rejected.
	data, _ := os.ReadFile(path)
	assert.Equal(t, "aaa", string(data))
}

func TestEditFileReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("aaa"), 0644)

	result, err := EditFile(path, "a", "b", true)
	require.NoError(t, err)
	assert.Equal(t, 3, result.Replacements)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "bbb", string(data))
}

func TestEditFileIdenticalTextRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	_, err := EditFile(path, "hello", "hello", false)
	assert.Error(t, err)
}

func TestEditFileMultiline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644)

	EditFile(path, "line2\nline3", "replaced", false)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "line1\nreplaced\n", string(data))

}

func TestEditFileEmptyReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("remove me please"), 0644)

	EditFile(path, "remove me ", "", false)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "please", string(data))

}
