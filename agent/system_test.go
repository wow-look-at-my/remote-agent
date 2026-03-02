package agent

import (
	"testing"
)

func TestGatherSystemInfo(t *testing.T) {
	info, err := GatherSystemInfo()
	if err != nil {
		t.Fatal(err)
	}

	// Hostname should be non-empty
	if info.Hostname == "" {
		t.Error("hostname should not be empty")
	}

	// Arch should match
	if info.Arch == "" {
		t.Error("arch should not be empty")
	}

	// CPU should have threads > 0 (we're running on a real system)
	if info.CPU.Threads <= 0 {
		t.Errorf("cpu.threads = %d, want > 0", info.CPU.Threads)
	}

	// Memory total should be > 0
	if info.Memory.TotalBytes <= 0 {
		t.Errorf("memory.total = %d, want > 0", info.Memory.TotalBytes)
	}

	// Memory used should be > 0
	if info.Memory.UsedBytes <= 0 {
		t.Errorf("memory.used = %d, want > 0", info.Memory.UsedBytes)
	}
}

func TestReadOSRelease(t *testing.T) {
	result := readOSRelease()
	// On a real system, this should not be "unknown"
	if result == "" {
		t.Error("os release should not be empty")
	}
}

func TestReadUptime(t *testing.T) {
	result := readUptime()
	if result == "" {
		t.Error("uptime should not be empty")
	}
}

func TestReadCPUInfo(t *testing.T) {
	info := readCPUInfo()
	if info.Threads <= 0 {
		t.Errorf("threads = %d, want > 0", info.Threads)
	}
}

func TestReadMemoryInfo(t *testing.T) {
	info := readMemoryInfo()
	if info.TotalBytes <= 0 {
		t.Errorf("total = %d, want > 0", info.TotalBytes)
	}
	if info.UsedBytes < 0 {
		t.Errorf("used = %d, want >= 0", info.UsedBytes)
	}
}

func TestReadDiskInfo(t *testing.T) {
	disks := readDiskInfo()
	// There should be at least one disk (root filesystem)
	if len(disks) == 0 {
		t.Error("expected at least one disk")
	}
	for _, d := range disks {
		if d.MountPoint == "" {
			t.Error("disk mount point should not be empty")
		}
		if d.TotalBytes <= 0 {
			t.Errorf("disk %s total = %d, want > 0", d.MountPoint, d.TotalBytes)
		}
	}
}

func TestReadNetworkInfo(t *testing.T) {
	interfaces := readNetworkInfo()
	// There should be at least lo
	if len(interfaces) == 0 {
		t.Error("expected at least one network interface")
	}
	foundLo := false
	for _, iface := range interfaces {
		if iface.Name == "lo" {
			foundLo = true
			if iface.State != "up" {
				t.Error("lo should be up")
			}
		}
	}
	if !foundLo {
		t.Error("expected to find loopback interface")
	}
}

func TestReadGPUInfo(t *testing.T) {
	// GPU info may be empty (no GPU), should not panic
	gpus := readGPUInfo()
	_ = gpus // just verify it doesn't crash
}

func TestParseKBValue(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"1024 kB", 1024},
		{"0 kB", 0},
		{"16384 kB", 16384},
		{"100", 100},
		{"", 0},
	}
	for _, tt := range tests {
		got := parseKBValue(tt.input)
		if got != tt.want {
			t.Errorf("parseKBValue(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}
