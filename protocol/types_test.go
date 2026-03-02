package protocol

import (
	"encoding/json"
	"testing"
)

func TestDaemonRequestJSON(t *testing.T) {
	req := DaemonRequest{
		Action: "exec",
		Params: map[string]any{"command": "ls -la"},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DaemonRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Action != "exec" {
		t.Errorf("action = %q, want %q", decoded.Action, "exec")
	}
	cmd, ok := decoded.Params["command"].(string)
	if !ok || cmd != "ls -la" {
		t.Errorf("command = %v, want %q", decoded.Params["command"], "ls -la")
	}
}

func TestDaemonResponseJSON(t *testing.T) {
	resp := DaemonResponse{
		OK:   true,
		Data: ExecResult{Stdout: "hello", Stderr: "", ExitCode: 0},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DaemonResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.OK {
		t.Error("ok should be true")
	}
}

func TestDaemonResponseErrorJSON(t *testing.T) {
	resp := DaemonResponse{Error: "something went wrong"}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DaemonResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.OK {
		t.Error("ok should be false")
	}
	if decoded.Error != "something went wrong" {
		t.Errorf("error = %q, want %q", decoded.Error, "something went wrong")
	}
}

func TestExecResultJSON(t *testing.T) {
	r := ExecResult{Stdout: "out\n", Stderr: "err\n", ExitCode: 1}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ExecResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Stdout != "out\n" || decoded.Stderr != "err\n" || decoded.ExitCode != 1 {
		t.Errorf("got %+v", decoded)
	}
}

func TestFileInfoJSON(t *testing.T) {
	f := FileInfo{Content: "hello", Size: 5}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var decoded FileInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Content != "hello" || decoded.Size != 5 {
		t.Errorf("got %+v", decoded)
	}
}

func TestWriteResultJSON(t *testing.T) {
	w := WriteResult{BytesWritten: 1024}
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	var decoded WriteResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.BytesWritten != 1024 {
		t.Errorf("bytes_written = %d, want 1024", decoded.BytesWritten)
	}
}

func TestEditResultJSON(t *testing.T) {
	e := EditResult{Modified: true, Message: "done"}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var decoded EditResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Modified || decoded.Message != "done" {
		t.Errorf("got %+v", decoded)
	}
}

func TestDirEntryJSON(t *testing.T) {
	e := DirEntry{Name: "test.go", Size: 100, Mode: "0644", IsDir: false, ModTime: 1709300000}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DirEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Name != "test.go" || decoded.IsDir || decoded.Size != 100 {
		t.Errorf("got %+v", decoded)
	}
}

func TestDirListingJSON(t *testing.T) {
	dl := DirListing{
		Path: "/tmp",
		Entries: []DirEntry{
			{Name: "a", IsDir: true},
			{Name: "b.txt", Size: 42},
		},
	}
	data, err := json.Marshal(dl)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DirListing
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Path != "/tmp" || len(decoded.Entries) != 2 {
		t.Errorf("got %+v", decoded)
	}
}

func TestProcessInfoJSON(t *testing.T) {
	p := ProcessInfo{PID: 1, PPID: 0, User: "root", State: "S", RSS: 4096, Command: "init"}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ProcessInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.PID != 1 || decoded.User != "root" || decoded.Command != "init" {
		t.Errorf("got %+v", decoded)
	}
}

func TestSystemInfoJSON(t *testing.T) {
	si := SystemInfo{
		Hostname: "test",
		OS:       "Ubuntu 24.04",
		Arch:     "amd64",
		Uptime:   "1d 2h",
		CPU:      CPUInfo{Model: "Intel", Cores: 4, Threads: 8, MHz: 2100},
		Memory:   MemoryInfo{TotalBytes: 1024 * 1024 * 1024},
		Disk:     []DiskInfo{{Device: "/dev/sda1", MountPoint: "/", TotalBytes: 100 * 1024 * 1024 * 1024}},
		Network:  []NetworkInterface{{Name: "eth0", State: "up", IPv4: []string{"10.0.0.1/24"}}},
		GPU:      []GPUInfo{},
	}
	data, err := json.Marshal(si)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SystemInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Hostname != "test" || decoded.CPU.Model != "Intel" || decoded.CPU.Cores != 4 {
		t.Errorf("got %+v", decoded)
	}
}

func TestPingResultJSON(t *testing.T) {
	p := PingResult{Pong: true}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PingResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Pong {
		t.Error("pong should be true")
	}
}

func TestAuditEntryJSON(t *testing.T) {
	a := AuditEntry{Action: "exec", Detail: "ls", User: "admin", ClientIP: "10.0.0.1", Fingerprint: "SHA256:abc"}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var decoded AuditEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Action != "exec" || decoded.User != "admin" || decoded.Fingerprint != "SHA256:abc" {
		t.Errorf("got %+v", decoded)
	}
}

func TestAuditEntryOmitEmpty(t *testing.T) {
	a := AuditEntry{Action: "ping"}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if contains(s, "detail") || contains(s, "user") || contains(s, "client_ip") {
		t.Errorf("omitempty fields should not appear: %s", s)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestGPUInfoTempOmitEmpty(t *testing.T) {
	g := GPUInfo{Name: "RTX 4090", TempC: 0}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if contains(string(data), "temp_celsius") {
		t.Error("temp_celsius should be omitted when 0")
	}
	g.TempC = 65
	data, err = json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(data), "temp_celsius") {
		t.Error("temp_celsius should appear when non-zero")
	}
}
