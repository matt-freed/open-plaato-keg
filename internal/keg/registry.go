package keg

import (
	"errors"
	"log/slog"
	"net"
	"sort"
	"sync"
)

// ErrNotConnected is returned when a command targets a keg that has no live
// connection.
var ErrNotConnected = errors.New("keg is not connected")

// Conn is a live connection to one keg.
type Conn struct {
	net  net.Conn
	once sync.Once

	// writeMu serialises writes, since commands from HTTP handlers race with
	// the acknowledgements written by the read loop.
	writeMu sync.Mutex
}

// Send writes a frame to the device.
func (c *Conn) Send(frame []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.net.Write(frame)
	return err
}

// Close closes the underlying socket. Safe to call more than once.
func (c *Conn) Close() {
	c.once.Do(func() { c.net.Close() })
}

// RemoteAddr identifies the device's address, for logging.
func (c *Conn) RemoteAddr() string { return c.net.RemoteAddr().String() }

// Registry tracks the live connection for each keg, keyed by auth token.
type Registry struct {
	mu    sync.RWMutex
	conns map[string]*Conn
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{conns: map[string]*Conn{}}
}

// Register makes c the connection for id, displacing any existing one.
//
// A keg that reconnects before the server has noticed the old socket is gone
// would otherwise be unreachable, so the stale connection is closed rather
// than the new one rejected.
func (r *Registry) Register(id string, c *Conn) {
	r.mu.Lock()
	stale := r.conns[id]
	r.conns[id] = c
	r.mu.Unlock()

	if stale != nil && stale != c {
		slog.Info("keg reconnected, closing the previous connection",
			"keg", id, "previous", stale.RemoteAddr(), "current", c.RemoteAddr())
		stale.Close()
	}
}

// Unregister removes c, but only if it is still the registered connection.
//
// The guard matters on reconnect: the displaced connection's read loop exits
// after the new one has registered, and must not remove its replacement.
func (r *Registry) Unregister(id string, c *Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conns[id] != c {
		return false
	}
	delete(r.conns, id)
	return true
}

// Lookup returns the live connection for id.
func (r *Registry) Lookup(id string) (*Conn, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if c, ok := r.conns[id]; ok {
		return c, nil
	}
	return nil, ErrNotConnected
}

// IDs returns the ids of every connected keg, sorted.
func (r *Registry) IDs() []string {
	r.mu.RLock()
	ids := make([]string, 0, len(r.conns))
	for id := range r.conns {
		ids = append(ids, id)
	}
	r.mu.RUnlock()

	sort.Strings(ids)
	return ids
}

// CloseAll closes every live connection.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	conns := make([]*Conn, 0, len(r.conns))
	for _, c := range r.conns {
		conns = append(conns, c)
	}
	r.conns = map[string]*Conn{}
	r.mu.Unlock()

	for _, c := range conns {
		c.Close()
	}
}
