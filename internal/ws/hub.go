// Package ws broadcasts keg updates to browser clients over WebSocket.
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/matt-freed/open-plaato-keg/internal/events"
	"github.com/matt-freed/open-plaato-keg/internal/store"
)

// Message is one broadcast frame.
//
// Every message is tagged, so a client can dispatch on Type alone.
type Message struct {
	Type string `json:"type"`
	// Data carries the full record for an update.
	Data any `json:"data,omitempty"`
	// ID identifies the subject of a removal.
	ID string `json:"id,omitempty"`
}

// Message types.
const (
	TypeKeg        = "keg"
	TypeKegRemoved = "keg_removed"
)

// clientBuffer is how many messages a client may fall behind by before it is
// disconnected. A browser tab that has been throttled in the background should
// not hold up the broadcast.
const clientBuffer = 16

// writeTimeout caps how long a single frame write may take.
const writeTimeout = 10 * time.Second

type client struct {
	send chan Message
}

// Hub holds the connected clients and broadcasts to them.
type Hub struct {
	store *store.Store

	mu      sync.RWMutex
	clients map[*client]struct{}

	// pending is the set of kegs with changes not yet broadcast, keyed by id
	// so repeated changes to one keg collapse. It is bounded by the number of
	// kegs, however fast they report.
	pendingMu sync.Mutex
	pending   map[string]events.Kind
	// wake signals the flusher that pending is not empty.
	wake chan struct{}
}

// NewHub returns a hub that reads records from st.
func NewHub(st *store.Store) *Hub {
	return &Hub{
		store:   st,
		clients: map[*client]struct{}{},
		pending: map[string]events.Kind{},
		wake:    make(chan struct{}, 1),
	}
}

// mark records that a keg has changed and wakes the flusher.
func (h *Hub) mark(e events.Event) {
	h.pendingMu.Lock()
	h.pending[e.KegID] = e.Kind
	h.pendingMu.Unlock()

	select {
	case h.wake <- struct{}{}:
	default: // a flush is already pending
	}
}

// takePending returns the changes waiting to be broadcast and clears them.
func (h *Hub) takePending() map[string]events.Kind {
	h.pendingMu.Lock()
	defer h.pendingMu.Unlock()

	if len(h.pending) == 0 {
		return nil
	}
	taken := h.pending
	h.pending = make(map[string]events.Kind, len(taken))
	return taken
}

// Run forwards bus events to connected clients until ctx is cancelled.
//
// Events are collapsed by keg before any work is done for them. Only a keg's
// current state is ever sent, so a burst of twenty updates for one keg costs a
// single database read and a single broadcast rather than twenty of each. That
// matters when every keg reconnects at once — the work per event was
// previously slow enough that the event bus filled and discarded updates.
func (h *Hub) Run(ctx context.Context, bus *events.Bus) {
	sub, cancel := bus.Subscribe()
	defer cancel()

	// Taking events off the bus is kept separate from acting on them. A flush
	// reads the database, which contends with every connected keg writing to
	// it, and while the flush ran the bus would fill and start discarding
	// updates. Recording an event is a map write, so this keeps up regardless.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-sub:
				if !ok {
					return
				}
				h.mark(e)
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-h.wake:
			h.flush(h.takePending())
		}
	}
}

// flush sends one message per keg that has changes waiting.
func (h *Hub) flush(pending map[string]events.Kind) {
	if len(pending) == 0 {
		return
	}
	// With nobody listening there is nothing to deliver, and reading the
	// database would be pure waste — a client that connects is sent the full
	// current state anyway.
	if h.Clients() == 0 {
		clear(pending)
		return
	}

	for kegID, kind := range pending {
		switch kind {
		case events.KegRemoved:
			h.Broadcast(Message{Type: TypeKegRemoved, ID: kegID})
		case events.KegUpdated:
			// The event carries only an id; the current record is read here so
			// every client receives the same complete state.
			k, err := h.store.GetKeg(kegID)
			if err != nil {
				slog.Debug("skipping broadcast for a keg that is no longer stored",
					"keg", kegID, "error", err)
				continue
			}
			h.Broadcast(Message{Type: TypeKeg, Data: k})
		}
	}
	clear(pending)
}

// Broadcast sends a message to every connected client.
func (h *Hub) Broadcast(msg Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
			// Every keg message carries that keg's full state, so a client
			// that misses one is corrected by the next.
			slog.Debug("dropping a message for a client that is not keeping up")
		}
	}
}

// Clients reports how many clients are connected.
func (h *Hub) Clients() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// ServeHTTP upgrades a request to a WebSocket and streams updates to it.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The UI is served from the same origin as this endpoint, but a user
		// may reach it by IP, hostname or mDNS name, any of which would fail a
		// strict origin check.
		InsecureSkipVerify: true,
	})
	if err != nil {
		slog.Debug("websocket upgrade failed", "error", err)
		return
	}

	c := &client{send: make(chan Message, clientBuffer)}
	h.add(c)
	defer h.remove(c)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Clients only listen, but reading is what surfaces a closed connection
	// and honours the protocol's close handshake.
	go func() {
		defer cancel()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()

	// Send the current state immediately, so a page that connects between
	// updates is not blank until the next one.
	if kegs, err := h.store.ListKegs(); err == nil {
		for _, k := range kegs {
			select {
			case c.send <- Message{Type: TypeKeg, Data: k}:
			default:
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "")
			return
		case msg := <-c.send:
			if err := writeMessage(ctx, conn, msg); err != nil {
				if !errors.Is(err, context.Canceled) {
					slog.Debug("websocket write failed", "error", err)
				}
				conn.Close(websocket.StatusInternalError, "write failed")
				return
			}
		}
	}
}

func writeMessage(ctx context.Context, conn *websocket.Conn, msg Message) error {
	encoded, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, encoded)
}
