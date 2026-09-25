package units

import (
	"math"
	"testing"
)

func closeTo(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestLabelCoversEverySystemAndMeasure(t *testing.T) {
	tests := []struct {
		name string
		sys  System
		m    Measure
		co2  bool
		want string
	}{
		{"metric weight", Metric, Weight, false, "kg"},
		{"metric volume", Metric, Volume, false, "litre"},
		{"us weight", US, Weight, false, "lbs"},
		{"us volume", US, Volume, false, "gal"},
		// CO2 is weighed whatever the measure says.
		{"metric co2 ignores volume", Metric, Volume, true, "kg CO₂"},
		{"us co2 ignores volume", US, Volume, true, "lbs CO₂"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Label(tt.sys, tt.m, tt.co2); got != tt.want {
				t.Errorf("Label = %q, want %q", got, tt.want)
			}
		})
	}
}

// Every label Label can produce must parse back to what produced it, which is
// what lets a display preference be applied one axis at a time.
func TestParseLabelRoundTripsEveryLabel(t *testing.T) {
	for _, sys := range []System{Metric, US} {
		for _, m := range []Measure{Weight, Volume} {
			for _, co2 := range []bool{false, true} {
				label := Label(sys, m, co2)
				gotSys, gotM, gotCO2, ok := ParseLabel(label)
				if !ok {
					t.Fatalf("ParseLabel(%q) not ok", label)
				}
				if gotCO2 != co2 || gotSys != sys {
					t.Errorf("ParseLabel(%q) = sys %v co2 %v, want sys %v co2 %v",
						label, gotSys, gotCO2, sys, co2)
				}
				// CO2 is always weighed, so the measure it parses back to is
				// Weight rather than the one that was asked for.
				wantM := m
				if co2 {
					wantM = Weight
				}
				if gotM != wantM {
					t.Errorf("ParseLabel(%q) measure = %v, want %v", label, gotM, wantM)
				}
			}
		}
	}
}

func TestParseLabelRejectsAnUnknownLabel(t *testing.T) {
	// A device that has not reported its unit has its own pin 74 passed
	// through, and that must not be converted from.
	for _, label := range []string{"", "pints", "oz", "L"} {
		if _, _, _, ok := ParseLabel(label); ok {
			t.Errorf("ParseLabel(%q) = ok, want not ok", label)
		}
	}
}

func TestConvertAmount(t *testing.T) {
	tests := []struct {
		name     string
		v        float64
		from, to string
		want     float64
	}{
		{"same unit is untouched", 3.5, "litre", "litre", 3.5},
		{"kg to lbs", 1, "kg", "lbs", 2.2046244},
		{"lbs to kg", 2.2046244, "lbs", "kg", 1},
		{"litre to gal", 3.78541, "litre", "gal", 1},
		{"gal to litre", 1, "gal", "litre", 3.78541},
		// Crossing weight and volume uses the one-litre-is-one-kilogram
		// assumption the ESP32 endpoint already makes.
		{"litre to kg", 5, "litre", "kg", 5},
		{"gal to lbs", 1, "gal", "lbs", 8.3454073},
		{"co2 kg to lbs", 1, "kg CO₂", "lbs CO₂", 2.2046244},
		// An unconvertible unit is passed through rather than rescaled.
		{"unknown source", 4, "pints", "litre", 4},
		{"unknown target", 4, "litre", "pints", 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConvertAmount(tt.v, tt.from, tt.to); !closeTo(got, tt.want) {
				t.Errorf("ConvertAmount(%v, %q, %q) = %v, want %v", tt.v, tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestConvertAmountRoundTrips(t *testing.T) {
	units := []string{"kg", "litre", "lbs", "gal"}
	for _, from := range units {
		for _, to := range units {
			got := ConvertAmount(ConvertAmount(7.25, from, to), to, from)
			if !closeTo(got, 7.25) {
				t.Errorf("round trip %s->%s->%s = %v, want 7.25", from, to, from, got)
			}
		}
	}
}

func TestConvertTemp(t *testing.T) {
	tests := []struct {
		name       string
		v          float64
		fromF, toF bool
		want       float64
	}{
		{"celsius unchanged", 4, false, false, 4},
		{"fahrenheit unchanged", 39, true, true, 39},
		{"freezing to F", 0, false, true, 32},
		{"boiling to F", 100, false, true, 212},
		{"cellar to F", 4, false, true, 39.2},
		{"freezing from F", 32, true, false, 0},
		{"boiling from F", 212, true, false, 100},
		// The one temperature that reads the same either way.
		{"minus forty to F", -40, false, true, -40},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConvertTemp(tt.v, tt.fromF, tt.toF); !closeTo(got, tt.want) {
				t.Errorf("ConvertTemp(%v, %v, %v) = %v, want %v", tt.v, tt.fromF, tt.toF, got, tt.want)
			}
		})
	}
}

func TestParseTempLabel(t *testing.T) {
	for _, s := range []string{"°F", "F", "f"} {
		if f, ok := ParseTempLabel(s); !ok || !f {
			t.Errorf("ParseTempLabel(%q) = %v, %v; want true, true", s, f, ok)
		}
	}
	for _, s := range []string{"°C", "C", "c"} {
		if f, ok := ParseTempLabel(s); !ok || f {
			t.Errorf("ParseTempLabel(%q) = %v, %v; want false, true", s, f, ok)
		}
	}
	for _, s := range []string{"", "kelvin", "degrees"} {
		if _, ok := ParseTempLabel(s); ok {
			t.Errorf("ParseTempLabel(%q) = ok, want not ok", s)
		}
	}
}

func TestPourSubUnit(t *testing.T) {
	tests := []struct {
		unit      string
		wantMult  float64
		wantLabel string
	}{
		{"litre", 1000, "ml"},
		{"kg", 1000, "g"},
		{"gal", 128, "oz"},
		{"lbs", 16, "oz"},
		{"kg CO₂", 1000, "g"},
		{"lbs CO₂", 16, "oz"},
		// An unknown unit is left alone, which is what the dashboard did
		// before this table moved out of its inline script.
		{"pints", 1, "pints"},
	}
	for _, tt := range tests {
		t.Run(tt.unit, func(t *testing.T) {
			mult, label := PourSubUnit(tt.unit)
			if mult != tt.wantMult || label != tt.wantLabel {
				t.Errorf("PourSubUnit(%q) = %v, %q; want %v, %q",
					tt.unit, mult, label, tt.wantMult, tt.wantLabel)
			}
		})
	}
}
