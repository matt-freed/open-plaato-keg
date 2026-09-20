// Package plaato interprets Blynk frames as Plaato Keg virtual-pin data.
//
// The pin numbering is documented at
// https://intercom.help/plaato/en/articles/5004722-pins-plaato-keg
package plaato

import (
	"sort"
	"strconv"
	"strings"
)

// Kind is the Go type a pin's value decodes to.
type Kind int

const (
	KindString Kind = iota
	KindFloat
	KindInt
	KindBool
)

// Pin describes one virtual pin.
type Pin struct {
	// Name is the property name used throughout the application and the API.
	Name string
	Kind Kind
	// Transient marks a pin the device only echoes back after we write it — a
	// momentary button or a trigger. It decodes, so it is not reported as an
	// unknown pin, but it carries no state worth persisting.
	Transient bool
}

// hardwarePins maps a "vw" virtual-pin write to its property.
//
// Pins the firmware is known to send but that carry nothing useful (78, 84, 85,
// 95 — requested in the device's own sync but never written) are deliberately
// absent and surface as unknown pins.
var hardwarePins = map[string]Pin{
	"47": {Name: "last_pour_string", Kind: KindString},
	"48": {Name: "percent_of_beer_left", Kind: KindFloat},
	"49": {Name: "is_pouring", Kind: KindBool},
	"51": {Name: "amount_left", Kind: KindFloat},
	"52": {Name: "temperature_offset", Kind: KindFloat},
	"53": {Name: "weight_raw", Kind: KindFloat},
	"54": {Name: "volume_raw", Kind: KindFloat},
	"55": {Name: "pour_volume_raw", Kind: KindFloat},
	"56": {Name: "keg_temperature", Kind: KindFloat},
	"59": {Name: "last_pour", Kind: KindFloat},
	"60": {Name: "tare", Kind: KindString, Transient: true},
	"61": {Name: "known_weight_calibrate", Kind: KindFloat, Transient: true},
	"62": {Name: "empty_keg_weight", Kind: KindFloat},
	"63": {Name: "temperature_correction", Kind: KindFloat},
	"64": {Name: "device_beer_style", Kind: KindString},
	"65": {Name: "device_og", Kind: KindFloat},
	"66": {Name: "device_fg", Kind: KindFloat},
	"67": {Name: "device_date", Kind: KindString},
	"68": {Name: "calculated_abv", Kind: KindFloat},
	"69": {Name: "keg_temperature_string", Kind: KindString},
	"70": {Name: "calculated_alcohol_string", Kind: KindString},
	"71": {Name: "unit", Kind: KindInt},
	"72": {Name: "calculate", Kind: KindString, Transient: true},
	"73": {Name: "weight_unit", Kind: KindString},
	"74": {Name: "beer_left_unit", Kind: KindString},
	"75": {Name: "measure_unit", Kind: KindInt},
	"76": {Name: "max_keg_volume", Kind: KindFloat},
	"80": {Name: "temperature_unit", Kind: KindString},
	"81": {Name: "wifi_signal_strength", Kind: KindInt},
	"82": {Name: "volume_unit", Kind: KindString},
	"83": {Name: "leak_detection", Kind: KindInt},
	"86": {Name: "min_temperature", Kind: KindFloat},
	"87": {Name: "max_temperature", Kind: KindFloat},
	"88": {Name: "keg_mode", Kind: KindInt},
	"89": {Name: "sensitivity", Kind: KindInt},
	"92": {Name: "chip_temperature_string", Kind: KindString},
	"93": {Name: "firmware_version", Kind: KindString},
}

// propertyPins maps a widget-property write (cmd 19) to its property.
//
// The asymmetry is deliberate and matches the hardware: pin 86 is the
// min-temperature slider, so its "max" is that slider's upper bound rather than
// a maximum temperature.
var propertyPins = map[[2]string]Pin{
	{"51", "max"}: {Name: "max_keg_volume", Kind: KindFloat},
	{"86", "min"}: {Name: "min_temperature", Kind: KindFloat},
	{"86", "max"}: {Name: "min_temperature_max", Kind: KindFloat},
	{"87", "min"}: {Name: "max_temperature_min", Kind: KindFloat},
	{"87", "max"}: {Name: "max_temperature", Kind: KindFloat},
}

// Command pin numbers for writes back to the device.
const (
	PinTemperatureOffset = "52"
	PinTare              = "60"
	PinKnownWeight       = "61"
	PinEmptyKegWeight    = "62"
	PinBeerStyle         = "64"
	PinDate              = "67"
	PinUnit              = "71"
	PinMeasureUnit       = "75"
	PinMaxKegVolume      = "76"
	PinKegMode           = "88"
	PinSensitivity       = "89"
)

// parseValue converts a raw pin value to the pin's declared type. A value that
// will not parse is reported as unusable so the caller can drop it rather than
// storing a zero.
func parseValue(p Pin, raw string) (any, bool) {
	switch p.Kind {
	case KindString:
		return raw, true

	case KindFloat:
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, false
		}
		return f, true

	case KindInt:
		// Some values arrive as "1" and others as "1.000"; accept both.
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, false
		}
		return int64(f), true

	case KindBool:
		// The firmware signals "pouring" with 255 rather than 1.
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, false
		}
		return f != 0, true
	}
	return nil, false
}

// PersistentPropertyNames returns every property name the decoder can produce
// that is worth storing, so callers can verify they handle all of them.
func PersistentPropertyNames() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p Pin) {
		if p.Transient || seen[p.Name] {
			return
		}
		seen[p.Name] = true
		out = append(out, p.Name)
	}
	for _, p := range hardwarePins {
		add(p)
	}
	for _, p := range propertyPins {
		add(p)
	}
	sort.Strings(out)
	return out
}
