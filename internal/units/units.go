// Package units converts keg readings between the unit systems the UI can
// display.
//
// Nothing here touches stored or forwarded data: the scale keeps reporting,
// this server keeps persisting, and BarHelper and the ESP32 display endpoint
// keep receiving, in the device's own units. These conversions exist only so
// the browser can present a reading in a unit the user prefers.
package units

// System is the unit system a reading is presented in.
type System int

const (
	Metric System = iota
	US
)

// Measure is whether a remaining-beer reading is presented as a weight or a
// volume.
type Measure int

const (
	Weight Measure = iota
	Volume
)

// KgPerUnit converts a remaining-beer reading to kilograms.
//
// Beer is close enough to water that one litre is treated as one kilogram,
// which is the same assumption the ESP32 display endpoint already makes. Real
// beer is nearer 1.01, so a weight/volume switch is off by about 1%.
//
// Denominating everything in kilograms makes all four conversions fall out of
// one table: kg to lbs and litre to gal are exact, and the two that cross
// between weight and volume pick up the density assumption. CO2 only ever
// crosses kg to lbs, so density never applies to it.
var KgPerUnit = map[string]float64{
	"litre":   1,
	"kg":      1,
	"lbs":     0.453592,
	"gal":     3.78541, // US gallons, matching the device's own unit=2
	"kg CO₂":  1,
	"lbs CO₂": 0.453592,
}

// pourSubUnits is the sub-unit a pour-sized reading is shown in, so a 0.5
// litre pour reads as "500 ml" rather than "0.5 litre".
var pourSubUnits = map[string]struct {
	mult float64
	unit string
}{
	"lbs":     {16, "oz"},
	"kg":      {1000, "g"},
	"gal":     {128, "oz"},
	"litre":   {1000, "ml"},
	"kg CO₂":  {1000, "g"},
	"lbs CO₂": {16, "oz"},
}

// Label returns the remaining-beer unit for a system and measure.
//
// CO2 is always weighed, whatever the measure says. The vocabulary matches
// store.(*Keg).DeriveBeerLeftUnit exactly, so a label can round-trip through
// ParseLabel.
func Label(sys System, m Measure, co2 bool) string {
	switch {
	case co2 && sys == Metric:
		return "kg CO₂"
	case co2:
		return "lbs CO₂"
	case sys == Metric && m == Weight:
		return "kg"
	case sys == Metric:
		return "litre"
	case m == Weight:
		return "lbs"
	default:
		return "gal"
	}
}

// ParseLabel maps a remaining-beer label back onto its system and measure.
//
// ok is false for a label this package does not know, which happens when the
// device has not reported its unit yet and its own pin 74 is passed through.
// Such a reading must not be converted, because there is nothing reliable to
// convert it from.
func ParseLabel(label string) (sys System, m Measure, co2 bool, ok bool) {
	switch label {
	case "kg":
		return Metric, Weight, false, true
	case "litre":
		return Metric, Volume, false, true
	case "lbs":
		return US, Weight, false, true
	case "gal":
		return US, Volume, false, true
	case "kg CO₂":
		return Metric, Weight, true, true
	case "lbs CO₂":
		return US, Weight, true, true
	}
	return Metric, Volume, false, false
}

// ConvertAmount rescales v from one remaining-beer unit to another.
//
// An unrecognised unit on either side leaves the value alone, so an
// unconvertible reading is passed through rather than silently rescaled.
func ConvertAmount(v float64, from, to string) float64 {
	if from == to {
		return v
	}
	fromKg, ok := KgPerUnit[from]
	if !ok {
		return v
	}
	toKg, ok := KgPerUnit[to]
	if !ok || toKg == 0 {
		return v
	}
	return v * fromKg / toKg
}

// TempLabel returns the temperature unit for a system.
func TempLabel(sys System) string {
	if sys == US {
		return "°F"
	}
	return "°C"
}

// ParseTempLabel reads a device-reported temperature unit.
//
// The device sends this as a free-form string, so anything ending in an F is
// taken as Fahrenheit and anything ending in a C as Celsius; ok is false for
// a string that is neither.
func ParseTempLabel(s string) (fahrenheit, ok bool) {
	switch s {
	case "°F", "F", "f", "℉":
		return true, true
	case "°C", "C", "c", "℃":
		return false, true
	}
	return false, false
}

// ConvertTemp converts a temperature between Celsius and Fahrenheit.
func ConvertTemp(v float64, fromF, toF bool) float64 {
	switch {
	case fromF == toF:
		return v
	case toF:
		return v*9/5 + 32
	default:
		return (v - 32) * 5 / 9
	}
}

// PourSubUnit returns the multiplier and label a pour-sized reading is shown
// in. An unknown unit is left as it is, which is what the dashboard did before
// this table moved out of its inline script.
func PourSubUnit(unit string) (mult float64, label string) {
	if sub, ok := pourSubUnits[unit]; ok {
		return sub.mult, sub.unit
	}
	return 1, unit
}
