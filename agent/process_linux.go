package agent

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

// GatherProcessList returns a structured list of running processes.
func GatherProcessList(filter string) (*protocol.ProcessList, error) {
	procs, err := readProcsFromProc(filter)
	if err != nil {
		return nil, err
	}
	return &protocol.ProcessList{Processes: procs}, nil
}

func readProcsFromProc(filter string) ([]protocol.ProcessInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}

	var procs []protocol.ProcessInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		info, err := readProcessInfo(pid)
		if err != nil {
			continue
		}

		if filter != "" && !strings.Contains(strings.ToLower(info.Command), strings.ToLower(filter)) {
			continue
		}

		procs = append(procs, *info)
	}
	return procs, nil
}

func readProcessInfo(pid int) (*protocol.ProcessInfo, error) {
	procDir := filepath.Join("/proc", strconv.Itoa(pid))

	info := &protocol.ProcessInfo{PID: pid}

	// Read stat for name, ppid, state, rss
	statData, err := os.ReadFile(filepath.Join(procDir, "stat"))
	if err != nil {
		return nil, err
	}
	parseStat(info, string(statData))

	// Read status for uid
	statusData, err := os.ReadFile(filepath.Join(procDir, "status"))
	if err == nil {
		parseStatus(info, string(statusData))
	}

	// Read cmdline for full command
	cmdData, err := os.ReadFile(filepath.Join(procDir, "cmdline"))
	if err == nil && len(cmdData) > 0 {
		// cmdline is null-separated
		info.Command = strings.Join(strings.Split(strings.TrimRight(string(cmdData), "\x00"), "\x00"), " ")
	}

	// If cmdline is empty (kernel thread), use the name from stat
	if info.Command == "" {
		info.Command = fmt.Sprintf("[%s]", info.State)
	}

	return info, nil
}

func parseStat(info *protocol.ProcessInfo, data string) {
	// Format: pid (comm) state ppid ... rss ...
	// The comm field can contain spaces and parentheses, so we find the last ')'
	start := strings.Index(data, "(")
	end := strings.LastIndex(data, ")")
	if start < 0 || end < 0 || end <= start {
		return
	}

	name := data[start+1 : end]
	rest := strings.Fields(data[end+2:])
	// rest[0] = state, rest[1] = ppid, ...
	if len(rest) < 22 {
		return
	}

	if info.Command == "" {
		info.Command = name
	}
	info.State = rest[0]
	info.PPID, _ = strconv.Atoi(rest[1])

	// RSS is field 23 in stat (index 21 in rest, since rest starts after comm)
	// Actually rest[21] is rss in pages
	rssPages, _ := strconv.ParseInt(rest[21], 10, 64)
	info.RSS = rssPages * int64(os.Getpagesize())
}

func parseStatus(info *protocol.ProcessInfo, data string) {
	for _, line := range strings.Split(data, "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				u, err := user.LookupId(fields[1])
				if err == nil {
					info.User = u.Username
				} else {
					info.User = fields[1]
				}
			}
			return
		}
	}
}
