package agent

import (
	"testing"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestGatherSystemInfo(t *testing.T) {
	info, err := GatherSystemInfo()
	require.Nil(t, err)
	assert.NotEqual(t,// Hostname should be non-empty
	"", info.Hostname)
	assert.NotEqual(t,// Arch should match
	"", info.Arch)
	assert.Greater(t,// CPU should have threads > 0 (we're running on a real system)
	info.CPU.Threads, 0)
	assert.Greater(t,// Memory total should be > 0
	info.Memory.TotalBytes, 0)
	assert.Greater(t,// Memory used should be > 0
	info.Memory.UsedBytes, 0)

}

func TestReadOSRelease(t *testing.T) {
	result := readOSRelease()
	assert.
	// On a real system, this should not be "unknown"
	NotEqual(t, "", result)

}

func TestReadUptime(t *testing.T) {
	result := readUptime()
	assert.NotEqual(t, "", result)

}

func TestReadCPUInfo(t *testing.T) {
	info := readCPUInfo()
	assert.Greater(t, info.Threads, 0)

}

func TestReadMemoryInfo(t *testing.T) {
	info := readMemoryInfo()
	assert.Greater(t, info.TotalBytes, 0)
	assert.GreaterOrEqual(t, info.UsedBytes, 0)

}

func TestReadDiskInfo(t *testing.T) {
	disks := readDiskInfo()
	assert.
	// There should be at least one disk (root filesystem)
	NotEqual(t, 0, len(disks))

	for _, d := range disks {
		assert.NotEqual(t, "", d.MountPoint)
		assert.Greater(t, d.TotalBytes, 0)

	}
}

func TestReadNetworkInfo(t *testing.T) {
	interfaces := readNetworkInfo()
	assert.
	// There should be at least lo
	NotEqual(t, 0, len(interfaces))

	foundLo := false
	for _, iface := range interfaces {
		if iface.Name == "lo" {
			foundLo = true
			assert.Equal(t, "up", iface.State)

		}
	}
	assert.True(t, foundLo)

}

func TestReadGPUInfo(t *testing.T) {
	// GPU info may be empty (no GPU), should not panic
	gpus := readGPUInfo()
	_ = gpus	// just verify it doesn't crash
}

func TestParseKBValue(t *testing.T) {
	tests := []struct {
		input	string
		want	int64
	}{
		{"1024 kB", 1024},
		{"0 kB", 0},
		{"16384 kB", 16384},
		{"100", 100},
		{"", 0},
	}
	for _, tt := range tests {
		got := parseKBValue(tt.input)
		assert.Equal(t, tt.want, got)

	}
}
