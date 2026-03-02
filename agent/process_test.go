package agent

import (
	"os"
	"testing"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

func TestGatherProcessList(t *testing.T) {
	list, err := GatherProcessList("")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Processes) == 0 {
		t.Error("expected at least one process")
	}

	// Our own PID should be in the list
	myPID := os.Getpid()
	found := false
	for _, p := range list.Processes {
		if p.PID == myPID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("own PID %d not found in process list", myPID)
	}
}

func TestGatherProcessListWithFilter(t *testing.T) {
	// Filter for something that doesn't exist
	list, err := GatherProcessList("__nonexistent_process_name__")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Processes) != 0 {
		t.Errorf("expected 0 processes with impossible filter, got %d", len(list.Processes))
	}
}

func TestParseStat(t *testing.T) {
	info := &protocol.ProcessInfo{}
	// Simulate a /proc/[pid]/stat line
	data := "1234 (test process) S 1 1234 1234 0 -1 4194304 100 0 0 0 10 5 0 0 20 0 1 0 100 12345678 500 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0"
	parseStat(info, data)

	if info.State != "S" {
		t.Errorf("state = %q, want %q", info.State, "S")
	}
	if info.PPID != 1 {
		t.Errorf("ppid = %d, want 1", info.PPID)
	}
	// RSS is field 23 (index 21 in rest after comm)
	// The 500 at position 21 (0-indexed from rest) = 500 pages = 500 * 4096
	if info.RSS != 500*4096 {
		t.Errorf("rss = %d, want %d", info.RSS, 500*4096)
	}
}

func TestParseStatWithParensInName(t *testing.T) {
	info := &protocol.ProcessInfo{}
	// Process name with parentheses
	data := "5678 (tricky (name)) R 100 5678 5678 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 0 0 200 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0"
	parseStat(info, data)

	if info.State != "R" {
		t.Errorf("state = %q, want %q", info.State, "R")
	}
	if info.PPID != 100 {
		t.Errorf("ppid = %d, want 100", info.PPID)
	}
}

func TestParseStatus(t *testing.T) {
	info := &protocol.ProcessInfo{}
	data := "Name:\ttest\nUmask:\t0022\nState:\tS (sleeping)\nTgid:\t1234\nNgid:\t0\nPid:\t1234\nPPid:\t1\nUid:\t0\t0\t0\t0\nGid:\t0\t0\t0\t0\n"
	parseStatus(info, data)

	if info.User != "root" {
		t.Errorf("user = %q, want %q", info.User, "root")
	}
}

func TestParseStatMalformed(t *testing.T) {
	info := &protocol.ProcessInfo{}
	// Too short
	parseStat(info, "1234")
	// No crash = pass

	// No parentheses
	parseStat(info, "1234 noparens S 1")
	// No crash = pass
}

func TestParseStatusUnknownUID(t *testing.T) {
	info := &protocol.ProcessInfo{}
	// Use a very high UID that won't exist
	data := "Name:\ttest\nUid:\t99999\t99999\t99999\t99999\n"
	parseStatus(info, data)

	// Should fall back to the numeric UID
	if info.User != "99999" {
		t.Errorf("user = %q, want %q", info.User, "99999")
	}
}

func TestParseStatusNoUid(t *testing.T) {
	info := &protocol.ProcessInfo{}
	data := "Name:\ttest\nState:\tS\n"
	parseStatus(info, data)

	if info.User != "" {
		t.Errorf("user = %q, want empty", info.User)
	}
}

func TestParseStatTooFewFields(t *testing.T) {
	info := &protocol.ProcessInfo{}
	// Has parens but not enough fields after
	data := "1234 (test) S 1 2 3"
	parseStat(info, data)
	// Should not crash, state should not be set (not enough fields)
	if info.State != "" {
		t.Error("state should be empty with too few fields")
	}
}

func TestReadProcessInfoSelf(t *testing.T) {
	info, err := readProcessInfo(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if info.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d", info.PID, os.Getpid())
	}
	if info.Command == "" {
		t.Error("command should not be empty")
	}
}
