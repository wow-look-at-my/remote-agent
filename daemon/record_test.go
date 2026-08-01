package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTargetRecordRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	require.NoError(t, WriteTargetRecord("root@host", 2222))

	rec, err := ReadTargetRecord(TargetPath("root@host"))
	require.NoError(t, err)
	assert.Equal(t, "root@host", rec.Target)
	assert.Equal(t, 2222, rec.Port)

	// The record sits beside the socket it describes and is not itself a socket.
	assert.Equal(t, filepath.Dir(SocketPath("root@host")), filepath.Dir(TargetPath("root@host")))
	assert.NotEqual(t, SocketPath("root@host"), TargetPath("root@host"))
}

func TestListTargetRecords(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	assert.Empty(t, ListTargetRecords())

	require.NoError(t, WriteTargetRecord("root@a", 22))
	require.NoError(t, WriteTargetRecord("root@b", 22))
	// Damaged records are skipped rather than failing the lookup.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "remote-agent-junk.target"), []byte("not json"), 0600))

	recs := ListTargetRecords()
	require.Len(t, recs, 2)
	targets := []string{recs[0].Target, recs[1].Target}
	assert.ElementsMatch(t, []string{"root@a", "root@b"}, targets)
}

func TestReadTargetRecordErrors(t *testing.T) {
	dir := t.TempDir()

	_, err := ReadTargetRecord(filepath.Join(dir, "missing.target"))
	assert.Error(t, err)

	bad := filepath.Join(dir, "bad.target")
	require.NoError(t, os.WriteFile(bad, []byte("{}"), 0600))
	_, err = ReadTargetRecord(bad)
	assert.ErrorContains(t, err, "no target")
}
