package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGatherSystemInfo(t *testing.T) {
	info, err := GatherSystemInfo()
	require.Nil(t, err)
	assert.NotEqual(t, "", info.Hostname)
	assert.NotEqual(t, "", info.Arch)
	assert.Greater(t, info.CPU.Threads, 0)
	assert.Greater(t, info.Memory.TotalBytes, int64(0))
	assert.Greater(t, info.Memory.UsedBytes, int64(0))
}

func TestReadOSRelease(t *testing.T) {
	result := readOSRelease()
	assert.NotEqual(t, "", result)
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
	assert.Greater(t, info.TotalBytes, int64(0))
	assert.GreaterOrEqual(t, info.UsedBytes, int64(0))
}

func TestReadDiskInfo(t *testing.T) {
	disks := readDiskInfo()
	assert.NotEqual(t, 0, len(disks))

	for _, d := range disks {
		assert.NotEqual(t, "", d.MountPoint)
		assert.GreaterOrEqual(t, d.TotalBytes, int64(0))
	}
}

func TestReadNetworkInfo(t *testing.T) {
	interfaces := readNetworkInfo()
	assert.NotEqual(t, 0, len(interfaces))

	foundLoopback := false
	for _, iface := range interfaces {
		// Linux uses "lo", macOS uses "lo0"
		if iface.Name == "lo" || iface.Name == "lo0" {
			foundLoopback = true
			assert.Equal(t, "up", iface.State)
		}
	}
	assert.True(t, foundLoopback)
}

func TestReadGPUInfo(t *testing.T) {
	// GPU info may be empty (no GPU), should not panic
	gpus := readGPUInfo()
	_ = gpus // just verify it doesn't crash
}
