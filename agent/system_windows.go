package agent

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

func readOSRelease() string {
	out, err := exec.Command("cmd", "/C", "ver").Output()
	if err != nil {
		return "Windows"
	}
	return strings.TrimSpace(string(out))
}

func readUptime() string {
	out, err := exec.Command("wmic", "os", "get", "lastbootuptime").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func readCPUInfo() protocol.CPUInfo {
	return protocol.CPUInfo{
		Model:   runtime.GOARCH,
		Cores:   runtime.NumCPU(),
		Threads: runtime.NumCPU(),
	}
}

func readMemoryInfo() protocol.MemoryInfo {
	info := protocol.MemoryInfo{}

	out, err := exec.Command("wmic", "OS", "get", "TotalVisibleMemorySize,FreePhysicalMemory", "/value").Output()
	if err != nil {
		return info
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		val, _ := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		switch strings.TrimSpace(parts[0]) {
		case "TotalVisibleMemorySize":
			info.TotalBytes = val * 1024
		case "FreePhysicalMemory":
			info.AvailableBytes = val * 1024
		}
	}
	info.UsedBytes = info.TotalBytes - info.AvailableBytes

	return info
}

func readDiskInfo() []protocol.DiskInfo {
	var disks []protocol.DiskInfo

	out, err := exec.Command("wmic", "logicaldisk", "get", "DeviceID,FileSystem,FreeSpace,Size", "/value").Output()
	if err != nil {
		return disks
	}

	var current protocol.DiskInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if current.Device != "" && current.TotalBytes > 0 {
				current.UsedBytes = current.TotalBytes - current.AvailBytes
				if current.TotalBytes > 0 {
					current.UsePct = float64(current.UsedBytes) / float64(current.TotalBytes) * 100
				}
				current.MountPoint = current.Device
				disks = append(disks, current)
			}
			current = protocol.DiskInfo{}
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "DeviceID":
			current.Device = val
		case "FileSystem":
			current.FSType = val
		case "FreeSpace":
			current.AvailBytes, _ = strconv.ParseInt(val, 10, 64)
		case "Size":
			current.TotalBytes, _ = strconv.ParseInt(val, 10, 64)
		}
	}
	// Handle last entry
	if current.Device != "" && current.TotalBytes > 0 {
		current.UsedBytes = current.TotalBytes - current.AvailBytes
		if current.TotalBytes > 0 {
			current.UsePct = float64(current.UsedBytes) / float64(current.TotalBytes) * 100
		}
		current.MountPoint = current.Device
		disks = append(disks, current)
	}

	return disks
}
