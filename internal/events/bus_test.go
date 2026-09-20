package events

import (
	"sync"
	"testing"
	"time"
)

func TestPublishReachesEverySubscriber(t *testing.T) {
	bus := NewBus()
	first, cancelFirst := bus.Subscribe()
	defer cancelFirst()
	second, cancelSecond := bus.Subscribe()
	defer cancelSecond()

	bus.Publish(Event{Kind: KegUpdated, KegID: "keg-1"})

	for i, sub := range []<-chan Event{first, second} {
		select {
		case e := <-sub:
			if e.Kind != KegUpdated || e.KegID != "keg-1" {
				t.Errorf("subscriber %d got %+v", i, e)
			}
		case <-time.After(time.Second):
			t.Errorf("subscriber %d received nothing", i)
		}
	}
}

func TestCancelStopsDelivery(t *testing.T) {
	bus := NewBus()
	sub, cancel := bus.Subscribe()

	if bus.Subscribers() != 1 {
		t.Fatalf("Subscribers = %d, want 1", bus.Subscribers())
	}
	cancel()
	if bus.Subscribers() != 0 {
		t.Errorf("Subscribers = %d after cancel, want 0", bus.Subscribers())
	}

	// The channel is closed, so a read returns immediately.
	if _, open := <-sub; open {
		t.Error("the subscription channel was not closed")
	}

	// Publishing after a cancel must not panic on the closed channel.
	bus.Publish(Event{Kind: KegUpdated, KegID: "keg-1"})
}

func TestCancelIsIdempotent(t *testing.T) {
	bus := NewBus()
	_, cancel := bus.Subscribe()
	cancel()
	cancel()
}

// Publishing must never block, or a stalled browser tab would hold up the TCP
// ingest path that produced the event.
func TestPublishDoesNotBlockOnASlowSubscriber(t *testing.T) {
	bus := NewBus()
	_, cancel := bus.Subscribe()
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < subscriberBuffer*10; i++ {
			bus.Publish(Event{Kind: KegUpdated, KegID: "keg-1"})
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a subscriber that was not reading")
	}
}

func TestConcurrentSubscribeAndPublish(t *testing.T) {
	bus := NewBus()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			sub, cancel := bus.Subscribe()
			defer cancel()
			select {
			case <-sub:
			case <-time.After(100 * time.Millisecond):
			}
		}()
		go func() {
			defer wg.Done()
			bus.Publish(Event{Kind: KegUpdated, KegID: "keg-1"})
		}()
	}
	wg.Wait()
}
