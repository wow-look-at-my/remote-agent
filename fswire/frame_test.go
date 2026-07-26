package fswire

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)

	payload := []byte{0x00, 0xff, 0x10, 'h', 'i'} // binary, including NUL
	require.NoError(t, w.WriteFrame(&Request{ID: 7, Op: OpWrite, Path: "a/b", Offset: 42}, payload))
	require.NoError(t, w.WriteFrame(&Request{ID: 8, Op: OpStat, Path: "c"}, nil))

	r := NewReader(&buf)

	var first Request
	got, err := r.ReadFrame(&first)
	require.NoError(t, err)
	assert.Equal(t, uint64(7), first.ID)
	assert.Equal(t, OpWrite, first.Op)
	assert.Equal(t, int64(42), first.Offset)
	assert.Equal(t, payload, got, "binary payloads must survive byte for byte")

	var second Request
	got, err = r.ReadFrame(&second)
	require.NoError(t, err)
	assert.Equal(t, uint64(8), second.ID)
	assert.Nil(t, got)

	// A clean end of stream is io.EOF, not an error.
	_, err = r.ReadFrame(&Request{})
	assert.ErrorIs(t, err, io.EOF)
}

func TestFrameLargePayload(t *testing.T) {
	var buf bytes.Buffer
	payload := bytes.Repeat([]byte("x"), 1<<20)
	require.NoError(t, NewWriter(&buf).WriteFrame(&Response{ID: 1}, payload))

	var resp Response
	got, err := NewReader(&buf).ReadFrame(&resp)
	require.NoError(t, err)
	assert.Len(t, got, len(payload))
}

func TestFrameRejectsOversizedPayload(t *testing.T) {
	err := NewWriter(&bytes.Buffer{}).WriteFrame(&Request{}, make([]byte, MaxPayload+1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

func TestFrameRejectsImplausibleLengths(t *testing.T) {
	// A corrupt length prefix must be refused, not turned into a giant
	// allocation.
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(MaxHeader+1))
	_, err := NewReader(&buf).ReadFrame(&Request{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")

	buf.Reset()
	header := []byte(`{"id":1}`)
	binary.Write(&buf, binary.BigEndian, uint32(len(header)))
	buf.Write(header)
	binary.Write(&buf, binary.BigEndian, uint32(MaxPayload+1))
	_, err = NewReader(&buf).ReadFrame(&Request{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

func TestFrameTruncatedStream(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, NewWriter(&buf).WriteFrame(&Request{ID: 1, Op: OpStat}, []byte("data")))

	truncated := buf.Bytes()[:buf.Len()-2]
	_, err := NewReader(bytes.NewReader(truncated)).ReadFrame(&Request{})
	require.Error(t, err)
	assert.NotErrorIs(t, err, io.EOF, "a truncated frame is corruption, not a clean close")
}

func TestFrameRejectsBadJSON(t *testing.T) {
	var buf bytes.Buffer
	header := []byte(`{not json`)
	binary.Write(&buf, binary.BigEndian, uint32(len(header)))
	buf.Write(header)
	binary.Write(&buf, binary.BigEndian, uint32(0))

	_, err := NewReader(&buf).ReadFrame(&Request{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode frame header")
}

func TestWriterIsConcurrencySafe(t *testing.T) {
	// Frames from many goroutines must not interleave: the remote answers
	// requests concurrently onto one stream.
	var buf syncBuffer
	w := NewWriter(&buf)

	const writers = 16
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			payload := bytes.Repeat([]byte{byte(id)}, 1024)
			assert.NoError(t, w.WriteFrame(&Response{ID: uint64(id)}, payload))
		}(i)
	}
	wg.Wait()

	r := NewReader(bytes.NewReader(buf.Bytes()))
	seen := map[uint64]bool{}
	for i := 0; i < writers; i++ {
		var resp Response
		payload, err := r.ReadFrame(&resp)
		require.NoError(t, err)
		require.Len(t, payload, 1024)
		assert.Equal(t, byte(resp.ID), payload[0], "payload must belong to its header")
		seen[resp.ID] = true
	}
	assert.Len(t, seen, writers)
}

func TestReadFrameReusesHeaderBuffer(t *testing.T) {
	// Reading many frames must not depend on header sizes growing monotonic.
	var buf bytes.Buffer
	w := NewWriter(&buf)
	require.NoError(t, w.WriteFrame(&Request{ID: 1, Op: OpStat, Path: strings.Repeat("long/", 200)}, nil))
	require.NoError(t, w.WriteFrame(&Request{ID: 2, Op: OpStat, Path: "x"}, nil))

	r := NewReader(&buf)
	var a, b Request
	_, err := r.ReadFrame(&a)
	require.NoError(t, err)
	_, err = r.ReadFrame(&b)
	require.NoError(t, err)
	assert.Equal(t, "x", b.Path)
}

// syncBuffer is a mutex-guarded bytes.Buffer for the concurrency test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Bytes()
}
