package store

import (
	"log/slog"

	"github.com/matt-freed/open-plaato-keg/internal/plaato"
)

// kegSetters maps a decoded property name onto the Keg field it updates.
//
// Transient pins (tare, calculate, known_weight_calibrate) are deliberately
// absent: the device only echoes them back after we write them and they carry
// no state worth keeping.
var kegSetters = map[string]func(*Keg, any){
	"amount_left":               func(k *Keg, v any) { k.AmountLeft = asFloat(v) },
	"percent_of_beer_left":      func(k *Keg, v any) { k.PercentOfBeerLeft = asFloat(v) },
	"is_pouring":                func(k *Keg, v any) { k.IsPouring = asBool(v) },
	"keg_temperature":           func(k *Keg, v any) { k.KegTemperature = asFloat(v) },
	"last_pour":                 func(k *Keg, v any) { k.LastPour = asFloat(v) },
	"last_pour_string":          func(k *Keg, v any) { k.LastPourString = asString(v) },
	"temperature_offset":        func(k *Keg, v any) { k.TemperatureOffset = asFloat(v) },
	"temperature_correction":    func(k *Keg, v any) { k.TemperatureCorrection = asFloat(v) },
	"weight_raw":                func(k *Keg, v any) { k.WeightRaw = asFloat(v) },
	"volume_raw":                func(k *Keg, v any) { k.VolumeRaw = asFloat(v) },
	"pour_volume_raw":           func(k *Keg, v any) { k.PourVolumeRaw = asFloat(v) },
	"empty_keg_weight":          func(k *Keg, v any) { k.EmptyKegWeight = asFloat(v) },
	"max_keg_volume":            func(k *Keg, v any) { k.MaxKegVolume = asFloat(v) },
	"min_temperature":           func(k *Keg, v any) { k.MinTemperature = asFloat(v) },
	"max_temperature":           func(k *Keg, v any) { k.MaxTemperature = asFloat(v) },
	"min_temperature_max":       func(k *Keg, v any) { k.MinTemperatureMax = asFloat(v) },
	"max_temperature_min":       func(k *Keg, v any) { k.MaxTemperatureMin = asFloat(v) },
	"unit":                      func(k *Keg, v any) { k.Unit = asInt(v) },
	"measure_unit":              func(k *Keg, v any) { k.MeasureUnit = asInt(v) },
	"keg_mode":                  func(k *Keg, v any) { k.KegMode = asInt(v) },
	"sensitivity":               func(k *Keg, v any) { k.Sensitivity = asInt(v) },
	"weight_unit":               func(k *Keg, v any) { k.WeightUnit = asString(v) },
	"beer_left_unit":            func(k *Keg, v any) { k.BeerLeftUnitDevice = asString(v) },
	"volume_unit":               func(k *Keg, v any) { k.VolumeUnit = asString(v) },
	"temperature_unit":          func(k *Keg, v any) { k.TemperatureUnit = asString(v) },
	"keg_temperature_string":    func(k *Keg, v any) { k.KegTemperatureString = asString(v) },
	"chip_temperature_string":   func(k *Keg, v any) { k.ChipTemperatureString = asString(v) },
	"calculated_abv":            func(k *Keg, v any) { k.CalculatedABV = asFloat(v) },
	"calculated_alcohol_string": func(k *Keg, v any) { k.CalculatedAlcoholString = asString(v) },
	"wifi_signal_strength":      func(k *Keg, v any) { k.WifiSignalStrength = asInt(v) },
	"leak_detection":            func(k *Keg, v any) { k.LeakDetection = asInt(v) },
	"firmware_version":          func(k *Keg, v any) { k.FirmwareVersion = asString(v) },
	"device_og":                 func(k *Keg, v any) { k.DeviceOG = asFloat(v) },
	"device_fg":                 func(k *Keg, v any) { k.DeviceFG = asFloat(v) },
	"device_beer_style":         func(k *Keg, v any) { k.DeviceBeerStyle = asString(v) },
	"device_date":               func(k *Keg, v any) { k.DeviceDate = asString(v) },
}

// pourRange bounds a plausible pour, keyed by the keg's remaining-beer unit.
//
// The lower bound rejects near-zero scale noise and the upper bound rejects
// vibration spikes — a fridge compressor starting up can otherwise register as
// a 110 lb pour. Roughly 2 oz to 48 oz in every unit system.
var pourRange = map[string][2]float64{
	"lbs":   {0.1, 3.0},
	"kg":    {0.05, 1.4},
	"gal":   {0.015, 0.375},
	"litre": {0.05, 1.4},
}

var defaultPourRange = [2]float64{0.05, 1.4}

// ApplyPacket merges a decoded packet into the stored keg and returns the
// updated record.
func (s *Store) ApplyPacket(id string, pkt plaato.Packet) (*Keg, error) {
	return s.UpdateKeg(id, func(k *Keg) {
		for _, p := range pkt.Props {
			if p.Transient {
				continue
			}
			set, ok := kegSetters[p.Name]
			if !ok {
				// A pin the decoder knows but the store does not is a wiring
				// mistake, not device noise; make it visible.
				slog.Warn("decoded property has no storage mapping", "property", p.Name, "keg", id)
				continue
			}
			if p.Name == "last_pour" && !plausiblePour(k, p.Value) {
				continue
			}
			set(k, p.Value)
		}

		for key, value := range pkt.Internal {
			if k.Internal == nil {
				k.Internal = map[string]string{}
			}
			k.Internal[key] = value
		}
		for key, value := range pkt.Unknown {
			if k.Extra == nil {
				k.Extra = map[string]string{}
			}
			k.Extra[key] = value
		}
	})
}

// plausiblePour reports whether a reported pour is within the plausible range
// for the keg's current unit.
func plausiblePour(k *Keg, value any) bool {
	f := asFloat(value)
	if f == nil {
		return false
	}
	bounds, ok := pourRange[k.DeriveBeerLeftUnit()]
	if !ok {
		bounds = defaultPourRange
	}
	if *f < bounds[0] || *f > bounds[1] {
		slog.Warn("ignoring implausible pour",
			"keg", k.ID, "last_pour", *f, "unit", k.DeriveBeerLeftUnit(),
			"min", bounds[0], "max", bounds[1])
		return false
	}
	return true
}

// SetPouring records a pouring state change without touching anything else.
// Used to clear a stale "pouring" flag when a keg disconnects.
func (s *Store) SetPouring(id string, pouring bool) (*Keg, error) {
	return s.UpdateKeg(id, func(k *Keg) { k.IsPouring = &pouring })
}

func asFloat(v any) *float64 {
	switch n := v.(type) {
	case float64:
		return &n
	case int64:
		f := float64(n)
		return &f
	}
	return nil
}

func asInt(v any) *int64 {
	switch n := v.(type) {
	case int64:
		return &n
	case float64:
		i := int64(n)
		return &i
	}
	return nil
}

func asString(v any) *string {
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

func asBool(v any) *bool {
	if b, ok := v.(bool); ok {
		return &b
	}
	return nil
}
