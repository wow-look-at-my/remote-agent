package agent

import (
	"os"
	"testing"

	"github.com/wow-look-at-my/remote-agent/protocol"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestGatherProcessList(t *testing.T) {
	list, err := GatherProcessList("")
	require.Nil(t, err)
	assert.NotEqual(t, 0, len(list.Processes))

	// Our own PID should be in the list
	myPID := os.Getpid()
	found := false
	for _, p := range list.Processes {
		if p.PID == myPID {
			found = true
			break
		}
	}
	assert.True(t, found)

}

func TestGatherProcessListWithFilter(t *testing.T) {
	// Filter for something that doesn't exist
	list, err := GatherProcessList("__nonexistent_process_name__")
	require.Nil(t, err)
	assert.Equal(t, 0, len(list.Processes))

}

func TestParseStat(t *testing.T) {
	info := &protocol.ProcessInfo{}
	// Simulate a /proc/[pid]/stat line
	data := "1234 (test process) S 1 1234 1234 0 -1 4194304 100 0 0 0 10 5 0 0 20 0 1 0 100 12345678 500 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0"
	parseStat(info, data)
	assert.Equal(t, "S", info.State)
	assert.Equal(t, 1, info.PPID)
	assert.Equal(t,// RSS is field 23 (index 21 in rest after comm)
	// The 500 at position 21 (0-indexed from rest) = 500 pages = 500 * 4096
	500*4096, info.RSS)

}

func TestParseStatWithParensInName(t *testing.T) {
	info := &protocol.ProcessInfo{}
	// Process name with parentheses
	data := "5678 (tricky (name)) R 100 5678 5678 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 0 0 200 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0"
	parseStat(info, data)
	assert.Equal(t, "R", info.State)
	assert.Equal(t, 100, info.PPID)

}

func TestParseStatus(t *testing.T) {
	info := &protocol.ProcessInfo{}
	data := "Name:\ttest\nUmask:\t0022\nState:\tS (sleeping)\nTgid:\t1234\nNgid:\t0\nPid:\t1234\nPPid:\t1\nUid:\t0\t0\t0\t0\nGid:\t0\t0\t0\t0\n"
	parseStatus(info, data)
	assert.Equal(t, "root", info.User)

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
	assert.

	// Should fall back to the numeric UID
	Equal(t, "99999", info.User)

}

func TestParseStatusNoUid(t *testing.T) {
	info := &protocol.ProcessInfo{}
	data := "Name:\ttest\nState:\tS\n"
	parseStatus(info, data)
	assert.Equal(t, "", info.User)

}

func TestParseStatTooFewFields(t *testing.T) {
	info := &protocol.ProcessInfo{}
	// Has parens but not enough fields after
	data := "1234 (test) S 1 2 3"
	parseStat(info, data)
	assert.
	// Should not crash, state should not be set (not enough fields)
	Equal(t, "", info.State)

}

func TestReadProcessInfoSelf(t *testing.T) {
	info, err := readProcessInfo(os.Getpid())
	require.Nil(t, err)
	assert.Equal(t, os.Getpid(), info.PID)
	assert.NotEqual(t, "", info.Command)

}
