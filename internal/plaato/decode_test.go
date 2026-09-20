package plaato

import (
	"testing"

	"github.com/matt-freed/open-plaato-keg/internal/blynk"
)

// decodeBody runs a single frame body through the decoder.
func decodeBody(t *testing.T, cmd blynk.Command, body string, includeUnknown bool) Packet {
	t.Helper()
	return Decode([]blynk.Frame{{Cmd: cmd, MsgID: 1, Body: []byte(body)}}, includeUnknown)
}

func onlyProp(t *testing.T, pkt Packet) Property {
	t.Helper()
	if len(pkt.Props) != 1 {
		t.Fatalf("got %d properties, want 1: %+v", len(pkt.Props), pkt.Props)
	}
	return pkt.Props[0]
}

func TestSplitBodyDropsEmptyFields(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"pin write", "vw\x0051\x000.040", []string{"vw", "51", "0.040"}},
		{"trailing NUL", "ver\x002.0.10a\x00", []string{"ver", "2.0.10a"}},
		{"empty value collapses", "vw\x0052\x00", []string{"vw", "52"}},
		{"leading NUL", "\x00vw\x0051", []string{"vw", "51"}},
		{"no separator", "0.040", []string{"0.040"}},
		{"empty body", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitBody([]byte(tt.in))
			if len(got) != len(tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %q, want %q", got, tt.want)
				}
			}
		})
	}
}

// A pin write with an empty value collapses to two fields and must be dropped
// rather than stored as an empty value.
func TestEmptyPinValueIsDropped(t *testing.T) {
	pkt := decodeBody(t, blynk.CmdHardware, "vw\x0052\x00", true)
	if len(pkt.Props) != 0 {
		t.Errorf("got %+v, want no properties", pkt.Props)
	}
	if len(pkt.Unknown) != 0 {
		t.Errorf("got unknown %v, want none", pkt.Unknown)
	}
}

func TestDecodeHardwarePins(t *testing.T) {
	tests := []struct {
		body string
		name string
		want any
	}{
		{"vw\x0047\x000.18L", "last_pour_string", "0.18L"},
		{"vw\x0048\x0026.000", "percent_of_beer_left", 26.0},
		{"vw\x0049\x00255", "is_pouring", true},
		{"vw\x0049\x000", "is_pouring", false},
		{"vw\x0051\x000.040", "amount_left", 0.04},
		{"vw\x0052\x00-7.500", "temperature_offset", -7.5},
		{"vw\x0053\x00200621", "weight_raw", 200621.0},
		{"vw\x0056\x0022.250", "keg_temperature", 22.25},
		{"vw\x0059\x000.184", "last_pour", 0.184},
		{"vw\x0062\x000.000", "empty_keg_weight", 0.0},
		{"vw\x0063\x00-44.000", "temperature_correction", -44.0},
		{"vw\x0065\x001000", "device_og", 1000.0},
		{"vw\x0066\x001000", "device_fg", 1000.0},
		{"vw\x0068\x005.403", "calculated_abv", 5.403},
		{"vw\x0070\x005.40%", "calculated_alcohol_string", "5.40%"},
		{"vw\x0071\x001", "unit", int64(1)},
		{"vw\x0071\x002", "unit", int64(2)},
		{"vw\x0073\x00kg", "weight_unit", "kg"},
		{"vw\x0074\x00kg", "beer_left_unit", "kg"},
		{"vw\x0075\x001", "measure_unit", int64(1)},
		{"vw\x0076\x0020.059", "max_keg_volume", 20.059},
		{"vw\x0081\x0052", "wifi_signal_strength", int64(52)},
		{"vw\x0082\x00litre", "volume_unit", "litre"},
		{"vw\x0083\x000", "leak_detection", int64(0)},
		{"vw\x0086\x000.000", "min_temperature", 0.0},
		{"vw\x0087\x0030.000", "max_temperature", 30.0},
		{"vw\x0088\x001", "keg_mode", int64(1)},
		{"vw\x0088\x002", "keg_mode", int64(2)},
		{"vw\x0089\x004", "sensitivity", int64(4)},
		{"vw\x0093\x002.0.10a", "firmware_version", "2.0.10a"},
		// The firmware sends the degree sign as UTF-8 C2 B0.
		{"vw\x0069\x0022.13°C", "keg_temperature_string", "22.13°C"},
		{"vw\x0080\x00°C", "temperature_unit", "°C"},
		{"vw\x0092\x0074.44°C", "chip_temperature_string", "74.44°C"},
	}
	for _, tt := range tests {
		t.Run(tt.name+"="+tt.body, func(t *testing.T) {
			p := onlyProp(t, decodeBody(t, blynk.CmdHardware, tt.body, false))
			if p.Name != tt.name {
				t.Fatalf("Name = %q, want %q", p.Name, tt.name)
			}
			if p.Value != tt.want {
				t.Errorf("Value = %#v (%T), want %#v (%T)", p.Value, p.Value, tt.want, tt.want)
			}
		})
	}
}

// Property writes (cmd 19) carry slider bounds. Pin 86 is the min-temperature
// slider, so its "max" is that slider's upper bound — not a maximum
// temperature.
func TestDecodePropertyPins(t *testing.T) {
	tests := []struct {
		body string
		name string
		want float64
	}{
		{"51\x00max\x0020.059", "max_keg_volume", 20.059},
		{"86\x00min\x00-20.000", "min_temperature", -20},
		{"86\x00max\x0040.000", "min_temperature_max", 40},
		{"87\x00min\x00-20.000", "max_temperature_min", -20},
		{"87\x00max\x0030.000", "max_temperature", 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := onlyProp(t, decodeBody(t, blynk.CmdProperty, tt.body, false))
			if p.Name != tt.name || p.Value != tt.want {
				t.Errorf("got %s=%v, want %s=%v", p.Name, p.Value, tt.name, tt.want)
			}
		})
	}
}

// A label property write is not tracked and must not become a property.
func TestUnhandledPropertyIsIgnored(t *testing.T) {
	pkt := decodeBody(t, blynk.CmdProperty, "47\x00label\x00Last pour", false)
	if len(pkt.Props) != 0 {
		t.Errorf("got %+v, want no properties", pkt.Props)
	}
}

func TestTransientPinsAreMarked(t *testing.T) {
	for _, body := range []string{"vw\x0060\x001", "vw\x0072\x001", "vw\x0061\x000.1"} {
		p := onlyProp(t, decodeBody(t, blynk.CmdHardware, body, false))
		if !p.Transient {
			t.Errorf("%q decoded to %s, which is not marked transient", body, p.Name)
		}
	}
}

func TestUnknownPins(t *testing.T) {
	t.Run("dropped by default", func(t *testing.T) {
		pkt := decodeBody(t, blynk.CmdHardware, "vw\x0078\x0042", false)
		if len(pkt.Props) != 0 || len(pkt.Unknown) != 0 {
			t.Errorf("got props %+v unknown %v, want both empty", pkt.Props, pkt.Unknown)
		}
	})
	t.Run("captured when enabled", func(t *testing.T) {
		pkt := decodeBody(t, blynk.CmdHardware, "vw\x0078\x0042", true)
		if got := pkt.Unknown["_hardware_vw_78"]; got != "42" {
			t.Errorf("Unknown = %v, want _hardware_vw_78=42", pkt.Unknown)
		}
	})
}

// A value that will not parse as its pin's type is dropped rather than stored
// as a zero.
func TestUnparseableValueIsDropped(t *testing.T) {
	pkt := decodeBody(t, blynk.CmdHardware, "vw\x0051\x00not-a-number", false)
	if len(pkt.Props) != 0 {
		t.Errorf("got %+v, want no properties", pkt.Props)
	}
}

func TestDecodeDeviceID(t *testing.T) {
	const token = "00000000000000000000000000000001"
	t.Run("get_shared_dash", func(t *testing.T) {
		if got := decodeBody(t, blynk.CmdGetSharedDash, token, false).DeviceID; got != token {
			t.Errorf("DeviceID = %q, want %q", got, token)
		}
	})
	// Older firmware announces itself with login instead.
	t.Run("login", func(t *testing.T) {
		if got := decodeBody(t, blynk.CmdLogin, token, false).DeviceID; got != token {
			t.Errorf("DeviceID = %q, want %q", got, token)
		}
	})
}

func TestDecodeInternal(t *testing.T) {
	const body = "ver\x002.0.10a\x00h-beat\x0020\x00buff-in\x001024\x00dev\x00ESP32\x00" +
		"fw\x002.0.10a\x00build\x00Jul 20 2020 12:31:35\x00tmpl\x00TMPL57889\x00"
	pkt := decodeBody(t, blynk.CmdInternal, body, false)
	want := map[string]string{
		"ver": "2.0.10a", "h-beat": "20", "buff-in": "1024", "dev": "ESP32",
		"fw": "2.0.10a", "build": "Jul 20 2020 12:31:35", "tmpl": "TMPL57889",
	}
	if len(pkt.Internal) != len(want) {
		t.Fatalf("got %v, want %v", pkt.Internal, want)
	}
	for k, v := range want {
		if pkt.Internal[k] != v {
			t.Errorf("Internal[%q] = %q, want %q", k, pkt.Internal[k], v)
		}
	}
}

// An odd field count must not panic — the Elixir implementation raised here.
func TestDecodeInternalOddFieldCount(t *testing.T) {
	pkt := decodeBody(t, blynk.CmdInternal, "ver\x002.0.10a\x00orphan", false)
	if pkt.Internal["ver"] != "2.0.10a" {
		t.Errorf("Internal = %v, want ver to survive", pkt.Internal)
	}
	if len(pkt.Internal) != 1 {
		t.Errorf("Internal = %v, want only the complete pair", pkt.Internal)
	}
}

// hardware_sync is the device asking us for values; it carries no data.
func TestHardwareSyncCarriesNoData(t *testing.T) {
	pkt := decodeBody(t, blynk.CmdHardwareSync, "vr\x0088\x0065\x0066", true)
	if len(pkt.Props) != 0 || len(pkt.Unknown) != 0 {
		t.Errorf("got props %+v unknown %v, want both empty", pkt.Props, pkt.Unknown)
	}
}

// A single TCP segment can carry the same pin twice; the later value wins.
func TestGetReturnsLatestValue(t *testing.T) {
	pkt := Decode([]blynk.Frame{
		{Cmd: blynk.CmdHardware, MsgID: 1, Body: []byte("vw\x0051\x000.600")},
		{Cmd: blynk.CmdHardware, MsgID: 2, Body: []byte("vw\x0048\x008.000")},
		{Cmd: blynk.CmdHardware, MsgID: 3, Body: []byte("vw\x0051\x001.700")},
	}, false)
	if len(pkt.Props) != 3 {
		t.Fatalf("got %d properties, want 3", len(pkt.Props))
	}
	if got, ok := pkt.Float("amount_left"); !ok || got != 1.7 {
		t.Errorf("amount_left = %v (ok=%v), want 1.7", got, ok)
	}
	if !pkt.Has("percent_of_beer_left") {
		t.Error("percent_of_beer_left missing")
	}
	if pkt.Has("keg_temperature") {
		t.Error("keg_temperature reported present but was never sent")
	}
}
