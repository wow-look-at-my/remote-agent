package main

import (
	"os"
	"testing"
)

func TestPrintUsage(t *testing.T) {
	old := os.Stderr
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = f
	defer func() { os.Stderr = old }()

	printUsage()

	f.Seek(0, 0)
	data, _ := os.ReadFile(f.Name())
	if len(data) == 0 {
		t.Error("printUsage should write to stderr")
	}
}

func TestRunNoArgs(t *testing.T) {
	old := os.Stderr
	f, _ := os.CreateTemp(t.TempDir(), "stderr")
	os.Stderr = f
	defer func() { os.Stderr = old }()

	err := run(nil)
	if err == nil {
		t.Error("expected error with no args")
	}
}

func TestRunUnknownCommand(t *testing.T) {
	old := os.Stderr
	f, _ := os.CreateTemp(t.TempDir(), "stderr")
	os.Stderr = f
	defer func() { os.Stderr = old }()

	err := run([]string{"nonexistent"})
	if err == nil {
		t.Error("expected error for unknown command")
	}
}

func TestRunExecNoSocket(t *testing.T) {
	// Set TMPDIR to empty dir so no daemon socket is found
	t.Setenv("TMPDIR", t.TempDir())

	err := run([]string{"exec", "ls"})
	if err == nil {
		t.Error("expected error (no daemon)")
	}
}

func TestRunDisconnectNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	err := run([]string{"disconnect"})
	if err == nil {
		t.Error("expected error (no daemon)")
	}
}

func TestRunReadNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	err := run([]string{"read", "/some/file"})
	if err == nil {
		t.Error("expected error (no daemon)")
	}
}

func TestRunPingNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	err := run([]string{"ping"})
	if err == nil {
		t.Error("expected error (no daemon)")
	}
}

func TestRunSysinfoNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	err := run([]string{"sysinfo"})
	if err == nil {
		t.Error("expected error (no daemon)")
	}
}

func TestRunServeNoAction(t *testing.T) {
	err := run([]string{"serve"})
	if err == nil {
		t.Error("expected error with no serve action")
	}
}

func TestRunUploadNoArgs(t *testing.T) {
	err := run([]string{"upload"})
	if err == nil {
		t.Error("expected error with no upload args")
	}
}

func TestRunDownloadNoArgs(t *testing.T) {
	err := run([]string{"download"})
	if err == nil {
		t.Error("expected error with no download args")
	}
}

func TestRunLsNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	err := run([]string{"ls"})
	if err == nil {
		t.Error("expected error (no daemon)")
	}
}

func TestRunPsNoSocket(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	err := run([]string{"ps"})
	if err == nil {
		t.Error("expected error (no daemon)")
	}
}

func TestRunEditNoArgs(t *testing.T) {
	err := run([]string{"edit"})
	if err == nil {
		t.Error("expected error with no edit args")
	}
}

func TestRunConnectNoTarget(t *testing.T) {
	err := run([]string{"connect"})
	if err == nil {
		t.Error("expected error with no target")
	}
}

func TestRunWriteNoArgs(t *testing.T) {
	err := run([]string{"write"})
	if err == nil {
		t.Error("expected error with no write args")
	}
}
