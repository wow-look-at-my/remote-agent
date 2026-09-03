package agent

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

// GatherProcessList returns a structured list of running processes using
// tasklist on Windows.
func GatherProcessList(filter string) (*protocol.ProcessList, error) {
	out, err := exec.Command("tasklist", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return nil, fmt.Errorf("tasklist: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var procs []protocol.ProcessInfo
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// CSV format: "name","pid","session","session#","mem usage"
		fields := strings.Split(line, "\",\"")
		if len(fields) < 5 {
			continue
		}

		name := strings.Trim(fields[0], "\"")
		pid, _ := strconv.Atoi(strings.Trim(fields[1], "\""))
		memStr := strings.Trim(fields[4], "\"")
		memStr = strings.ReplaceAll(memStr, ",", "")
		memStr = strings.TrimSuffix(memStr, " K")
		memKB, _ := strconv.ParseInt(strings.TrimSpace(memStr), 10, 64)

		if filter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(filter)) {
			continue
		}

		procs = append(procs, protocol.ProcessInfo{
			PID:     pid,
			RSS:     memKB * 1024,
			Command: name,
		})
	}
	return &protocol.ProcessList{Processes: procs}, nil
}
