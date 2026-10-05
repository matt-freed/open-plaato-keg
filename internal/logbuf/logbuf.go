// Package logbuf keeps the most recent log records in memory so the UI can
// show them without shell access to the server.
package logbuf

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Capacity is how many records the buffer keeps. Older ones are discarded,
// which bounds the memory the buffer can use to well under a megabyte.
const Capacity = 1000

// Attr is one key and value from a record, flattened to text when captured so
// the buffer never holds a reference to a live object.
type Attr struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Record is one captured log line.
type Record struct {
	Seq     uint64    `json:"seq"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Attrs   []Attr    `json:"attrs"`
}

// Buffer is a fixed-size ring of the most recent records.
type Buffer struct {
	mu      sync.Mutex
	records []Record
	next    int    // where the next record is written once the ring is full
	seq     uint64 // the last sequence number handed out
}

// New returns an empty buffer.
func New() *Buffer {
	return &Buffer{records: make([]Record, 0, Capacity)}
}

func (b *Buffer) add(r Record) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.seq++
	r.Seq = b.seq
	if len(b.records) < Capacity {
		b.records = append(b.records, r)
		return
	}
	b.records[b.next] = r
	b.next = (b.next + 1) % Capacity
}

// Page is what Since returns.
type Page struct {
	// Records are those after the requested sequence number, oldest first.
	Records []Record
	// Oldest is the sequence number of the oldest record still buffered, and
	// Latest the newest; both are 0 while the buffer is empty. A caller whose
	// last seen number is below Oldest-1 missed records that were discarded,
	// and one whose number is above Latest is looking at an earlier process.
	Oldest, Latest uint64
}

// Since returns the buffered records with a sequence number above after.
func (b *Buffer) Since(after uint64) Page {
	b.mu.Lock()
	defer b.mu.Unlock()

	page := Page{Records: []Record{}, Latest: b.seq}
	if len(b.records) == 0 {
		return page
	}
	page.Oldest = b.records[b.next].Seq // b.next is 0 until the ring is full
	for i := range b.records {
		r := b.records[(b.next+i)%len(b.records)]
		if r.Seq > after {
			page.Records = append(page.Records, r)
		}
	}
	return page
}

// Handler is an slog.Handler that passes every record to an inner handler
// and also keeps a copy in a Buffer. It captures exactly what the inner
// handler is enabled for.
type Handler struct {
	inner  slog.Handler
	buf    *Buffer
	attrs  []Attr // from WithAttrs, already prefixed with their group
	prefix string // the open groups, as "a.b."
}

// NewHandler returns a handler that writes to inner and captures into buf.
func NewHandler(inner slog.Handler, buf *Buffer) *Handler {
	return &Handler{inner: inner, buf: buf}
}

// Enabled reports whether the inner handler wants records at level.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle writes r to the inner handler, then captures it.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	err := h.inner.Handle(ctx, r)

	attrs := make([]Attr, len(h.attrs), len(h.attrs)+r.NumAttrs())
	copy(attrs, h.attrs)
	r.Attrs(func(a slog.Attr) bool {
		attrs = appendAttr(attrs, h.prefix, a)
		return true
	})
	h.buf.add(Record{
		Time:    r.Time,
		Level:   r.Level.String(),
		Message: r.Message,
		Attrs:   attrs,
	})
	return err
}

// WithAttrs returns a handler that adds attrs to every record.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	next := *h
	next.inner = h.inner.WithAttrs(attrs)
	next.attrs = append([]Attr(nil), h.attrs...)
	for _, a := range attrs {
		next.attrs = appendAttr(next.attrs, h.prefix, a)
	}
	return &next
}

// WithGroup returns a handler that nests later attributes under name.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := *h
	next.inner = h.inner.WithGroup(name)
	next.prefix = h.prefix + name + "."
	return &next
}

// appendAttr flattens a, expanding groups into dotted keys as the text
// handler does.
func appendAttr(out []Attr, prefix string, a slog.Attr) []Attr {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return out
	}
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		if len(group) == 0 {
			return out
		}
		if a.Key != "" {
			prefix += a.Key + "."
		}
		for _, g := range group {
			out = appendAttr(out, prefix, g)
		}
		return out
	}
	return append(out, Attr{Key: prefix + a.Key, Value: a.Value.String()})
}
