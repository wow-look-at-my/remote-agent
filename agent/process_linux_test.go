package agent

import (
	"os"
	"testing"

	"github.com/wow-look-at-my/remote-agent/protocol"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestParseStat(t *testing.T) {
	info := &protocol.ProcessInfo{}
	data := "1234 (test process) S 1 1234 1234 0 -1 4194304 100 0 0 0 10 5 0 0 20 0 1 0 100 12345678 500 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0"
	parseStat(info, data)
	assert.Equal(t, "S", info.State)
	assert.Equal(t, 1, info.PPID)
	// RSS is field 23 (index 21 in rest after comm) = 500 pages.
	assert.Equal(t, int64(500)*int64(os.Getpagesize()), info.RSS)
}

func TestParseStatWithParensInName(t *testing.T) {
	info := &protocol.ProcessInfo{}
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
	parseStat(info, "1234")
	parseStat(info, "1234 noparens S 1")
}

func TestParseStatusUnknownUID(t *testing.T) {
	info := &protocol.ProcessInfo{}
	data := "Name:\ttest\nUid:\t99999\t99999\t99999\t99999\n"
	parseStatus(info, data)
	assert.Equal(t, "99999", info.User)
}

func TestParseStatusNoUid(t *testing.T) {
	info := &protocol.ProcessInfo{}
	data := "Name:\ttest\nState:\tS\n"
	parseStatus(info, data)
	assert.Equal(t, "", info.User)
}

func TestParseStatTooFewFields(t *testing.T) {
	info := &protocol.ProcessInfo{}
	data := "1234 (test) S 1 2 3"
	parseStat(info, data)
	assert.Equal(t, "", info.State)
}

func TestReadProcessInfoSelf(t *testing.T) {
	info, err := readProcessInfo(os.Getpid())
	require.Nil(t, err)
	assert.Equal(t, os.Getpid(), info.PID)
	assert.NotEqual(t, "", info.Command)
}
