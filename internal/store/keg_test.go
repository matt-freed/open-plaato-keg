package store

import (
	"errors"
	"testing"

	"github.com/matt-freed/open-plaato-keg/internal/blynk"
	"github.com/matt-freed/open-plaato-keg/internal/plaato"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// decode builds a packet from raw pin-write bodies, as the device would send
// them.
func decode(t *testing.T, bodies ...string) plaato.Packet {
	t.Helper()
	frames := make([]blynk.Frame, len(bodies))
	for i, b := range bodies {
		frames[i] = blynk.Frame{Cmd: blynk.CmdHardware, MsgID: uint16(i + 1), Body: []byte(b)}
	}
	return plaato.Decode(frames, false)
}

// Every property the decoder can produce must have somewhere to go, or data
// would be silently dropped as the pin map grows.
func TestEveryDecodedPropertyHasASetter(t *testing.T) {
	for _, name := range plaato.PersistentPropertyNames() {
		if _, ok := kegSetters[name]; !ok {
			t.Errorf("property %q is decoded but has no storage mapping", name)
		}
	}
}

func TestApplyPacketCreatesAndMerges(t *testing.T) {
	s := newTestStore(t)
	const id = "00000000000000000000000000000001"

	k, err := s.ApplyPacket(id, decode(t, "vw\x0051\x003.802", "vw\x0056\x0022.875"))
	if err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}
	if k.AmountLeft == nil || *k.AmountLeft != 3.802 {
		t.Errorf("AmountLeft = %v, want 3.802", k.AmountLeft)
	}
	if k.KegTemperature == nil || *k.KegTemperature != 22.875 {
		t.Errorf("KegTemperature = %v, want 22.875", k.KegTemperature)
	}
	if k.FirstSeen == 0 || k.LastSeen == 0 {
		t.Errorf("FirstSeen/LastSeen not set: %d/%d", k.FirstSeen, k.LastSeen)
	}

	// A later packet carries only what changed; everything else must survive.
	k, err = s.ApplyPacket(id, decode(t, "vw\x0051\x003.500"))
	if err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}
	if k.AmountLeft == nil || *k.AmountLeft != 3.5 {
		t.Errorf("AmountLeft = %v, want 3.5", k.AmountLeft)
	}
	if k.KegTemperature == nil || *k.KegTemperature != 22.875 {
		t.Errorf("KegTemperature = %v, want the merge to preserve 22.875", k.KegTemperature)
	}
}

// A pin the device has never sent must stay null rather than reading as zero,
// so the UI can tell "unknown" from a genuine zero on an uncalibrated scale.
func TestUnreportedPinsStayNull(t *testing.T) {
	s := newTestStore(t)
	k, err := s.ApplyPacket("keg-1", decode(t, "vw\x0051\x001.000"))
	if err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}
	if k.KegTemperature != nil {
		t.Errorf("KegTemperature = %v, want nil", *k.KegTemperature)
	}
	if k.FirmwareVersion != nil {
		t.Errorf("FirmwareVersion = %q, want nil", *k.FirmwareVersion)
	}
}

func TestApplyPacketPersists(t *testing.T) {
	s := newTestStore(t)
	const id = "keg-1"
	if _, err := s.ApplyPacket(id, decode(t, "vw\x0093\x002.0.10a", "vw\x0049\x00255")); err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}

	k, err := s.GetKeg(id)
	if err != nil {
		t.Fatalf("GetKeg: %v", err)
	}
	if k.FirmwareVersion == nil || *k.FirmwareVersion != "2.0.10a" {
		t.Errorf("FirmwareVersion = %v", k.FirmwareVersion)
	}
	if k.IsPouring == nil || !*k.IsPouring {
		t.Errorf("IsPouring = %v, want true (pin 49 reports 255 while pouring)", k.IsPouring)
	}
}

func TestInternalMetadataMerges(t *testing.T) {
	s := newTestStore(t)
	const id = "keg-1"

	pkt := plaato.Decode([]blynk.Frame{{
		Cmd: blynk.CmdInternal, MsgID: 2,
		Body: []byte("ver\x002.0.10a\x00dev\x00ESP32\x00"),
	}}, false)
	if _, err := s.ApplyPacket(id, pkt); err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}

	pkt = plaato.Decode([]blynk.Frame{{
		Cmd: blynk.CmdInternal, MsgID: 3,
		Body: []byte("tmpl\x00TMPL57889\x00"),
	}}, false)
	k, err := s.ApplyPacket(id, pkt)
	if err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}

	for key, want := range map[string]string{"ver": "2.0.10a", "dev": "ESP32", "tmpl": "TMPL57889"} {
		if k.Internal[key] != want {
			t.Errorf("Internal[%q] = %q, want %q", key, k.Internal[key], want)
		}
	}
}

func TestTransientPinsAreNotStored(t *testing.T) {
	s := newTestStore(t)
	k, err := s.ApplyPacket("keg-1", decode(t, "vw\x0060\x001", "vw\x0051\x001.000"))
	if err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}
	if k.AmountLeft == nil {
		t.Fatal("the non-transient pin in the same packet was dropped")
	}
}

func TestGetKegNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetKeg("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetKeg = %v, want ErrNotFound", err)
	}
}

func TestDeleteKeg(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.ApplyPacket("keg-1", decode(t, "vw\x0051\x001.000")); err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}
	if err := s.DeleteKeg("keg-1"); err != nil {
		t.Fatalf("DeleteKeg: %v", err)
	}
	if _, err := s.GetKeg("keg-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetKeg after delete = %v, want ErrNotFound", err)
	}
}

func TestListKegsOrderedBySortOrder(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []string{"a", "b", "c"} {
		if _, err := s.ApplyPacket(id, decode(t, "vw\x0051\x001.000")); err != nil {
			t.Fatalf("ApplyPacket: %v", err)
		}
	}
	order := map[string]int{"a": 2, "b": 0, "c": 1}
	for id, n := range order {
		if _, err := s.UpdateKeg(id, func(k *Keg) { k.SortOrder = n }); err != nil {
			t.Fatalf("UpdateKeg: %v", err)
		}
	}

	kegs, err := s.ListKegs()
	if err != nil {
		t.Fatalf("ListKegs: %v", err)
	}
	want := []string{"b", "c", "a"}
	if len(kegs) != len(want) {
		t.Fatalf("got %d kegs, want %d", len(kegs), len(want))
	}
	for i, k := range kegs {
		if k.ID != want[i] {
			t.Errorf("position %d = %q, want %q", i, k.ID, want[i])
		}
	}
}

func TestDeriveBeerLeftUnit(t *testing.T) {
	i64 := func(n int64) *int64 { return &n }
	tests := []struct {
		name                       string
		unit, measureUnit, kegMode *int64
		deviceUnit                 string
		want                       string
	}{
		{"metric weight", i64(1), i64(1), i64(1), "", "kg"},
		{"metric volume", i64(1), i64(2), i64(1), "", "litre"},
		{"us weight", i64(2), i64(1), i64(1), "", "lbs"},
		{"us volume", i64(2), i64(2), i64(1), "", "gal"},
		// CO2 is weighed whatever the measure mode says.
		{"metric co2 ignores volume mode", i64(1), i64(2), i64(2), "", "kg CO₂"},
		{"us co2 ignores volume mode", i64(2), i64(2), i64(2), "", "lbs CO₂"},
		// Before the unit is known, the device's own label is the best guess.
		{"unknown unit falls back to device", nil, nil, nil, "kg", "kg"},
		{"unknown unit and no device label", nil, nil, nil, "", "litre"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &Keg{Unit: tt.unit, MeasureUnit: tt.measureUnit, KegMode: tt.kegMode}
			if tt.deviceUnit != "" {
				k.BeerLeftUnitDevice = &tt.deviceUnit
			}
			if got := k.DeriveBeerLeftUnit(); got != tt.want {
				t.Errorf("DeriveBeerLeftUnit = %q, want %q", got, tt.want)
			}
		})
	}
}

// The device's own pin 74 must never override the derived label, which is what
// stops a stale unit showing after a mode change.
func TestDerivedUnitOverridesDeviceReportedUnit(t *testing.T) {
	s := newTestStore(t)
	k, err := s.ApplyPacket("keg-1", decode(t,
		"vw\x0074\x00litre", // device says litres
		"vw\x0071\x001",     // metric
		"vw\x0075\x001",     // weight mode
	))
	if err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}
	if k.BeerLeftUnit != "kg" {
		t.Errorf("BeerLeftUnit = %q, want kg", k.BeerLeftUnit)
	}
}

func TestImplausiblePourIsRejected(t *testing.T) {
	s := newTestStore(t)
	const id = "keg-1"

	// Establish metric weight mode, so pours are bounded to 0.05-1.4 kg.
	if _, err := s.ApplyPacket(id, decode(t, "vw\x0071\x001", "vw\x0075\x001")); err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}

	k, err := s.ApplyPacket(id, decode(t, "vw\x0059\x000.500"))
	if err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}
	if k.LastPour == nil || *k.LastPour != 0.5 {
		t.Fatalf("LastPour = %v, want a plausible 0.5 to be accepted", k.LastPour)
	}

	// A compressor kicking in looks like an enormous pour.
	k, err = s.ApplyPacket(id, decode(t, "vw\x0059\x0050.000"))
	if err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}
	if k.LastPour == nil || *k.LastPour != 0.5 {
		t.Errorf("LastPour = %v, want the spike rejected and 0.5 kept", k.LastPour)
	}

	// Scale noise near zero is equally bogus.
	k, err = s.ApplyPacket(id, decode(t, "vw\x0059\x000.001"))
	if err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}
	if k.LastPour == nil || *k.LastPour != 0.5 {
		t.Errorf("LastPour = %v, want the noise rejected and 0.5 kept", k.LastPour)
	}
}

func TestSetPouring(t *testing.T) {
	s := newTestStore(t)
	const id = "keg-1"
	if _, err := s.ApplyPacket(id, decode(t, "vw\x0049\x00255", "vw\x0051\x002.000")); err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}

	k, err := s.SetPouring(id, false)
	if err != nil {
		t.Fatalf("SetPouring: %v", err)
	}
	if k.IsPouring == nil || *k.IsPouring {
		t.Errorf("IsPouring = %v, want false", k.IsPouring)
	}
	if k.AmountLeft == nil || *k.AmountLeft != 2 {
		t.Errorf("AmountLeft = %v, want it untouched at 2", k.AmountLeft)
	}
}

func TestUnknownPinsLandInExtra(t *testing.T) {
	s := newTestStore(t)
	pkt := plaato.Decode([]blynk.Frame{{
		Cmd: blynk.CmdHardware, MsgID: 1, Body: []byte("vw\x0078\x0042"),
	}}, true)
	k, err := s.ApplyPacket("keg-1", pkt)
	if err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}
	if k.Extra["_hardware_vw_78"] != "42" {
		t.Errorf("Extra = %v, want _hardware_vw_78=42", k.Extra)
	}
}
