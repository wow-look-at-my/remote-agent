package agent

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestEditFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	result, err := EditFile(path, "hello", "goodbye")
	require.Nil(t, err)
	assert.True(t, result.Modified)

	data, _ := os.ReadFile(path)
	assert.Equal(t, "goodbye world", string(data))

}

func TestEditFileNotFound(t *testing.T) {
	_, err := EditFile("/nonexistent/path/file.txt", "a", "b")
	assert.NotNil(t, err)

}

func TestEditFileTextNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	_, err := EditFile(path, "nonexistent", "replacement")
	assert.NotNil(t, err)

}

func TestEditFilePreservesPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0755)

	EditFile(path, "hello", "goodbye")

	fi, _ := os.Stat(path)
	assert.
		// Compare just the permission bits
		Equal(t, 0755, fi.Mode().Perm())

}

func TestEditFileOnlyReplacesFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("aaa"), 0644)

	EditFile(path, "a", "b")

	data, _ := os.ReadFile(path)
	assert.Equal(t, "baa", string(data))

}

func TestEditFileMultiline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644)

	EditFile(path, "line2\nline3", "replaced")

	data, _ := os.ReadFile(path)
	assert.Equal(t, "line1\nreplaced\n", string(data))

}

func TestEditFileEmptyReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("remove me please"), 0644)

	EditFile(path, "remove me ", "")

	data, _ := os.ReadFile(path)
	assert.Equal(t, "please", string(data))

}
