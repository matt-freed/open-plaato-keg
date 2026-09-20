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
}

// NewHub returns a hub that reads records from st.
func NewHub(st *store.Store) *Hub {
	return &Hub{store: st, clients: map[*client]struct{}{}}
}

// Run forwards bus events to connected clients until ctx is cancelled.
func (h *Hub) Run(ctx context.Context, bus *events.Bus) {
	sub, cancel := bus.Subscribe()
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub:
			if !ok {
				return
			}
			h.handle(e)
		}
	}
}

func (h *Hub) handle(e events.Event) {
	switch e.Kind {
	case events.KegUpdated:
		// The event carries only an id; the current record is read here so
		// every client receives the same complete state.
		k, err := h.store.GetKeg(e.KegID)
		if err != nil {
			slog.Debug("skipping broadcast for a keg that is no longer stored",
				"keg", e.KegID, "error", err)
			return
		}
		h.Broadcast(Message{Type: TypeKeg, Data: k})
	case events.KegRemoved:
		h.Broadcast(Message{Type: TypeKegRemoved, ID: e.KegID})
	}
}

// Broadcast sends a message to every connected client.
func (h *Hub) Broadcast(msg Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
			// The writer notices the full buffer and closes the connection.
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
