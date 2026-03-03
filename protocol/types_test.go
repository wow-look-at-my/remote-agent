package protocol

import (
	"encoding/json"
	"testing"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestDaemonRequestJSON(t *testing.T) {
	req := DaemonRequest{
		Action:	"exec",
		Params:	map[string]any{"command": "ls -la"},
	}
	data, err := json.Marshal(req)
	require.Nil(t, err)

	var decoded DaemonRequest
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "exec", decoded.Action)

	cmd, ok := decoded.Params["command"].(string)
	assert.False(t, !ok || cmd != "ls -la")

}

func TestDaemonResponseJSON(t *testing.T) {
	resp := DaemonResponse{
		OK:	true,
		Data:	ExecResult{Stdout: "hello", Stderr: "", ExitCode: 0},
	}
	data, err := json.Marshal(resp)
	require.Nil(t, err)

	var decoded DaemonResponse
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.True(t, decoded.OK)

}

func TestDaemonResponseErrorJSON(t *testing.T) {
	resp := DaemonResponse{Error: "something went wrong"}
	data, err := json.Marshal(resp)
	require.Nil(t, err)

	var decoded DaemonResponse
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.False(t, decoded.OK)
	assert.Equal(t, "something went wrong", decoded.Error)

}

func TestExecResultJSON(t *testing.T) {
	r := ExecResult{Stdout: "out\n", Stderr: "err\n", ExitCode: 1}
	data, err := json.Marshal(r)
	require.Nil(t, err)

	var decoded ExecResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.False(t, decoded.Stdout != "out\n" || decoded.Stderr != "err\n" || decoded.ExitCode != 1)

}

func TestFileInfoJSON(t *testing.T) {
	f := FileInfo{Content: "hello", Size: 5}
	data, err := json.Marshal(f)
	require.Nil(t, err)

	var decoded FileInfo
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.False(t, decoded.Content != "hello" || decoded.Size != 5)

}

func TestWriteResultJSON(t *testing.T) {
	w := WriteResult{BytesWritten: 1024}
	data, err := json.Marshal(w)
	require.Nil(t, err)

	var decoded WriteResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, int64(1024), decoded.BytesWritten)

}

func TestEditResultJSON(t *testing.T) {
	e := EditResult{Modified: true, Message: "done"}
	data, err := json.Marshal(e)
	require.Nil(t, err)

	var decoded EditResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.False(t, !decoded.Modified || decoded.Message != "done")

}

func TestDirEntryJSON(t *testing.T) {
	e := DirEntry{Name: "test.go", Size: 100, Mode: "0644", IsDir: false, ModTime: 1709300000}
	data, err := json.Marshal(e)
	require.Nil(t, err)

	var decoded DirEntry
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.False(t, decoded.Name != "test.go" || decoded.IsDir || decoded.Size != 100)

}

func TestDirListingJSON(t *testing.T) {
	dl := DirListing{
		Path:	"/tmp",
		Entries: []DirEntry{
			{Name: "a", IsDir: true},
			{Name: "b.txt", Size: 42},
		},
	}
	data, err := json.Marshal(dl)
	require.Nil(t, err)

	var decoded DirListing
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.False(t, decoded.Path != "/tmp" || len(decoded.Entries) != 2)

}

func TestProcessInfoJSON(t *testing.T) {
	p := ProcessInfo{PID: 1, PPID: 0, User: "root", State: "S", RSS: 4096, Command: "init"}
	data, err := json.Marshal(p)
	require.Nil(t, err)

	var decoded ProcessInfo
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.False(t, decoded.PID != 1 || decoded.User != "root" || decoded.Command != "init")

}

func TestSystemInfoJSON(t *testing.T) {
	si := SystemInfo{
		Hostname:	"test",
		OS:		"Ubuntu 24.04",
		Arch:		"amd64",
		Uptime:		"1d 2h",
		CPU:		CPUInfo{Model: "Intel", Cores: 4, Threads: 8, MHz: 2100},
		Memory:		MemoryInfo{TotalBytes: 1024 * 1024 * 1024},
		Disk:		[]DiskInfo{{Device: "/dev/sda1", MountPoint: "/", TotalBytes: 100 * 1024 * 1024 * 1024}},
		Network:	[]NetworkInterface{{Name: "eth0", State: "up", IPv4: []string{"10.0.0.1/24"}}},
		GPU:		[]GPUInfo{},
	}
	data, err := json.Marshal(si)
	require.Nil(t, err)

	var decoded SystemInfo
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.False(t, decoded.Hostname != "test" || decoded.CPU.Model != "Intel" || decoded.CPU.Cores != 4)

}

func TestPingResultJSON(t *testing.T) {
	p := PingResult{Pong: true}
	data, err := json.Marshal(p)
	require.Nil(t, err)

	var decoded PingResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.True(t, decoded.Pong)

}

func TestAuditEntryJSON(t *testing.T) {
	a := AuditEntry{Action: "exec", Detail: "ls", User: "admin", ClientIP: "10.0.0.1", Fingerprint: "SHA256:abc"}
	data, err := json.Marshal(a)
	require.Nil(t, err)

	var decoded AuditEntry
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.False(t, decoded.Action != "exec" || decoded.User != "admin" || decoded.Fingerprint != "SHA256:abc")

}

func TestAuditEntryOmitEmpty(t *testing.T) {
	a := AuditEntry{Action: "ping"}
	data, err := json.Marshal(a)
	require.Nil(t, err)

	s := string(data)
	assert.False(t, contains(s, "detail") || contains(s, "user") || contains(s, "client_ip"))

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
	require.Nil(t, err)
	assert.False(t, contains(string(data), "temp_celsius"))

	g.TempC = 65
	data, err = json.Marshal(g)
	require.Nil(t, err)
	assert.True(t, contains(string(data), "temp_celsius"))

}

func TestReadlinkResultJSON(t *testing.T) {
	r := ReadlinkResult{Path: "/usr/bin/python", Target: "/usr/bin/python3.11"}
	data, err := json.Marshal(r)
	require.Nil(t, err)

	var decoded ReadlinkResult
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "/usr/bin/python", decoded.Path)
	assert.Equal(t, "/usr/bin/python3.11", decoded.Target)
}

func TestDirEntrySymlinkJSON(t *testing.T) {
	e := DirEntry{Name: "link", Size: 12, Mode: "777", IsDir: false, IsLink: true, Target: "/real/path", ModTime: 1709300000}
	data, err := json.Marshal(e)
	require.Nil(t, err)

	var decoded DirEntry
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.True(t, decoded.IsLink)
	assert.Equal(t, "/real/path", decoded.Target)
}

func TestDirEntryOmitEmpty(t *testing.T) {
	e := DirEntry{Name: "regular", Size: 100, Mode: "644", IsDir: false}
	data, err := json.Marshal(e)
	require.Nil(t, err)

	s := string(data)
	// is_link and target should be omitted when empty
	assert.False(t, contains(s, "is_link"))
	assert.False(t, contains(s, "target"))
}
