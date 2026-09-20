package blynk

import (
	"encoding/binary"
	"fmt"
)

// MaxBodySize bounds how large a single frame body may be before the stream is
// treated as corrupt. The Plaato firmware advertises a 1024-byte buffer and the
// largest frame observed in a real capture is 103 bytes, so this is generous.
const MaxBodySize = 8192

// Framer reassembles Blynk frames from a TCP byte stream.
//
// The Elixir implementation this was ported from decoded whatever a single
// socket read happened to deliver and crashed the connection when a frame was
// split across reads. Framer buffers the remainder instead, so a frame may
// safely arrive one byte at a time.
type Framer struct {
	buf []byte
}

// Feed appends p to the buffer and returns every complete frame now available.
// Any trailing partial frame is retained for the next call.
//
// An error means the stream is unrecoverable (a body length beyond
// MaxBodySize); the caller should close the connection.
func (f *Framer) Feed(p []byte) ([]Frame, error) {
	f.buf = append(f.buf, p...)

	var frames []Frame
	for {
		if len(f.buf) < HeaderSize {
			break
		}

		cmd := Command(f.buf[0])
		msgID := binary.BigEndian.Uint16(f.buf[1:3])
		third := binary.BigEndian.Uint16(f.buf[3:5])

		// A response frame ends at the header: its third field is a status
		// code, not a body length.
		if cmd == CmdResponse {
			frames = append(frames, Frame{Cmd: cmd, MsgID: msgID, Status: Status(third)})
			f.buf = f.buf[HeaderSize:]
			continue
		}

		if int(third) > MaxBodySize {
			return frames, fmt.Errorf("blynk: frame body of %d bytes exceeds the %d byte limit", third, MaxBodySize)
		}
		if len(f.buf) < HeaderSize+int(third) {
			break // wait for the rest of the body
		}

		body := make([]byte, third)
		copy(body, f.buf[HeaderSize:HeaderSize+int(third)])
		frames = append(frames, Frame{Cmd: cmd, MsgID: msgID, Body: body})
		f.buf = f.buf[HeaderSize+int(third):]
	}

	// Reclaim the backing array once it has been fully drained, so a long-lived
	// connection does not hold onto a grown buffer.
	if len(f.buf) == 0 {
		f.buf = nil
	}
	return frames, nil
}

// Buffered reports how many bytes of an incomplete frame are being held.
func (f *Framer) Buffered() int { return len(f.buf) }
