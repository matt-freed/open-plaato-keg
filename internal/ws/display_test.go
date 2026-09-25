package ws

import (
	"encoding/json"
	"testing"

	"github.com/matt-freed/open-plaato-keg/internal/blynk"
	"github.com/matt-freed/open-plaato-keg/internal/events"
	"github.com/matt-freed/open-plaato-keg/internal/plaato"
	"github.com/matt-freed/open-plaato-keg/internal/store"
)

// usVolume switches the UI to US volume units.
func usVolume(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.SetDisplayUnits(store.DisplayUnits{
		System:  store.DisplaySystemUS,
		Measure: store.DisplayMeasureVolume,
	}); err != nil {
		t.Fatalf("SetDisplayUnits: %v", err)
	}
}

// metricKeg stores a scale reporting 10 litres, which is 2.642 gal.
func metricKeg(t *testing.T, st *store.Store, id string) {
	t.Helper()
	frames := []blynk.Frame{}
	for i, body := range []string{
		"vw\x0051\x0010.000", "vw\x0071\x001", "vw\x0075\x002", "vw\x0088\x001",
	} {
		frames = append(frames, blynk.Frame{Cmd: blynk.CmdHardware, MsgID: uint16(i + 1), Body: []byte(body)})
	}
	if _, err := st.ApplyPacket(id, plaato.Decode(frames, false)); err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}
}

func decodeKeg(t *testing.T, msg Message) store.Keg {
	t.Helper()
	data, _ := json.Marshal(msg.Data)
	var k store.Keg
	if err := json.Unmarshal(data, &k); err != nil {
		t.Fatalf("decode keg: %v", err)
	}
	return k
}

// Both of the hub's send paths carry the display block, so a browser never
// receives a frame it cannot render in the chosen units.
func assertUSVolume(t *testing.T, k store.Keg) {
	t.Helper()
	if k.Display == nil {
		t.Fatal("no display block")
	}
	if got := *k.Display.AmountLeft; got < 2.64 || got > 2.65 {
		t.Errorf("display amount = %v, want ~2.642 gal", got)
	}
	if k.Display.AmountUnit != "gal" {
		t.Errorf("display unit = %q, want gal", k.Display.AmountUnit)
	}
	// The canonical reading stays in the device's own unit.
	if *k.AmountLeft != 10 {
		t.Errorf("amount_left = %v, want the device's own 10", *k.AmountLeft)
	}
}

func TestConnectSnapshotCarriesDisplayBlock(t *testing.T) {
	_, st, _, url := newTestHub(t)
	metricKeg(t, st, "keg-1")
	usVolume(t, st)

	conn := dialWS(t, url)
	assertUSVolume(t, decodeKeg(t, readMessage(t, conn)))
}

func TestBroadcastCarriesDisplayBlock(t *testing.T) {
	hub, st, bus, url := newTestHub(t)
	usVolume(t, st)

	conn := dialWS(t, url)
	waitForClients(t, hub, 1)

	metricKeg(t, st, "keg-1")
	bus.Publish(events.Event{Kind: events.KegUpdated, KegID: "keg-1"})

	assertUSVolume(t, decodeKeg(t, readMessage(t, conn)))
}
