package agent

import (
	"os"
	"testing"

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
	list, err := GatherProcessList("__nonexistent_process_name__")
	require.Nil(t, err)
	assert.Equal(t, 0, len(list.Processes))
}
