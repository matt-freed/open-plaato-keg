package blynk

import (
	"bytes"
	"testing"
)

// Bytes taken verbatim from real captures in testdata/capture.
var (
	// 0.bin - get_shared_dash carrying the 32-char auth token.
	loginFrame = []byte{29, 0, 1, 0, 32,
		'0', '0', '0', '0', '0', '0', '0', '0', '0', '0', '0', '0', '0', '0', '0', '0',
		'0', '0', '0', '0', '0', '0', '0', '0', '0', '0', '0', '0', '0', '0', '0', '1'}

	// A single vw write: pin 51 = "0.040".
	pin51Frame = []byte{20, 0, 7, 0, 11, 'v', 'w', 0, '5', '1', 0, '0', '.', '0', '4', '0'}

	// 163.bin - ping, zero-length body.
	pingFrame = []byte{6, 0, 163, 0, 0}

	// 3.bin - six frames coalesced into one TCP segment.
	coalesced = []byte{
		20, 0, 4, 0, 12, 118, 119, 0, 53, 50, 0, 45, 55, 46, 53, 48, 48,
		20, 0, 5, 0, 13, 118, 119, 0, 57, 51, 0, 50, 46, 48, 46, 49, 48, 97,
		19, 0, 6, 0, 13, 53, 49, 0, 109, 97, 120, 0, 50, 48, 46, 48, 53, 57,
		20, 0, 7, 0, 11, 118, 119, 0, 53, 49, 0, 48, 46, 48, 52, 48,
		20, 0, 8, 0, 11, 118, 119, 0, 52, 56, 0, 48, 46, 48, 48, 48,
		20, 0, 9, 0, 11, 118, 119, 0, 53, 52, 0, 48, 46, 48, 48, 48,
	}
)

func TestFeedSingleFrame(t *testing.T) {
	var f Framer
	frames, err := f.Feed(loginFrame)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	got := frames[0]
	if got.Cmd != CmdGetSharedDash {
		t.Errorf("Cmd = %v, want get_shared_dash", got.Cmd)
	}
	if got.MsgID != 1 {
		t.Errorf("MsgID = %d, want 1", got.MsgID)
	}
	if string(got.Body) != "00000000000000000000000000000001" {
		t.Errorf("Body = %q", got.Body)
	}
	if f.Buffered() != 0 {
		t.Errorf("Buffered = %d, want 0", f.Buffered())
	}
}

func TestFeedZeroLengthBody(t *testing.T) {
	var f Framer
	frames, err := f.Feed(pingFrame)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frames) != 1 || frames[0].Cmd != CmdPing || frames[0].MsgID != 163 {
		t.Fatalf("got %+v, want a single ping with msgID 163", frames)
	}
	if len(frames[0].Body) != 0 {
		t.Errorf("Body = %q, want empty", frames[0].Body)
	}
}

func TestFeedCoalescedFrames(t *testing.T) {
	var f Framer
	frames, err := f.Feed(coalesced)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frames) != 6 {
		t.Fatalf("got %d frames, want 6", len(frames))
	}
	wantCmds := []Command{CmdHardware, CmdHardware, CmdProperty, CmdHardware, CmdHardware, CmdHardware}
	wantIDs := []uint16{4, 5, 6, 7, 8, 9}
	for i, fr := range frames {
		if fr.Cmd != wantCmds[i] {
			t.Errorf("frame %d: Cmd = %v, want %v", i, fr.Cmd, wantCmds[i])
		}
		if fr.MsgID != wantIDs[i] {
			t.Errorf("frame %d: MsgID = %d, want %d", i, fr.MsgID, wantIDs[i])
		}
	}
	if string(frames[2].Body) != "51\x00max\x0020.059" {
		t.Errorf("property body = %q", frames[2].Body)
	}
}

// The Elixir server dropped the connection when a frame was split across TCP
// reads. Feeding one byte at a time must yield exactly the same frames.
func TestFeedByteAtATime(t *testing.T) {
	var f Framer
	var got []Frame
	for _, b := range coalesced {
		frames, err := f.Feed([]byte{b})
		if err != nil {
			t.Fatalf("Feed: %v", err)
		}
		got = append(got, frames...)
	}
	if len(got) != 6 {
		t.Fatalf("got %d frames, want 6", len(got))
	}
	if string(got[3].Body) != "vw\x0051\x000.040" {
		t.Errorf("frame 3 body = %q", got[3].Body)
	}
	if f.Buffered() != 0 {
		t.Errorf("Buffered = %d, want 0", f.Buffered())
	}
}

func TestFeedSplitMidBody(t *testing.T) {
	var f Framer
	frames, err := f.Feed(pin51Frame[:8])
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("got %d frames from a partial body, want 0", len(frames))
	}
	if f.Buffered() != 8 {
		t.Errorf("Buffered = %d, want 8", f.Buffered())
	}
	frames, err = f.Feed(pin51Frame[8:])
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frames) != 1 || string(frames[0].Body) != "vw\x0051\x000.040" {
		t.Fatalf("got %+v after completion", frames)
	}
}

func TestFeedResponseFrameEndsAtHeader(t *testing.T) {
	// A response frame carries a status in the length position and has no body,
	// so a following frame must still decode.
	stream := append(ResponseSuccess(42), pingFrame...)
	var f Framer
	frames, err := f.Feed(stream)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(frames))
	}
	if frames[0].Cmd != CmdResponse || frames[0].MsgID != 42 || frames[0].Status != StatusSuccess {
		t.Errorf("response frame = %+v", frames[0])
	}
	if frames[1].Cmd != CmdPing {
		t.Errorf("second frame = %v, want ping", frames[1].Cmd)
	}
}

// An unassigned opcode must still be framed by its length so the stream does
// not desynchronise.
func TestFeedUnknownCommandDoesNotDesync(t *testing.T) {
	unknown := []byte{57, 0, 99, 0, 3, 'a', 'b', 'c'}
	var f Framer
	frames, err := f.Feed(append(unknown, pingFrame...))
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(frames))
	}
	if frames[0].Cmd.String() != "unknown_cmd" {
		t.Errorf("Cmd.String() = %q, want unknown_cmd", frames[0].Cmd.String())
	}
	if frames[1].Cmd != CmdPing {
		t.Errorf("stream desynchronised: second frame = %v", frames[1].Cmd)
	}
}

func TestFeedRejectsOversizedBody(t *testing.T) {
	oversized := []byte{20, 0, 1, 0xFF, 0xFF}
	var f Framer
	if _, err := f.Feed(oversized); err == nil {
		t.Fatal("Feed accepted a body larger than MaxBodySize")
	}
}

func TestResponseSuccessBytes(t *testing.T) {
	// The exact acknowledgement the firmware expects.
	if got, want := ResponseSuccess(1), []byte{0x00, 0x00, 0x01, 0x00, 0xC8}; !bytes.Equal(got, want) {
		t.Errorf("ResponseSuccess(1) = % X, want % X", got, want)
	}
	if got, want := ResponseSuccess(45), []byte{0x00, 0x00, 0x2D, 0x00, 0xC8}; !bytes.Equal(got, want) {
		t.Errorf("ResponseSuccess(45) = % X, want % X", got, want)
	}
	if got, want := ResponseSuccess(65535), []byte{0x00, 0xFF, 0xFF, 0x00, 0xC8}; !bytes.Equal(got, want) {
		t.Errorf("ResponseSuccess(65535) = % X, want % X", got, want)
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	encoded := NewCommand(CmdHardware, 4242, []byte("vw\x0060\x001"))
	var f Framer
	frames, err := f.Feed(encoded)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	if frames[0].Cmd != CmdHardware || frames[0].MsgID != 4242 || string(frames[0].Body) != "vw\x0060\x001" {
		t.Errorf("round trip = %+v", frames[0])
	}
}
