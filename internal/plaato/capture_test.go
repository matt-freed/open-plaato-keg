package plaato

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/matt-freed/open-plaato-keg/internal/blynk"
)

// replayCapture feeds the recorded session through the framer and decoder in
// the order the device sent it, returning the accumulated final state.
func replayCapture(t *testing.T, includeUnknown bool) (id string, internal map[string]string, state map[string]any, unknown map[string]string) {
	t.Helper()

	names, err := filepath.Glob("../../testdata/capture/*.bin")
	if err != nil || len(names) == 0 {
		t.Fatalf("no capture files: %v", err)
	}
	sort.Slice(names, func(i, j int) bool {
		a, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(names[i]), ".bin"))
		b, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(names[j]), ".bin"))
		return a < b
	})

	state = map[string]any{}
	unknown = map[string]string{}
	var framer blynk.Framer
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		frames, err := framer.Feed(data)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(name), err)
		}
		pkt := Decode(frames, includeUnknown)
		if pkt.DeviceID != "" {
			id = pkt.DeviceID
		}
		if pkt.Internal != nil {
			internal = pkt.Internal
		}
		for _, p := range pkt.Props {
			state[p.Name] = p.Value
		}
		for k, v := range pkt.Unknown {
			unknown[k] = v
		}
	}
	return id, internal, state, unknown
}

// Replaying the full recorded session must reproduce the device's real state.
// These values are cross-checked against the sample output documented in the
// Elixir project's README.
func TestReplayCapture(t *testing.T) {
	id, internal, state, unknown := replayCapture(t, true)

	if want := "00000000000000000000000000000001"; id != want {
		t.Errorf("device id = %q, want %q", id, want)
	}

	wantInternal := map[string]string{
		"ver": "2.0.10a", "h-beat": "20", "buff-in": "1024", "dev": "ESP32",
		"fw": "2.0.10a", "build": "Jul 20 2020 12:31:35", "tmpl": "TMPL57889",
	}
	for k, want := range wantInternal {
		if internal[k] != want {
			t.Errorf("internal[%q] = %q, want %q", k, internal[k], want)
		}
	}

	want := map[string]any{
		"amount_left":             0.04,
		"beer_left_unit":          "kg",
		"chip_temperature_string": "74.44°C",
		"device_fg":               1000.0,
		"device_og":               1000.0,
		"empty_keg_weight":        0.0,
		"firmware_version":        "2.0.10a",
		"is_pouring":              false,
		"keg_temperature":         22.25,
		"keg_temperature_string":  "22.25°C",
		"last_pour":               0.184,
		"last_pour_string":        "0.18L",
		"leak_detection":          int64(0),
		"max_keg_volume":          20.059,
		"max_temperature":         30.0,
		"max_temperature_min":     -20.0,
		"measure_unit":            int64(1),
		"min_temperature":         0.0,
		"min_temperature_max":     40.0,
		"percent_of_beer_left":    0.0,
		"pour_volume_raw":         0.0,
		"temperature_correction":  -44.0,
		"temperature_offset":      -7.5,
		"temperature_unit":        "°C",
		"unit":                    int64(1),
		"volume_raw":              0.0,
		"volume_unit":             "litre",
		"weight_raw":              200621.0,
		"weight_unit":             "kg",
		"wifi_signal_strength":    int64(52),
	}

	for name, wantVal := range want {
		got, ok := state[name]
		if !ok {
			t.Errorf("%s missing from the replayed state", name)
			continue
		}
		if got != wantVal {
			t.Errorf("%s = %#v (%T), want %#v (%T)", name, got, got, wantVal, wantVal)
		}
	}
	for name := range state {
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected property %s = %#v in the replayed state", name, state[name])
		}
	}

	// Pins 78, 84, 85 and 95 appear in the device's own sync request but are
	// never written, so nothing should fall through as unknown.
	if len(unknown) != 0 {
		t.Errorf("unknown pins = %v, want none", unknown)
	}
}

// The keg must be classifiable as a keg from the capture, which is what the
// connection handler relies on before it will store anything.
func TestReplayCaptureIdentifiesAKeg(t *testing.T) {
	_, internal, state, _ := replayCapture(t, false)

	if internal["dev"] != "ESP32" {
		t.Errorf("internal dev = %q, want ESP32", internal["dev"])
	}
	for _, name := range []string{"amount_left", "keg_temperature", "percent_of_beer_left", "is_pouring", "firmware_version"} {
		if _, ok := state[name]; !ok {
			t.Errorf("keg-identifying property %s never arrived", name)
		}
	}
}
