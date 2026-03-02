package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEditFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	result, err := EditFile(path, "hello", "goodbye")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Modified {
		t.Error("should be modified")
	}

	data, _ := os.ReadFile(path)
	if string(data) != "goodbye world" {
		t.Errorf("content = %q, want %q", string(data), "goodbye world")
	}
}

func TestEditFileNotFound(t *testing.T) {
	_, err := EditFile("/nonexistent/path/file.txt", "a", "b")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestEditFileTextNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	_, err := EditFile(path, "nonexistent", "replacement")
	if err == nil {
		t.Error("expected error when text not found")
	}
}

func TestEditFilePreservesPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0755)

	EditFile(path, "hello", "goodbye")

	fi, _ := os.Stat(path)
	// Compare just the permission bits
	if fi.Mode().Perm() != 0755 {
		t.Errorf("mode = %o, want %o", fi.Mode().Perm(), 0755)
	}
}

func TestEditFileOnlyReplacesFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("aaa"), 0644)

	EditFile(path, "a", "b")

	data, _ := os.ReadFile(path)
	if string(data) != "baa" {
		t.Errorf("content = %q, want %q", string(data), "baa")
	}
}

func TestEditFileMultiline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644)

	EditFile(path, "line2\nline3", "replaced")

	data, _ := os.ReadFile(path)
	if string(data) != "line1\nreplaced\n" {
		t.Errorf("content = %q", string(data))
	}
}

func TestEditFileEmptyReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("remove me please"), 0644)

	EditFile(path, "remove me ", "")

	data, _ := os.ReadFile(path)
	if string(data) != "please" {
		t.Errorf("content = %q, want %q", string(data), "please")
	}
}
