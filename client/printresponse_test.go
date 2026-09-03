package client

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

func TestPrintResponseJSON(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = true
	defer func() { OutputJSON = false }()

	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: map[string]any{"stdout": "hello\n", "stderr": "", "exit_code": float64(0)},
	}, "exec")
	assert.Nil(t, err)
}

func TestPrintResponseTextExec(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: map[string]any{"stdout": "hello\n", "stderr": "", "exit_code": float64(0)},
	}, "exec")
	assert.Nil(t, err)
}

func TestPrintResponseTextLs(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK: true,
		Data: map[string]any{
			"path": "/tmp",
			"entries": []any{
				map[string]any{
					"name": "/tmp/dir", "size": float64(4096), "mode": "755",
					"is_dir": true, "is_link": false,
				},
				map[string]any{
					"name": "/tmp/file.txt", "size": float64(100), "mode": "644",
					"is_dir": false, "is_link": false,
				},
			},
		},
	}, "ls")
	assert.Nil(t, err)
}

func TestPrintResponseTextPing(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: map[string]any{"pong": true},
	}, "ping")
	assert.Nil(t, err)
}

func TestPrintResponseTextPingFail(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: map[string]any{"pong": false},
	}, "ping")
	assert.Nil(t, err)
}

func TestPrintResponseTextPs(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK: true,
		Data: map[string]any{
			"processes": []any{
				map[string]any{
					"pid": float64(1), "ppid": float64(0), "user": "root",
					"state": "S", "rss_bytes": float64(4096), "command": "init",
				},
				map[string]any{
					"pid": float64(100), "ppid": float64(1), "user": "user",
					"state": "R", "rss_bytes": float64(8192), "command": "bash",
				},
			},
		},
	}, "ps")
	assert.Nil(t, err)
}

func TestPrintResponseTextPsEmpty(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: map[string]any{},
	}, "ps")
	assert.Nil(t, err)
}

func TestPrintResponseTextSysinfo(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK: true,
		Data: map[string]any{
			"hostname": "testhost",
			"os":       "Linux",
			"arch":     "amd64",
			"uptime":   "5d 3h",
			"cpu": map[string]any{
				"model": "Intel", "cores": float64(4), "threads": float64(8), "mhz": float64(2400),
			},
			"memory": map[string]any{
				"total_bytes": 16e9, "available_bytes": 8e9,
			},
			"disk": []any{
				map[string]any{
					"mount_point": "/", "total_bytes": 500e9, "use_pct": float64(42),
				},
			},
		},
	}, "sysinfo")
	assert.Nil(t, err)
}

func TestPrintResponseTextExecNonZero(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK: true,
		Data: map[string]any{
			"stdout": "", "stderr": "command not found\n", "exit_code": float64(127),
		},
	}, "exec")
	assert.Nil(t, err)
}

func TestPrintResponseTextWrite(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: map[string]any{"bytes_written": float64(1024)},
	}, "write")
	assert.Nil(t, err)
}

func TestPrintResponseTextEdit(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: map[string]any{"modified": true, "message": "replaced 3 occurrences"},
	}, "edit")
	assert.Nil(t, err)
}

func TestPrintResponseTextEditNotModified(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: map[string]any{"modified": false},
	}, "edit")
	assert.Nil(t, err)
}

func TestPrintResponseTextDisconnect(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: map[string]any{"status": "disconnecting"},
	}, "disconnect")
	assert.Nil(t, err)
}

func TestPrintResponseTextReadlink(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: map[string]any{"path": "/usr/bin/python", "target": "/usr/bin/python3.11"},
	}, "readlink")
	assert.Nil(t, err)
}

func TestPrintResponseTextUnknownAction(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: map[string]any{"key": "value"},
	}, "unknown")
	assert.Nil(t, err)
}

func TestPrintResponseTextNonMap(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: "plain string",
	}, "exec")
	assert.Nil(t, err)
}

func TestPrintResponseTextLsWithSymlink(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK: true,
		Data: map[string]any{
			"path": "/tmp",
			"entries": []any{
				map[string]any{
					"name": "/tmp/link", "size": float64(12), "mode": "777",
					"is_dir": false, "is_link": true, "target": "/tmp/real",
				},
			},
		},
	}, "ls")
	assert.Nil(t, err)
}

func TestPrintResponseTextRead(t *testing.T) {
	t.Serial()
	old := os.Stdout
	f, _ := os.CreateTemp(t.TempDir(), "stdout")
	os.Stdout = f
	defer func() { os.Stdout = old }()

	OutputJSON = false
	err := printResponse(&protocol.DaemonResponse{
		OK:   true,
		Data: map[string]any{"content": "file content here", "size": float64(17)},
	}, "read")
	assert.Nil(t, err)
}

// No-socket tests exercise the sendRequest error branch in each function.
