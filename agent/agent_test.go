package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

func TestRunServeNoArgs(t *testing.T) {
	err := RunServe(nil)
	if err == nil {
		t.Error("expected error with no args")
	}
}

func TestRunServeUnknownAction(t *testing.T) {
	err := RunServe([]string{"unknown_action"})
	if err == nil {
		t.Error("expected error for unknown action")
	}
}

func TestRunServeEditMissingFlags(t *testing.T) {
	err := RunServe([]string{"edit"})
	if err == nil {
		t.Error("expected error for edit with no flags")
	}
}

func TestRunServeAuditMissingAction(t *testing.T) {
	err := RunServe([]string{"audit"})
	if err == nil {
		t.Error("expected error for audit with no action")
	}
}

// captureStdout redirects stdout to a temp file and returns the captured output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = f
	fn()
	os.Stdout = old
	f.Seek(0, 0)
	data, _ := os.ReadFile(f.Name())
	return string(data)
}

func TestServeSysinfo(t *testing.T) {
	output := captureStdout(t, func() {
		RunServe([]string{"sysinfo"})
	})

	var info protocol.SystemInfo
	if err := json.Unmarshal([]byte(output), &info); err != nil {
		t.Fatalf("invalid JSON output: %v\noutput: %s", err, output)
	}
	if info.Hostname == "" {
		t.Error("hostname should not be empty")
	}
	if info.CPU.Threads <= 0 {
		t.Error("cpu threads should be > 0")
	}
}

func TestServePs(t *testing.T) {
	output := captureStdout(t, func() {
		RunServe([]string{"ps"})
	})

	var list protocol.ProcessList
	if err := json.Unmarshal([]byte(output), &list); err != nil {
		t.Fatalf("invalid JSON output: %v\noutput: %s", err, output)
	}
	if len(list.Processes) == 0 {
		t.Error("expected at least one process")
	}
}

func TestServePsWithFilter(t *testing.T) {
	output := captureStdout(t, func() {
		RunServe([]string{"ps", "--filter", "__nonexistent__"})
	})

	var list protocol.ProcessList
	if err := json.Unmarshal([]byte(output), &list); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(list.Processes) != 0 {
		t.Error("expected 0 processes with impossible filter")
	}
}

func TestServeEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	output := captureStdout(t, func() {
		RunServe([]string{"edit", "--path", path, "--old", "hello", "--new", "goodbye"})
	})

	var result protocol.EditResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !result.Modified {
		t.Error("should be modified")
	}

	data, _ := os.ReadFile(path)
	if string(data) != "goodbye world" {
		t.Errorf("content = %q", string(data))
	}
}

func TestServeEditNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello"), 0644)

	output := captureStdout(t, func() {
		RunServe([]string{"edit", "--path", path, "--old", "missing", "--new", "x"})
	})

	var result map[string]string
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := result["error"]; !ok {
		t.Error("expected error in output")
	}
}

func TestServeAuditStartup(t *testing.T) {
	output := captureStdout(t, func() {
		RunServe([]string{"audit", "--action", "startup", "--user", "test", "--client-ip", "1.2.3.4", "--fingerprint", "SHA256:abc"})
	})

	var result map[string]string
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["status"] != "logged" {
		t.Errorf("status = %q, want 'logged'", result["status"])
	}
}

func TestServeAuditShutdown(t *testing.T) {
	output := captureStdout(t, func() {
		RunServe([]string{"audit", "--action", "shutdown"})
	})

	var result map[string]string
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["status"] != "logged" {
		t.Errorf("status = %q, want 'logged'", result["status"])
	}
}

func TestServeAuditGeneric(t *testing.T) {
	output := captureStdout(t, func() {
		RunServe([]string{"audit", "--action", "exec", "--detail", "ls -la"})
	})

	var result map[string]string
	json.Unmarshal([]byte(output), &result)
	if result["status"] != "logged" {
		t.Errorf("status = %q, want 'logged'", result["status"])
	}
}

func TestWriteJSON(t *testing.T) {
	output := captureStdout(t, func() {
		writeJSON(map[string]int{"a": 1})
	})
	var result map[string]int
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["a"] != 1 {
		t.Errorf("a = %d, want 1", result["a"])
	}
}

func TestWriteJSONError(t *testing.T) {
	output := captureStdout(t, func() {
		writeJSONError(fmt.Errorf("test error"))
	})
	var result map[string]string
	json.Unmarshal([]byte(output), &result)
	if result["error"] != "test error" {
		t.Errorf("error = %q, want 'test error'", result["error"])
	}
}
