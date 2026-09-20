package keg

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matt-freed/open-plaato-keg/internal/blynk"
	"github.com/matt-freed/open-plaato-keg/internal/events"
	"github.com/matt-freed/open-plaato-keg/internal/store"
)

const testToken = "00000000000000000000000000000001"

type harness struct {
	t        *testing.T
	server   *Server
	store    *store.Store
	bus      *events.Bus
	addr     string
	amounts  *recordingConsumer
	listener net.Listener
}

// recordingConsumer captures the volumes handed to downstream integrations.
//
// KegAmount is called from the connection's goroutine while the test reads
// from its own, so the recording is guarded.
type recordingConsumer struct {
	mu   sync.Mutex
	seen []float64
}

func (r *recordingConsumer) KegAmount(_ string, amount float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, amount)
}

// amounts returns a copy of what has been recorded so far.
func (r *recordingConsumer) amounts() []float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]float64(nil), r.seen...)
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	consumer := &recordingConsumer{}
	bus := events.NewBus()
	srv := NewServer(st, bus, false, consumer)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ln)
	}()
	t.Cleanup(func() {
		ln.Close()
		srv.Shutdown()
		<-done
	})

	return &harness{
		t: t, server: srv, store: st, bus: bus,
		addr: ln.Addr().String(), amounts: consumer, listener: ln,
	}
}

// dial opens a connection and returns it along with a reader for the server's
// acknowledgements.
func (h *harness) dial() net.Conn {
	h.t.Helper()
	c, err := net.Dial("tcp", h.addr)
	if err != nil {
		h.t.Fatalf("dial: %v", err)
	}
	h.t.Cleanup(func() { c.Close() })
	return c
}

// sendAndRead writes a segment and reads the single acknowledgement it expects.
func sendAndRead(t *testing.T, c net.Conn, segment []byte) []byte {
	t.Helper()
	if _, err := c.Write(segment); err != nil {
		t.Fatalf("write: %v", err)
	}
	ack := make([]byte, blynk.HeaderSize)
	if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, err := io.ReadFull(c, ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	return ack
}

// waitFor polls until cond holds, so tests do not depend on ingest timing.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func loginSegment() []byte {
	return blynk.NewCommand(blynk.CmdGetSharedDash, 1, []byte(testToken))
}

func pinWrite(msgID uint16, pin, value string) []byte {
	return blynk.NewCommand(blynk.CmdHardware, msgID, []byte("vw\x00"+pin+"\x00"+value))
}

// The acknowledgement must be exactly the five bytes the firmware expects,
// echoing the message id it sent.
func TestAckIsFiveBytesEchoingMsgID(t *testing.T) {
	h := newHarness(t)
	c := h.dial()

	ack := sendAndRead(t, c, loginSegment())
	if want := []byte{0x00, 0x00, 0x01, 0x00, 0xC8}; !bytes.Equal(ack, want) {
		t.Errorf("ack = % X, want % X", ack, want)
	}

	ack = sendAndRead(t, c, pinWrite(45, "51", "3.802"))
	if want := []byte{0x00, 0x00, 0x2D, 0x00, 0xC8}; !bytes.Equal(ack, want) {
		t.Errorf("ack = % X, want % X", ack, want)
	}
}

// The firmware coalesces several messages into one segment and expects a
// single reply carrying the first message's id. Acknowledging each frame
// separately would be wire-incompatible.
func TestOneAckPerSegmentEchoingFirstMsgID(t *testing.T) {
	h := newHarness(t)
	c := h.dial()
	sendAndRead(t, c, loginSegment())

	var segment []byte
	for i, pin := range []string{"51", "48", "56"} {
		segment = append(segment, pinWrite(uint16(45+i), pin, "1.000")...)
	}

	if _, err := c.Write(segment); err != nil {
		t.Fatalf("write: %v", err)
	}

	ack := make([]byte, blynk.HeaderSize)
	if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, err := io.ReadFull(c, ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if want := []byte{0x00, 0x00, 0x2D, 0x00, 0xC8}; !bytes.Equal(ack, want) {
		t.Errorf("ack = % X, want % X (the first message's id)", ack, want)
	}

	// There must be no second acknowledgement.
	if err := c.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	extra := make([]byte, blynk.HeaderSize)
	n, err := c.Read(extra)
	if err == nil {
		t.Errorf("server sent a second ack of %d bytes: % X", n, extra[:n])
	} else if !isTimeout(err) {
		t.Errorf("unexpected error waiting for a second ack: %v", err)
	}
}

// A ping is acknowledged like anything else.
func TestPingIsAcknowledged(t *testing.T) {
	h := newHarness(t)
	c := h.dial()
	ack := sendAndRead(t, c, blynk.NewCommand(blynk.CmdPing, 163, nil))
	if want := []byte{0x00, 0x00, 0xA3, 0x00, 0xC8}; !bytes.Equal(ack, want) {
		t.Errorf("ack = % X, want % X", ack, want)
	}
}

// A frame split across TCP writes must be reassembled, not dropped. The Elixir
// server closed the connection in this case.
func TestFrameSplitAcrossWritesIsReassembled(t *testing.T) {
	h := newHarness(t)
	c := h.dial()
	sendAndRead(t, c, loginSegment())

	frame := pinWrite(50, "51", "2.500")
	if _, err := c.Write(frame[:7]); err != nil {
		t.Fatalf("write first half: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := c.Write(frame[7:]); err != nil {
		t.Fatalf("write second half: %v", err)
	}

	ack := make([]byte, blynk.HeaderSize)
	if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, err := io.ReadFull(c, ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}

	waitFor(t, "the split frame to be ingested", func() bool {
		k, err := h.store.GetKeg(testToken)
		return err == nil && k.AmountLeft != nil && *k.AmountLeft == 2.5
	})
}

func TestIngestStoresKegData(t *testing.T) {
	h := newHarness(t)
	c := h.dial()
	sendAndRead(t, c, loginSegment())
	sendAndRead(t, c, pinWrite(2, "51", "3.802"))

	waitFor(t, "the keg to be stored", func() bool {
		_, err := h.store.GetKeg(testToken)
		return err == nil
	})

	k, err := h.store.GetKeg(testToken)
	if err != nil {
		t.Fatalf("GetKeg: %v", err)
	}
	if k.AmountLeft == nil || *k.AmountLeft != 3.802 {
		t.Errorf("AmountLeft = %v, want 3.802", k.AmountLeft)
	}
}

// Device metadata arriving before the first pin write must be kept and stored
// once the device proves it is a keg, which is the order the firmware uses.
func TestInternalMetadataArrivingFirstIsReplayed(t *testing.T) {
	h := newHarness(t)
	c := h.dial()

	sendAndRead(t, c, loginSegment())
	sendAndRead(t, c, blynk.NewCommand(blynk.CmdInternal, 2,
		[]byte("ver\x002.0.10a\x00dev\x00ESP32\x00build\x00Jul 20 2020 12:31:35\x00")))

	// Nothing may be stored yet: metadata alone does not identify a keg.
	time.Sleep(100 * time.Millisecond)
	if _, err := h.store.GetKeg(testToken); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a keg was created from metadata alone: %v", err)
	}

	sendAndRead(t, c, pinWrite(3, "51", "3.802"))

	waitFor(t, "metadata to be replayed", func() bool {
		k, err := h.store.GetKeg(testToken)
		return err == nil && k.Internal["dev"] == "ESP32"
	})

	k, _ := h.store.GetKeg(testToken)
	if k.Internal["ver"] != "2.0.10a" || k.Internal["build"] != "Jul 20 2020 12:31:35" {
		t.Errorf("Internal = %v, want the metadata replayed in full", k.Internal)
	}
}

// A device that only ever sends metadata — an airlock, which shares the Blynk
// protocol and this port — must not create a phantom keg.
func TestMetadataOnlyDeviceCreatesNoKeg(t *testing.T) {
	h := newHarness(t)
	c := h.dial()

	sendAndRead(t, c, blynk.NewCommand(blynk.CmdLogin, 1, []byte("airlock-token")))
	sendAndRead(t, c, blynk.NewCommand(blynk.CmdInternal, 2, []byte("ver\x000.5.1\x00dev\x00NodeMCU\x00")))
	// Airlock pins: bubble count and temperature, which are not keg pins.
	sendAndRead(t, c, pinWrite(3, "100", "42"))
	sendAndRead(t, c, pinWrite(4, "101", "20.5"))

	time.Sleep(150 * time.Millisecond)

	ids, err := h.store.ListKegIDs()
	if err != nil {
		t.Fatalf("ListKegIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("stored kegs = %v, want none for a non-keg device", ids)
	}
}

func TestRegistryTracksConnectedKegs(t *testing.T) {
	h := newHarness(t)
	c := h.dial()
	sendAndRead(t, c, loginSegment())

	waitFor(t, "the keg to register", func() bool {
		return len(h.server.Registry().IDs()) == 1
	})
	if got := h.server.Registry().IDs(); got[0] != testToken {
		t.Errorf("connected = %v, want [%s]", got, testToken)
	}

	c.Close()
	waitFor(t, "the keg to deregister", func() bool {
		return len(h.server.Registry().IDs()) == 0
	})
}

// Disconnecting mid-pour must clear the pouring flag, or the UI shows a pour
// that never ends.
func TestDisconnectClearsPouringState(t *testing.T) {
	h := newHarness(t)
	c := h.dial()
	sendAndRead(t, c, loginSegment())
	sendAndRead(t, c, pinWrite(2, "49", "255"))

	waitFor(t, "the pour to register", func() bool {
		k, err := h.store.GetKeg(testToken)
		return err == nil && k.IsPouring != nil && *k.IsPouring
	})

	c.Close()

	waitFor(t, "the pouring flag to clear", func() bool {
		k, err := h.store.GetKeg(testToken)
		return err == nil && k.IsPouring != nil && !*k.IsPouring
	})
}

// A keg that reconnects before the server notices the old socket is gone must
// take over, and the displaced connection must not deregister its replacement.
func TestReconnectTakesOverRegistration(t *testing.T) {
	h := newHarness(t)

	first := h.dial()
	sendAndRead(t, first, loginSegment())
	waitFor(t, "the first connection to register", func() bool {
		return len(h.server.Registry().IDs()) == 1
	})

	second := h.dial()
	sendAndRead(t, second, loginSegment())

	// The new connection must be the registered one, and stay registered after
	// the displaced read loop has finished exiting.
	waitFor(t, "the takeover to settle", func() bool {
		conn, err := h.server.Registry().Lookup(testToken)
		return err == nil && conn.RemoteAddr() == second.LocalAddr().String()
	})
	time.Sleep(150 * time.Millisecond)

	conn, err := h.server.Registry().Lookup(testToken)
	if err != nil {
		t.Fatalf("the keg was deregistered by the displaced connection: %v", err)
	}
	if conn.RemoteAddr() != second.LocalAddr().String() {
		t.Errorf("registered connection = %s, want the new one at %s",
			conn.RemoteAddr(), second.LocalAddr())
	}

	// The displaced connection must have been closed.
	if err := first.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, err := first.Read(make([]byte, 1)); err == nil {
		t.Error("the displaced connection is still open")
	}
}

func TestCommanderSendsPinWrite(t *testing.T) {
	h := newHarness(t)
	c := h.dial()
	sendAndRead(t, c, loginSegment())
	waitFor(t, "the keg to register", func() bool {
		return len(h.server.Registry().IDs()) == 1
	})

	cmd := NewCommander(h.server.Registry())
	if err := cmd.Tare(testToken); err != nil {
		t.Fatalf("Tare: %v", err)
	}

	if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	header := make([]byte, blynk.HeaderSize)
	if _, err := io.ReadFull(c, header); err != nil {
		t.Fatalf("read command header: %v", err)
	}
	if header[0] != byte(blynk.CmdHardware) {
		t.Fatalf("command byte = %d, want %d (hardware)", header[0], blynk.CmdHardware)
	}
	msgID := uint16(header[1])<<8 | uint16(header[2])
	if msgID == 0 {
		t.Error("message id is 0; the device treats that as unset")
	}

	length := int(header[3])<<8 | int(header[4])
	body := make([]byte, length)
	if _, err := io.ReadFull(c, body); err != nil {
		t.Fatalf("read command body: %v", err)
	}
	if string(body) != "vw\x0060\x001" {
		t.Errorf("body = %q, want the tare pin write", body)
	}
}

func TestCommanderReportsNotConnected(t *testing.T) {
	h := newHarness(t)
	cmd := NewCommander(h.server.Registry())
	if err := cmd.Tare("nobody"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Tare = %v, want ErrNotConnected", err)
	}
}

func TestCommanderValidatesArguments(t *testing.T) {
	cmd := NewCommander(NewRegistry())
	if err := cmd.SetSensitivity("x", 5); err == nil {
		t.Error("SetSensitivity accepted level 5")
	}
	if err := cmd.SetUnit("x", 3); err == nil {
		t.Error("SetUnit accepted unit 3")
	}
	if err := cmd.SetKegMode("x", 0); err == nil {
		t.Error("SetKegMode accepted mode 0")
	}
}

// The beer style and date the app sends carry a leading space, which the
// device's display depends on.
func TestBeerStyleIsSpacePrefixed(t *testing.T) {
	h := newHarness(t)
	c := h.dial()
	sendAndRead(t, c, loginSegment())
	waitFor(t, "the keg to register", func() bool {
		return len(h.server.Registry().IDs()) == 1
	})

	cmd := NewCommander(h.server.Registry())
	if err := cmd.SetBeerStyle(testToken, "my style"); err != nil {
		t.Fatalf("SetBeerStyle: %v", err)
	}

	if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	header := make([]byte, blynk.HeaderSize)
	if _, err := io.ReadFull(c, header); err != nil {
		t.Fatalf("read header: %v", err)
	}
	body := make([]byte, int(header[3])<<8|int(header[4]))
	if _, err := io.ReadFull(c, body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "vw\x0064\x00 my style" {
		t.Errorf("body = %q, want a leading space before the style", body)
	}
}

func TestAmountConsumerIsNotified(t *testing.T) {
	h := newHarness(t)
	c := h.dial()
	sendAndRead(t, c, loginSegment())
	sendAndRead(t, c, pinWrite(2, "51", "3.802"))

	waitFor(t, "the consumer to be notified", func() bool {
		return len(h.amounts.amounts()) == 1
	})
	if got := h.amounts.amounts(); got[0] != 3.802 {
		t.Errorf("consumer saw %v, want 3.802", got[0])
	}

	// A packet without a volume reading must not notify.
	sendAndRead(t, c, pinWrite(3, "56", "22.5"))
	time.Sleep(100 * time.Millisecond)
	if got := h.amounts.amounts(); len(got) != 1 {
		t.Errorf("consumer saw %d notifications, want 1", len(got))
	}
}

func TestEventsArePublished(t *testing.T) {
	h := newHarness(t)
	sub, cancel := h.bus.Subscribe()
	defer cancel()

	c := h.dial()
	sendAndRead(t, c, loginSegment())
	sendAndRead(t, c, pinWrite(2, "51", "3.802"))

	select {
	case e := <-sub:
		if e.Kind != events.KegUpdated || e.KegID != testToken {
			t.Errorf("event = %+v, want a keg update for %s", e, testToken)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event published")
	}
}

// Replaying a recording of a real keg session end to end is the closest thing
// to the hardware this suite can get.
func TestReplayRealCaptureSession(t *testing.T) {
	h := newHarness(t)
	c := h.dial()

	names, err := filepath.Glob("../../testdata/capture/*.bin")
	if err != nil || len(names) == 0 {
		t.Fatalf("no capture files: %v", err)
	}
	sort.Slice(names, func(i, j int) bool {
		a, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(names[i]), ".bin"))
		b, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(names[j]), ".bin"))
		return a < b
	})

	// The device waits for its acknowledgement before sending the next
	// segment, so reading one ack per write also asserts the ack contract.
	for _, name := range names {
		segment, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		ack := sendAndRead(t, c, segment)
		if ack[0] != 0x00 || ack[3] != 0x00 || ack[4] != 0xC8 {
			t.Fatalf("%s: ack = % X, want a success response", filepath.Base(name), ack)
		}
	}

	waitFor(t, "the session to be ingested", func() bool {
		k, err := h.store.GetKeg(testToken)
		return err == nil && k.FirmwareVersion != nil
	})

	k, err := h.store.GetKeg(testToken)
	if err != nil {
		t.Fatalf("GetKeg: %v", err)
	}
	if *k.FirmwareVersion != "2.0.10a" {
		t.Errorf("FirmwareVersion = %q, want 2.0.10a", *k.FirmwareVersion)
	}
	if k.AmountLeft == nil || *k.AmountLeft != 0.04 {
		t.Errorf("AmountLeft = %v, want 0.04", k.AmountLeft)
	}
	if k.KegTemperature == nil || *k.KegTemperature != 22.25 {
		t.Errorf("KegTemperature = %v, want 22.25", k.KegTemperature)
	}
	if k.Internal["dev"] != "ESP32" {
		t.Errorf("Internal[dev] = %q, want ESP32", k.Internal["dev"])
	}
	if k.BeerLeftUnit != "kg" {
		t.Errorf("BeerLeftUnit = %q, want kg (metric, weight mode)", k.BeerLeftUnit)
	}
	// The session records one pour of 0.184.
	if k.LastPour == nil || *k.LastPour != 0.184 {
		t.Errorf("LastPour = %v, want 0.184", k.LastPour)
	}
}
