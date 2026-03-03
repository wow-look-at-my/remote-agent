package agent

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

// GatherProcessList returns a structured list of running processes using ps on macOS.
func GatherProcessList(filter string) (*protocol.ProcessList, error) {
	out, err := exec.Command("ps", "-axo", "pid,ppid,stat,rss,user,args").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var procs []protocol.ProcessInfo
	for i, line := range lines {
		if i == 0 { // skip header
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		pid, _ := strconv.Atoi(fields[0])
		ppid, _ := strconv.Atoi(fields[1])
		rss, _ := strconv.ParseInt(fields[3], 10, 64)
		command := strings.Join(fields[5:], " ")

		if filter != "" && !strings.Contains(strings.ToLower(command), strings.ToLower(filter)) {
			continue
		}

		procs = append(procs, protocol.ProcessInfo{
			PID:     pid,
			PPID:    ppid,
			State:   fields[2],
			RSS:     rss * 1024, // ps reports RSS in KB
			User:    fields[4],
			Command: command,
		})
	}
	return &protocol.ProcessList{Processes: procs}, nil
}
