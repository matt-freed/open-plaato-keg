package store

import (
	"math"
	"testing"

	"github.com/matt-freed/open-plaato-keg/internal/units"
)

func f64(v float64) *float64 { return &v }
func i64p(n int64) *int64    { return &n }
func strp(s string) *string  { return &s }

func nearly(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func follow() DisplayUnits {
	return DisplayUnits{System: DisplaySystemDevice, Measure: DisplayMeasureDevice}
}

// Every label DeriveBeerLeftUnit can return must be one the conversion package
// recognises, or a reading would silently pass through unconverted. The guard
// lives here so internal/units can stay dependency-free.
func TestDerivedLabelsAreAllConvertible(t *testing.T) {
	for _, unit := range []int64{1, 2} {
		for _, measure := range []int64{1, 2} {
			for _, mode := range []int64{1, 2} {
				k := &Keg{Unit: i64p(unit), MeasureUnit: i64p(measure), KegMode: i64p(mode)}
				label := k.DeriveBeerLeftUnit()
				if _, _, _, ok := units.ParseLabel(label); !ok {
					t.Errorf("DeriveBeerLeftUnit returned %q, which units.ParseLabel rejects", label)
				}
			}
		}
	}
}

// The default must render exactly as the UI did before this setting existed.
func TestSetDisplayFollowingDeviceIsAPassthrough(t *testing.T) {
	k := &Keg{
		Unit: i64p(1), MeasureUnit: i64p(2), KegMode: i64p(1),
		AmountLeft: f64(10), KegTemperature: f64(4), LastPour: f64(0.5),
		TemperatureUnit: strp("°C"),
	}
	k.SetDisplay(follow())

	if got := *k.Display.AmountLeft; !nearly(got, 10) {
		t.Errorf("amount = %v, want 10", got)
	}
	if k.Display.AmountUnit != "litre" {
		t.Errorf("amount unit = %q, want %q", k.Display.AmountUnit, "litre")
	}
	if got := *k.Display.KegTemperature; !nearly(got, 4) {
		t.Errorf("temperature = %v, want 4", got)
	}
	if k.Display.TemperatureUnit != "°C" {
		t.Errorf("temperature unit = %q, want %q", k.Display.TemperatureUnit, "°C")
	}
	// A pour is still shown in its sub-unit, as the dashboard always did.
	if got := *k.Display.LastPour; !nearly(got, 500) {
		t.Errorf("last pour = %v, want 500", got)
	}
	if k.Display.LastPourUnit != "ml" {
		t.Errorf("last pour unit = %q, want %q", k.Display.LastPourUnit, "ml")
	}
}

func TestSetDisplayConvertsMetricVolumeToUSVolume(t *testing.T) {
	k := &Keg{
		Unit: i64p(1), MeasureUnit: i64p(2), KegMode: i64p(1),
		AmountLeft: f64(18.92705), KegTemperature: f64(4), LastPour: f64(0.5),
		TemperatureUnit: strp("°C"),
	}
	k.SetDisplay(DisplayUnits{System: DisplaySystemUS, Measure: DisplayMeasureVolume})

	if got := *k.Display.AmountLeft; !nearly(got, 5) {
		t.Errorf("amount = %v, want 5 gal", got)
	}
	if k.Display.AmountUnit != "gal" {
		t.Errorf("amount unit = %q, want %q", k.Display.AmountUnit, "gal")
	}
	if got := *k.Display.KegTemperature; !nearly(got, 39.2) {
		t.Errorf("temperature = %v, want 39.2", got)
	}
	if k.Display.TemperatureUnit != "°F" {
		t.Errorf("temperature unit = %q, want %q", k.Display.TemperatureUnit, "°F")
	}
	// 0.5 litre is 0.1321 gal, shown as fluid ounces.
	if got := *k.Display.LastPour; !nearly(got, 16.9070193) {
		t.Errorf("last pour = %v, want ~16.907 oz", got)
	}
	if k.Display.LastPourUnit != "oz" {
		t.Errorf("last pour unit = %q, want %q", k.Display.LastPourUnit, "oz")
	}
}

// The two axes are independent, so a measure change alone keeps the system.
func TestSetDisplayConvertsVolumeToWeightWithoutChangingSystem(t *testing.T) {
	k := &Keg{
		Unit: i64p(1), MeasureUnit: i64p(2), KegMode: i64p(1),
		AmountLeft: f64(10),
	}
	k.SetDisplay(DisplayUnits{System: DisplaySystemDevice, Measure: DisplayMeasureWeight})

	if k.Display.AmountUnit != "kg" {
		t.Errorf("amount unit = %q, want %q", k.Display.AmountUnit, "kg")
	}
	if got := *k.Display.AmountLeft; !nearly(got, 10) {
		t.Errorf("amount = %v, want 10 (one litre is treated as one kilogram)", got)
	}
}

// CO2 is weighed whatever the measure says, matching DeriveBeerLeftUnit.
func TestSetDisplayKeepsCO2AsAWeight(t *testing.T) {
	k := &Keg{
		Unit: i64p(1), MeasureUnit: i64p(1), KegMode: i64p(2),
		AmountLeft: f64(1),
	}
	k.SetDisplay(DisplayUnits{System: DisplaySystemUS, Measure: DisplayMeasureVolume})

	if k.Display.AmountUnit != "lbs CO₂" {
		t.Errorf("amount unit = %q, want %q", k.Display.AmountUnit, "lbs CO₂")
	}
	if got := *k.Display.AmountLeft; !nearly(got, 2.2046244) {
		t.Errorf("amount = %v, want 2.2046244", got)
	}
}

// Before the device names its unit there is nothing reliable to convert from.
func TestSetDisplayLeavesAnUnknownDeviceUnitAlone(t *testing.T) {
	k := &Keg{BeerLeftUnitDevice: strp("pints"), AmountLeft: f64(3)}
	k.SetDisplay(DisplayUnits{System: DisplaySystemUS, Measure: DisplayMeasureWeight})

	if k.Display.AmountUnit != "pints" {
		t.Errorf("amount unit = %q, want %q", k.Display.AmountUnit, "pints")
	}
	if got := *k.Display.AmountLeft; !nearly(got, 3) {
		t.Errorf("amount = %v, want 3 unchanged", got)
	}
}

// A reading the device never sent must not become 0.
func TestSetDisplayLeavesUnreportedReadingsNil(t *testing.T) {
	k := &Keg{Unit: i64p(1), MeasureUnit: i64p(2), KegMode: i64p(1)}
	k.SetDisplay(DisplayUnits{System: DisplaySystemUS, Measure: DisplayMeasureVolume})

	if k.Display.AmountLeft != nil {
		t.Errorf("amount = %v, want nil", *k.Display.AmountLeft)
	}
	if k.Display.KegTemperature != nil {
		t.Errorf("temperature = %v, want nil", *k.Display.KegTemperature)
	}
	if k.Display.LastPour != nil {
		t.Errorf("last pour = %v, want nil", *k.Display.LastPour)
	}
}

// Without a device-reported temperature unit, the configured system says what
// the scale is reporting in.
func TestSetDisplayFallsBackToTheUnitSystemForTemperature(t *testing.T) {
	k := &Keg{Unit: i64p(2), MeasureUnit: i64p(2), KegMode: i64p(1), KegTemperature: f64(39.2)}
	k.SetDisplay(DisplayUnits{System: DisplaySystemMetric, Measure: DisplayMeasureDevice})

	if got := *k.Display.KegTemperature; !nearly(got, 4) {
		t.Errorf("temperature = %v, want 4", got)
	}
	if k.Display.TemperatureUnit != "°C" {
		t.Errorf("temperature unit = %q, want %q", k.Display.TemperatureUnit, "°C")
	}
}

// The display block is derived per request. Writing a keg back must not
// persist it, which is what keeps it out of kegColumns.
func TestDisplayIsNeverPersisted(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.UpdateKeg("keg-1", func(k *Keg) {
		k.Unit, k.MeasureUnit, k.KegMode = i64p(1), i64p(2), i64p(1)
		k.AmountLeft = f64(10)
		k.SetDisplay(DisplayUnits{System: DisplaySystemUS, Measure: DisplayMeasureVolume})
	}); err != nil {
		t.Fatalf("UpdateKeg: %v", err)
	}

	got, err := s.GetKeg("keg-1")
	if err != nil {
		t.Fatalf("GetKeg: %v", err)
	}
	if got.Display != nil {
		t.Errorf("Display survived a round trip through the database: %+v", got.Display)
	}
	// The canonical reading must still be in the device's own unit.
	if v := *got.AmountLeft; !nearly(v, 10) {
		t.Errorf("amount_left = %v, want 10 litres as the device reported it", v)
	}
	if got.BeerLeftUnit != "litre" {
		t.Errorf("beer_left_unit = %q, want %q", got.BeerLeftUnit, "litre")
	}
}

func TestConvertLogEntries(t *testing.T) {
	k := &Keg{Unit: i64p(1), MeasureUnit: i64p(2), KegMode: i64p(1), TemperatureUnit: strp("°C")}
	entries := []LogEntry{
		{Timestamp: 1, AmountLeft: f64(18.92705), KegTemperature: f64(0), PercentOfBeerLeft: f64(50)},
		{Timestamp: 2, AmountLeft: nil, KegTemperature: nil},
	}
	ConvertLogEntries(entries, k, DisplayUnits{System: DisplaySystemUS, Measure: DisplayMeasureVolume})

	if got := *entries[0].AmountLeft; !nearly(got, 5) {
		t.Errorf("amount = %v, want 5 gal", got)
	}
	if got := *entries[0].KegTemperature; !nearly(got, 32) {
		t.Errorf("temperature = %v, want 32", got)
	}
	// Percent is unitless and must be left alone.
	if got := *entries[0].PercentOfBeerLeft; !nearly(got, 50) {
		t.Errorf("percent = %v, want 50 unchanged", got)
	}
	if entries[1].AmountLeft != nil || entries[1].KegTemperature != nil {
		t.Error("an unreported reading was filled in")
	}
}

func TestConvertLogEntriesIsANoOpWhenFollowingTheDevice(t *testing.T) {
	k := &Keg{Unit: i64p(1), MeasureUnit: i64p(2), KegMode: i64p(1)}
	entries := []LogEntry{{Timestamp: 1, AmountLeft: f64(10), KegTemperature: f64(4)}}
	ConvertLogEntries(entries, k, follow())

	if got := *entries[0].AmountLeft; !nearly(got, 10) {
		t.Errorf("amount = %v, want 10 unchanged", got)
	}
	if got := *entries[0].KegTemperature; !nearly(got, 4) {
		t.Errorf("temperature = %v, want 4 unchanged", got)
	}
}

func TestDisplayUnitsDefaultToFollowingTheDevice(t *testing.T) {
	s := newTestStore(t)

	cfg, err := s.GetAppConfig()
	if err != nil {
		t.Fatalf("GetAppConfig: %v", err)
	}
	if !cfg.DisplayUnits.FollowsDevice() {
		t.Errorf("default display units = %+v, want both axes following the device", cfg.DisplayUnits)
	}
}

func TestSetDisplayUnitsRoundTrips(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetDisplayUnits(DisplayUnits{
		System: DisplaySystemUS, Measure: DisplayMeasureWeight,
	}); err != nil {
		t.Fatalf("SetDisplayUnits: %v", err)
	}

	cfg, err := s.GetAppConfig()
	if err != nil {
		t.Fatalf("GetAppConfig: %v", err)
	}
	if cfg.DisplayUnits.System != DisplaySystemUS || cfg.DisplayUnits.Measure != DisplayMeasureWeight {
		t.Errorf("display units = %+v", cfg.DisplayUnits)
	}
	if cfg.DisplayUnits.FollowsDevice() {
		t.Error("FollowsDevice = true after an explicit choice")
	}
}

// Anything unrecognised must fall back to following the device, so an
// unreadable setting shows the reading as the scale reports it.
func TestNormalizeDisplayUnitsRejectsJunk(t *testing.T) {
	for _, in := range []string{"", "cubits", "imperial", " "} {
		if got := NormalizeDisplaySystem(in); got != DisplaySystemDevice {
			t.Errorf("NormalizeDisplaySystem(%q) = %q, want %q", in, got, DisplaySystemDevice)
		}
	}
	for _, in := range []string{"", "mass", "litres"} {
		if got := NormalizeDisplayMeasure(in); got != DisplayMeasureDevice {
			t.Errorf("NormalizeDisplayMeasure(%q) = %q, want %q", in, got, DisplayMeasureDevice)
		}
	}
	// Casing and padding are tolerated, as they are for the other settings.
	if got := NormalizeDisplaySystem(" US "); got != DisplaySystemUS {
		t.Errorf("NormalizeDisplaySystem(\" US \") = %q, want %q", got, DisplaySystemUS)
	}
	if got := NormalizeDisplayMeasure("Weight"); got != DisplayMeasureWeight {
		t.Errorf("NormalizeDisplayMeasure(\"Weight\") = %q, want %q", got, DisplayMeasureWeight)
	}
}
