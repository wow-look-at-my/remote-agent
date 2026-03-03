package agent

import (
	"fmt"
	"os/exec"

	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

func readOSRelease() string {
	out, err := exec.Command("sw_vers", "-productName").Output()
	if err != nil {
		return "macOS"
	}
	name := strings.TrimSpace(string(out))

	out, err = exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return name
	}
	return name + " " + strings.TrimSpace(string(out))
}

func readUptime() string {
	out, err := exec.Command("sysctl", "-n", "kern.boottime").Output()
	if err != nil {
		return "unknown"
	}
	// Format: { sec = 1710000000, usec = 0 } ...
	s := string(out)
	i := strings.Index(s, "sec = ")
	if i < 0 {
		return "unknown"
	}
	s = s[i+6:]
	j := strings.Index(s, ",")
	if j < 0 {
		return "unknown"
	}
	bootSec, err := strconv.ParseInt(s[:j], 10, 64)
	if err != nil {
		return "unknown"
	}

	secs := time.Now().Unix() - bootSec
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

	out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
	if err == nil {
		info.Model = strings.TrimSpace(string(out))
	}

	out, err = exec.Command("sysctl", "-n", "hw.physicalcpu").Output()
	if err == nil {
		info.Cores, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}

	out, err = exec.Command("sysctl", "-n", "hw.logicalcpu").Output()
	if err == nil {
		info.Threads, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}

	out, err = exec.Command("sysctl", "-n", "hw.cpufrequency").Output()
	if err == nil {
		freq, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
		info.MHz = freq / 1e6
	}

	return info
}

func readMemoryInfo() protocol.MemoryInfo {
	info := protocol.MemoryInfo{}

	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err == nil {
		info.TotalBytes, _ = strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	}

	// Use vm_stat to get page statistics
	out, err = exec.Command("vm_stat").Output()
	if err != nil {
		return info
	}

	pageSize := int64(4096)
	lines := strings.Split(string(out), "\n")
	var freePages, activePages, wiredPages, specPages int64
	for _, line := range lines {
		if strings.HasPrefix(line, "Mach Virtual Memory Statistics") {
			if i := strings.Index(line, "page size of "); i >= 0 {
				s := line[i+13:]
				if j := strings.Index(s, " "); j >= 0 {
					pageSize, _ = strconv.ParseInt(s[:j], 10, 64)
				}
			}
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		val := strings.TrimSpace(parts[1])
		val = strings.TrimSuffix(val, ".")
		n, _ := strconv.ParseInt(val, 10, 64)

		switch strings.TrimSpace(parts[0]) {
		case "Pages free":
			freePages = n
		case "Pages active":
			activePages = n
		case "Pages wired down":
			wiredPages = n
		case "Pages speculative":
			specPages = n
		}
	}

	info.UsedBytes = (activePages + wiredPages) * pageSize
	info.AvailableBytes = (freePages + specPages) * pageSize

	return info
}

func readDiskInfo() []protocol.DiskInfo {
	var disks []protocol.DiskInfo

	out, err := exec.Command("df", "-k").Output()
	if err != nil {
		return disks
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i, line := range lines {
		if i == 0 {	// skip header
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		device := fields[0]
		if !strings.HasPrefix(device, "/dev/") {
			continue
		}

		totalKB, _ := strconv.ParseInt(fields[1], 10, 64)
		usedKB, _ := strconv.ParseInt(fields[2], 10, 64)
		availKB, _ := strconv.ParseInt(fields[3], 10, 64)
		mountPoint := fields[len(fields)-1]

		total := totalKB * 1024
		used := usedKB * 1024
		avail := availKB * 1024

		var usePct float64
		if total > 0 {
			usePct = float64(used) / float64(total) * 100
		}

		// Get filesystem type via statfs
		var stat syscall.Statfs_t
		fsType := ""
		if err := syscall.Statfs(mountPoint, &stat); err == nil {
			fsType = int8SliceToString(stat.Fstypename[:])
		}

		disks = append(disks, protocol.DiskInfo{
			Device:		device,
			MountPoint:	mountPoint,
			FSType:		fsType,
			TotalBytes:	total,
			UsedBytes:	used,
			AvailBytes:	avail,
			UsePct:		usePct,
		})
	}
	return disks
}

func int8SliceToString(s []int8) string {
	var b []byte
	for _, v := range s {
		if v == 0 {
			break
		}
		b = append(b, byte(v))
	}
	return string(b)
}
