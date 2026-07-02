package client

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStdout runs fn with os.Stdout redirected to a file and returns the
// raw bytes written, so binary output can be asserted exactly.
func captureStdout(t *testing.T, fn func()) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdout")
	f, err := os.Create(path)
	require.Nil(t, err)

	old := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = old }()
	fn()
	require.Nil(t, f.Close())

	data, err := os.ReadFile(path)
	require.Nil(t, err)
	return data
}

func TestPrintReadTextPlain(t *testing.T) {
	out := captureStdout(t, func() {
		err := printTextResponse(map[string]any{"content": "hello\n", "size": float64(6)}, "read")
		assert.Nil(t, err)
	})
	assert.Equal(t, "hello\n", string(out))
}

func TestPrintReadTextBinary(t *testing.T) {
	binary := []byte{0x7f, 'E', 'L', 'F', 0x00, 0xff, 0x80} // not valid UTF-8
	out := captureStdout(t, func() {
		err := printTextResponse(map[string]any{
			"content_b64": base64.StdEncoding.EncodeToString(binary),
			"size":        float64(len(binary)),
		}, "read")
		assert.Nil(t, err)
	})
	assert.Equal(t, binary, out, "binary file bytes must reach stdout exactly")
}

func TestPrintReadTextBadB64(t *testing.T) {
	captureStdout(t, func() {
		err := printTextResponse(map[string]any{"content_b64": "!!!"}, "read")
		assert.NotNil(t, err)
	})
}
