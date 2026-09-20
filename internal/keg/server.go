// Package keg runs the TCP listener that Plaato Keg hardware connects to, and
// sends commands back to connected devices.
package keg

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/matt-freed/open-plaato-keg/internal/blynk"
	"github.com/matt-freed/open-plaato-keg/internal/events"
	"github.com/matt-freed/open-plaato-keg/internal/plaato"
	"github.com/matt-freed/open-plaato-keg/internal/store"
)

// ReadTimeout is how long a connection may go without sending anything before
// the server closes it. The firmware advertises a 20 second heartbeat, so a
// minute of silence means the device is gone.
const ReadTimeout = 60 * time.Second

// readBufferSize is comfortably above the largest frame the firmware sends;
// it advertises a 1024 byte buffer of its own.
const readBufferSize = 4096

// kegIdentifyingPins are the values only a Plaato Keg reports. A device is not
// treated as a keg — and nothing is stored for it — until one of these arrives.
//
// Device metadata alone is deliberately not enough: a Plaato Airlock sends an
// indistinguishable metadata packet, and accepting that would create a phantom
// keg for every airlock on the network.
var kegIdentifyingPins = []string{
	"amount_left", "keg_temperature", "percent_of_beer_left", "is_pouring", "firmware_version",
}

// AmountConsumer is notified of a keg's remaining volume whenever the device
// reports it. The BarHelper integration uses this.
type AmountConsumer interface {
	KegAmount(kegID string, amount float64)
}

// Server accepts keg connections and ingests their data.
type Server struct {
	store          *store.Store
	bus            *events.Bus
	throttle       *store.LogThrottle
	includeUnknown bool
	registry       *Registry
	consumers      []AmountConsumer

	wg sync.WaitGroup
}

// NewServer returns a server that writes to st and publishes to bus.
func NewServer(st *store.Store, bus *events.Bus, includeUnknown bool, consumers ...AmountConsumer) *Server {
	return &Server{
		store:          st,
		bus:            bus,
		throttle:       store.NewLogThrottle(),
		includeUnknown: includeUnknown,
		registry:       NewRegistry(),
		consumers:      consumers,
	}
}

// Registry exposes the live connections, for the commander and the API.
func (s *Server) Registry() *Registry { return s.registry }

// Serve accepts connections until ln is closed, then waits for the in-flight
// connections to finish.
func (s *Server) Serve(ln net.Listener) error {
	defer s.wg.Wait()

	for {
		nc, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(nc)
		}()
	}
}

// Shutdown closes every live connection, unblocking the read loops.
func (s *Server) Shutdown() {
	s.registry.CloseAll()
	s.wg.Wait()
}

// connState is the per-connection state machine.
type connState struct {
	conn *Conn
	// kegID is the auth token, once the device has announced it.
	kegID string
	// confirmed records that this device has proved it is a keg.
	confirmed bool
	// pendingInternal holds device metadata that arrived before the device was
	// confirmed, so it can be stored once it is. The firmware sends its
	// metadata immediately after logging in, before any pin write.
	pendingInternal map[string]string
}

func (s *Server) handle(nc net.Conn) {
	c := &Conn{net: nc}
	state := &connState{conn: c}

	slog.Debug("keg connection opened", "remote", c.RemoteAddr())
	defer s.finish(state)

	var framer blynk.Framer
	buf := make([]byte, readBufferSize)

	for {
		if err := nc.SetReadDeadline(time.Now().Add(ReadTimeout)); err != nil {
			return
		}

		n, readErr := nc.Read(buf)
		if n > 0 {
			frames, frameErr := framer.Feed(buf[:n])

			// One acknowledgement per read, echoing the first frame's message
			// id. The firmware coalesces several messages into a single
			// segment and expects a single reply; it also validates that the
			// id matches, and will not complete its connection handshake
			// otherwise.
			if len(frames) > 0 {
				if err := c.Send(blynk.ResponseSuccess(frames[0].MsgID)); err != nil {
					slog.Debug("failed to acknowledge", "remote", c.RemoteAddr(), "error", err)
					return
				}
				s.ingest(state, frames)
			}

			if frameErr != nil {
				slog.Warn("closing connection on malformed stream",
					"remote", c.RemoteAddr(), "keg", state.kegID, "error", frameErr)
				return
			}
		}

		if readErr != nil {
			switch {
			case errors.Is(readErr, io.EOF):
				slog.Debug("keg closed the connection", "keg", state.kegID, "remote", c.RemoteAddr())
			case errors.Is(readErr, net.ErrClosed):
				slog.Debug("connection closed locally", "keg", state.kegID)
			case isTimeout(readErr):
				slog.Info("closing idle keg connection", "keg", state.kegID, "remote", c.RemoteAddr())
			default:
				slog.Warn("read failed", "keg", state.kegID, "remote", c.RemoteAddr(), "error", readErr)
			}
			return
		}
	}
}

// finish tears the connection down and clears any stale pouring state.
func (s *Server) finish(state *connState) {
	state.conn.Close()

	if state.kegID == "" {
		return
	}
	// Only the connection that currently owns this id may clean up after it:
	// on a reconnect the displaced read loop exits after its replacement has
	// registered, and must not disturb it.
	if !s.registry.Unregister(state.kegID, state.conn) {
		slog.Debug("displaced connection exiting", "keg", state.kegID)
		return
	}

	slog.Info("keg disconnected", "keg", state.kegID)

	if !state.confirmed {
		return
	}
	// The device stops reporting mid-pour, so without this the UI would show
	// it pouring forever.
	k, err := s.store.SetPouring(state.kegID, false)
	if err != nil {
		slog.Error("failed to clear pouring state", "keg", state.kegID, "error", err)
		return
	}
	s.bus.Publish(events.Event{Kind: events.KegUpdated, KegID: k.ID})
}

// ingest decodes a batch of frames and applies whatever it carries.
func (s *Server) ingest(state *connState, frames []blynk.Frame) {
	pkt := plaato.Decode(frames, s.includeUnknown)

	if pkt.DeviceID != "" && pkt.DeviceID != state.kegID {
		state.kegID = pkt.DeviceID
		slog.Info("keg identified", "keg", state.kegID, "remote", state.conn.RemoteAddr())
		s.registry.Register(state.kegID, state.conn)
	}

	if !state.confirmed && identifiesAKeg(pkt) {
		state.confirmed = true
		slog.Debug("device confirmed as a keg", "keg", state.kegID)
	}

	if !state.confirmed {
		// Hold metadata that arrived before the first pin write, so the setup
		// page's firmware and build fields are not lost.
		if len(pkt.Internal) > 0 {
			if state.pendingInternal == nil {
				state.pendingInternal = map[string]string{}
			}
			for k, v := range pkt.Internal {
				state.pendingInternal[k] = v
			}
		}
		return
	}

	if state.kegID == "" {
		slog.Warn("keg data arrived before the device announced its auth token", "remote", state.conn.RemoteAddr())
		return
	}

	if len(state.pendingInternal) > 0 {
		if pkt.Internal == nil {
			pkt.Internal = map[string]string{}
		}
		for k, v := range state.pendingInternal {
			if _, ok := pkt.Internal[k]; !ok {
				pkt.Internal[k] = v
			}
		}
		state.pendingInternal = nil
	}

	if len(pkt.Props) == 0 && len(pkt.Internal) == 0 && len(pkt.Unknown) == 0 {
		return
	}

	k, err := s.store.ApplyPacket(state.kegID, pkt)
	if err != nil {
		slog.Error("failed to store keg data", "keg", state.kegID, "error", err)
		return
	}

	// The reading check comes first so an empty row never consumes the
	// throttle slot that the first real reading needs.
	now := time.Now()
	if k.HasLoggableReading() && s.throttle.Allow(k.ID, now) {
		if err := s.store.AppendLog(k, now); err != nil {
			slog.Error("failed to record keg history", "keg", k.ID, "error", err)
		}
	}

	s.bus.Publish(events.Event{Kind: events.KegUpdated, KegID: k.ID})

	// Downstream integrations only care about a fresh volume reading.
	if amount, ok := pkt.Float("amount_left"); ok {
		for _, consumer := range s.consumers {
			consumer.KegAmount(k.ID, amount)
		}
	}
}

// identifiesAKeg reports whether the packet contains a value only a keg sends.
func identifiesAKeg(pkt plaato.Packet) bool {
	for _, name := range kegIdentifyingPins {
		if pkt.Has(name) {
			return true
		}
	}
	return false
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
