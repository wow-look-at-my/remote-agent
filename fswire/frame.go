package fswire

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Bounds what a peer can make the other end allocate. Well above FUSE's 1 MiB write.
const MaxPayload = 8 << 20

// A header is small, so this only stops a corrupt length prefix allocating wildly.
const MaxHeader = 16 << 20

// A frame is `uint32 headerLen | header JSON | uint32 payloadLen | payload`. File data stays
// out of the JSON: base64 inflates it by a third, and JSON cannot carry raw bytes at all.
// Writer is safe for concurrent use, because a frame must reach the wire in one piece.
type Writer struct {
	mu sync.Mutex
	w  io.Writer
}

// NewWriter returns a Writer that emits frames to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// WriteFrame writes one header + payload frame.
func (fw *Writer) WriteFrame(header any, payload []byte) error {
	data, err := json.Marshal(header)
	if err != nil {
		return fmt.Errorf("encode frame header: %w", err)
	}
	if len(payload) > MaxPayload {
		return fmt.Errorf("frame payload of %d bytes exceeds the %d byte limit", len(payload), MaxPayload)
	}

	buf := make([]byte, 0, 8+len(data)+len(payload))
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(data)))
	buf = append(buf, data...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(payload)))
	buf = append(buf, payload...)

	fw.mu.Lock()
	defer fw.mu.Unlock()
	if _, err := fw.w.Write(buf); err != nil {
		return fmt.Errorf("write frame: %w", err)
	}
	return nil
}

// Reader deserializes frames from a stream. A single goroutine should own it:
// a frame's header and payload must be read consecutively.
type Reader struct {
	r   io.Reader
	buf []byte // reusable header scratch
}

// NewReader returns a Reader over r.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: r}
}

// ReadFrame reads one frame and unmarshals its header into out, returning the
// binary payload. io.EOF at a frame boundary means the peer closed cleanly.
func (fr *Reader) ReadFrame(out any) ([]byte, error) {
	headerLen, err := fr.readLen(MaxHeader, "header")
	if err != nil {
		return nil, err
	}
	if cap(fr.buf) < int(headerLen) {
		fr.buf = make([]byte, headerLen)
	}
	header := fr.buf[:headerLen]
	if _, err := io.ReadFull(fr.r, header); err != nil {
		return nil, fmt.Errorf("read frame header: %w", err)
	}
	if err := json.Unmarshal(header, out); err != nil {
		return nil, fmt.Errorf("decode frame header: %w", err)
	}

	payloadLen, err := fr.readLen(MaxPayload, "payload")
	if err != nil {
		return nil, err
	}
	if payloadLen == 0 {
		return nil, nil
	}
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(fr.r, payload); err != nil {
		return nil, fmt.Errorf("read frame payload: %w", err)
	}
	return payload, nil
}

// readLen reads one length prefix, rejecting implausible values rather than
// trying to allocate them.
func (fr *Reader) readLen(max uint32, what string) (uint32, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(fr.r, prefix[:]); err != nil {
		if err == io.ErrUnexpectedEOF {
			return 0, fmt.Errorf("read frame %s length: %w", what, err)
		}
		return 0, err // io.EOF at a boundary is the clean-close signal
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n > max {
		return 0, fmt.Errorf("frame %s length %d exceeds the %d byte limit", what, n, max)
	}
	return n, nil
}
