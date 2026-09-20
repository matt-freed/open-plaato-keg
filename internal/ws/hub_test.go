package ws

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/matt-freed/open-plaato-keg/internal/blynk"
	"github.com/matt-freed/open-plaato-keg/internal/events"
	"github.com/matt-freed/open-plaato-keg/internal/plaato"
	"github.com/matt-freed/open-plaato-keg/internal/store"
)

func newTestHub(t *testing.T) (*Hub, *store.Store, *events.Bus, string) {
	t.Helper()

	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	hub := NewHub(st)
	bus := events.NewBus()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go hub.Run(ctx, bus)

	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)

	return hub, st, bus, "ws" + strings.TrimPrefix(srv.URL, "http")
}

func dialWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

func readMessage(t *testing.T, conn *websocket.Conn) Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return msg
}

func storeKeg(t *testing.T, st *store.Store, id, body string) {
	t.Helper()
	pkt := plaato.Decode([]blynk.Frame{{Cmd: blynk.CmdHardware, MsgID: 1, Body: []byte(body)}}, false)
	if _, err := st.ApplyPacket(id, pkt); err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}
}

func waitForClients(t *testing.T, hub *Hub, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if hub.Clients() == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d clients, have %d", n, hub.Clients())
}

// A page that connects between updates must not sit blank until the next one.
func TestConnectSendsCurrentState(t *testing.T) {
	hub, st, _, url := newTestHub(t)
	storeKeg(t, st, "keg-1", "vw\x0051\x003.802")

	conn := dialWS(t, url)
	msg := readMessage(t, conn)

	if msg.Type != TypeKeg {
		t.Fatalf("Type = %q, want %q", msg.Type, TypeKeg)
	}
	data, _ := json.Marshal(msg.Data)
	var keg store.Keg
	if err := json.Unmarshal(data, &keg); err != nil {
		t.Fatalf("decode keg: %v", err)
	}
	if keg.ID != "keg-1" || keg.AmountLeft == nil || *keg.AmountLeft != 3.802 {
		t.Errorf("keg = %+v", keg)
	}
	_ = hub
}

func TestBroadcastOnKegUpdate(t *testing.T) {
	hub, st, bus, url := newTestHub(t)
	conn := dialWS(t, url)
	waitForClients(t, hub, 1)

	storeKeg(t, st, "keg-1", "vw\x0051\x001.500")
	bus.Publish(events.Event{Kind: events.KegUpdated, KegID: "keg-1"})

	msg := readMessage(t, conn)
	if msg.Type != TypeKeg {
		t.Fatalf("Type = %q, want %q", msg.Type, TypeKeg)
	}
	data, _ := json.Marshal(msg.Data)
	var keg store.Keg
	if err := json.Unmarshal(data, &keg); err != nil {
		t.Fatalf("decode keg: %v", err)
	}
	if keg.AmountLeft == nil || *keg.AmountLeft != 1.5 {
		t.Errorf("AmountLeft = %v, want 1.5", keg.AmountLeft)
	}
}

func TestBroadcastKegRemoved(t *testing.T) {
	hub, _, bus, url := newTestHub(t)
	conn := dialWS(t, url)
	waitForClients(t, hub, 1)

	bus.Publish(events.Event{Kind: events.KegRemoved, KegID: "keg-1"})

	msg := readMessage(t, conn)
	if msg.Type != TypeKegRemoved {
		t.Fatalf("Type = %q, want %q", msg.Type, TypeKegRemoved)
	}
	if msg.ID != "keg-1" {
		t.Errorf("ID = %q, want keg-1", msg.ID)
	}
}

// Every message carries a type, so clients dispatch on it rather than
// inferring meaning from a missing field.
func TestEveryMessageIsTagged(t *testing.T) {
	hub, st, bus, url := newTestHub(t)
	storeKeg(t, st, "keg-1", "vw\x0051\x001.000")

	conn := dialWS(t, url)
	waitForClients(t, hub, 1)

	// Drain the initial state.
	readMessage(t, conn)

	bus.Publish(events.Event{Kind: events.KegUpdated, KegID: "keg-1"})
	bus.Publish(events.Event{Kind: events.KegRemoved, KegID: "keg-1"})

	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, ok := raw["type"]; !ok {
			t.Errorf("message has no type field: %s", data)
		}
	}
}

func TestBroadcastReachesEveryClient(t *testing.T) {
	hub, st, bus, url := newTestHub(t)
	storeKeg(t, st, "keg-1", "vw\x0051\x001.000")

	first := dialWS(t, url)
	second := dialWS(t, url)
	waitForClients(t, hub, 2)

	// Drain the initial state on both.
	readMessage(t, first)
	readMessage(t, second)

	bus.Publish(events.Event{Kind: events.KegRemoved, KegID: "keg-1"})

	for i, conn := range []*websocket.Conn{first, second} {
		if msg := readMessage(t, conn); msg.Type != TypeKegRemoved {
			t.Errorf("client %d got %q, want %q", i, msg.Type, TypeKegRemoved)
		}
	}
}

func TestDisconnectRemovesClient(t *testing.T) {
	hub, _, _, url := newTestHub(t)
	conn := dialWS(t, url)
	waitForClients(t, hub, 1)

	conn.Close(websocket.StatusNormalClosure, "")
	waitForClients(t, hub, 0)
}

// An update for a keg that has since been deleted must not crash the hub or
// broadcast a null record.
func TestUpdateForMissingKegIsSkipped(t *testing.T) {
	hub, _, bus, url := newTestHub(t)
	conn := dialWS(t, url)
	waitForClients(t, hub, 1)

	bus.Publish(events.Event{Kind: events.KegUpdated, KegID: "gone"})
	bus.Publish(events.Event{Kind: events.KegRemoved, KegID: "gone"})

	// Only the removal should arrive.
	if msg := readMessage(t, conn); msg.Type != TypeKegRemoved {
		t.Errorf("Type = %q, want the update for a missing keg to be skipped", msg.Type)
	}
}
