// Package events carries in-process notifications from the ingest path to
// anything that wants to react to them.
package events

import (
	"log/slog"
	"sync"
)

// Kind identifies what happened.
type Kind string

const (
	// KegUpdated is published whenever a keg's stored state changes.
	KegUpdated Kind = "keg"
	// KegRemoved is published when a keg is deleted.
	KegRemoved Kind = "keg_removed"
)

// Event is a single notification.
type Event struct {
	Kind  Kind
	KegID string
}

// subscriberBuffer is how many events a slow subscriber may fall behind by
// before it starts losing them.
const subscriberBuffer = 32

// Bus fans events out to every subscriber.
//
// Publishing never blocks: a subscriber that cannot keep up loses events
// rather than stalling the TCP ingest path that produced them.
type Bus struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

// NewBus returns an empty bus.
func NewBus() *Bus {
	return &Bus{subs: map[chan Event]struct{}{}}
}

// Subscribe returns a channel of events and a function that stops the
// subscription and closes the channel.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)

	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
			close(ch)
		})
	}
	return ch, cancel
}

// Publish delivers e to every current subscriber.
func (b *Bus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.subs {
		select {
		case ch <- e:
		default:
			slog.Warn("dropping event for a subscriber that is not keeping up",
				"kind", e.Kind, "keg", e.KegID)
		}
	}
}

// Subscribers reports how many subscriptions are active.
func (b *Bus) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
