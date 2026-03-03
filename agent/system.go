package agent

import (
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

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
