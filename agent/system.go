package agent

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

// GatherSystemInfo collects all system stats in one call.
func GatherSystemInfo() (*protocol.SystemInfo, error) {
	info := &protocol.SystemInfo{
		Arch: runtime.GOARCH,
	}

	info.Hostname, _ = os.Hostname()
	info.OS = readOSRelease()
	info.Uptime = readUptime()
	info.CPU = readCPUInfo()
	info.Memory = readMemoryInfo()
	info.Disk = readDiskInfo()
	info.Network = readNetworkInfo()
	info.GPU = readGPUInfo()

	return info, nil
}

func readOSRelease() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return "unknown"
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			val := strings.TrimPrefix(line, "PRETTY_NAME=")
			return strings.Trim(val, "\"")
		}
	}
	return "unknown"
}

func readUptime() string {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "unknown"
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return "unknown"
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return fields[0] + "s"
	}

	days := int(secs) / 86400
	hours := (int(secs) % 86400) / 3600
	mins := (int(secs) % 3600) / 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func readCPUInfo() protocol.CPUInfo {
	info := protocol.CPUInfo{}

	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return info
	}
	defer f.Close()

	var threads int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "model name":
			if info.Model == "" {
				info.Model = val
			}
		case "cpu cores":
			if info.Cores == 0 {
				info.Cores, _ = strconv.Atoi(val)
			}
		case "cpu MHz":
			if info.MHz == 0 {
				info.MHz, _ = strconv.ParseFloat(val, 64)
			}
		case "processor":
			threads++
		}
	}
	info.Threads = threads
	return info
}

func readMemoryInfo() protocol.MemoryInfo {
	info := protocol.MemoryInfo{}

	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return info
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Value is like "1234 kB"
		valKB := parseKBValue(val)

		switch key {
		case "MemTotal":
			info.TotalBytes = valKB * 1024
		case "MemAvailable":
			info.AvailableBytes = valKB * 1024
		case "SwapTotal":
			info.SwapTotalBytes = valKB * 1024
		case "SwapFree":
			// We'll compute SwapUsed after
			info.SwapUsedBytes = valKB * 1024 // temporarily store SwapFree
		}
	}
	info.UsedBytes = info.TotalBytes - info.AvailableBytes
	info.SwapUsedBytes = info.SwapTotalBytes - info.SwapUsedBytes // SwapTotal - SwapFree
	return info
}

func parseKBValue(s string) int64 {
	s = strings.TrimSuffix(s, " kB")
	s = strings.TrimSpace(s)
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func readDiskInfo() []protocol.DiskInfo {
	var disks []protocol.DiskInfo

	// Read /proc/mounts to get mount points
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return disks
	}
	defer f.Close()

	// Skip pseudo-filesystems
	skipFS := map[string]bool{
		"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true,
		"tmpfs": true, "cgroup": true, "cgroup2": true, "securityfs": true,
		"pstore": true, "debugfs": true, "tracefs": true, "hugetlbfs": true,
		"mqueue": true, "fusectl": true, "binfmt_misc": true, "configfs": true,
		"efivarfs": true, "autofs": true, "overlay": true,
	}

	seen := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		device := fields[0]
		mountPoint := fields[1]
		fsType := fields[2]

		if skipFS[fsType] {
			continue
		}
		if seen[mountPoint] {
			continue
		}
		seen[mountPoint] = true

		var stat syscall.Statfs_t
		if err := syscall.Statfs(mountPoint, &stat); err != nil {
			continue
		}

		total := int64(stat.Blocks) * int64(stat.Bsize)
		avail := int64(stat.Bavail) * int64(stat.Bsize)
		used := total - int64(stat.Bfree)*int64(stat.Bsize)

		var usePct float64
		if total > 0 {
			usePct = float64(used) / float64(total) * 100
		}

		disks = append(disks, protocol.DiskInfo{
			Device:     device,
			MountPoint: mountPoint,
			FSType:     fsType,
			TotalBytes: total,
			UsedBytes:  used,
			AvailBytes: avail,
			UsePct:     usePct,
		})
	}
	return disks
}

func readNetworkInfo() []protocol.NetworkInterface {
	var interfaces []protocol.NetworkInterface

	ifaces, err := net.Interfaces()
	if err != nil {
		return interfaces
	}

	for _, iface := range ifaces {
		ni := protocol.NetworkInterface{
			Name:  iface.Name,
			MAC:   iface.HardwareAddr.String(),
			State: "down",
		}

		if iface.Flags&net.FlagUp != 0 {
			ni.State = "up"
		}

		addrs, err := iface.Addrs()
		if err == nil {
			for _, addr := range addrs {
				ip := addr.String()
				if strings.Contains(ip, ":") {
					ni.IPv6 = append(ni.IPv6, ip)
				} else {
					ni.IPv4 = append(ni.IPv4, ip)
				}
			}
		}

		interfaces = append(interfaces, ni)
	}
	return interfaces
}

func readGPUInfo() []protocol.GPUInfo {
	var gpus []protocol.GPUInfo

	// Try nvidia-smi
	out, err := exec.Command("nvidia-smi",
		"--query-gpu=name,memory.total,memory.used,utilization.gpu,temperature.gpu",
		"--format=csv,noheader,nounits").Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for i, line := range lines {
			fields := strings.Split(line, ", ")
			if len(fields) < 5 {
				continue
			}
			memTotal, _ := strconv.Atoi(strings.TrimSpace(fields[1]))
			memUsed, _ := strconv.Atoi(strings.TrimSpace(fields[2]))
			utilPct, _ := strconv.ParseFloat(strings.TrimSpace(fields[3]), 64)
			tempC, _ := strconv.Atoi(strings.TrimSpace(fields[4]))

			gpus = append(gpus, protocol.GPUInfo{
				Index:      i,
				Name:       strings.TrimSpace(fields[0]),
				Vendor:     "nvidia",
				MemTotalMB: memTotal,
				MemUsedMB:  memUsed,
				UtilPct:    utilPct,
				TempC:      tempC,
			})
		}
		return gpus
	}

	// Try rocm-smi for AMD
	out, err = exec.Command("rocm-smi", "--showproductname", "--showmeminfo", "vram", "--showtemp", "--csv").Output()
	if err == nil && len(out) > 0 {
		gpus = append(gpus, protocol.GPUInfo{
			Vendor: "amd",
			Name:   "AMD GPU (rocm-smi detected)",
		})
		return gpus
	}

	return gpus
}
