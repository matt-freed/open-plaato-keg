package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/matt-freed/open-plaato-keg/internal/store"
)

// metricVolumeKeg is a scale reporting 10 litres at 4 °C with a half-litre
// pour, which is 2.641 gal / 39.2 °F once converted.
func metricVolumeKeg(a *testAPI, id string) *store.Keg {
	return a.storeKeg(id, "vw\x0051\x0010.000", "vw\x0059\x000.500", "vw\x0056\x004.000",
		"vw\x0071\x001", "vw\x0075\x002", "vw\x0088\x001", "vw\x0080\x00°C")
}

func setUS(t *testing.T, a *testAPI) {
	t.Helper()
	rec := a.do(http.MethodPost, "/api/config/display-units",
		map[string]string{"system": "us", "measure": "volume"})
	assertStatus(t, rec, http.StatusOK)
}

func TestDisplayUnitsEndpointRoundTrip(t *testing.T) {
	a := newTestAPI(t)

	rec := a.do(http.MethodGet, "/api/config/display-units", nil)
	assertStatus(t, rec, http.StatusOK)
	var got store.DisplayUnits
	a.decode(rec, &got)
	if got.System != store.DisplaySystemDevice || got.Measure != store.DisplayMeasureDevice {
		t.Errorf("default = %+v, want both axes following the device", got)
	}

	setUS(t, a)

	rec = a.do(http.MethodGet, "/api/config/display-units", nil)
	a.decode(rec, &got)
	if got.System != store.DisplaySystemUS || got.Measure != store.DisplayMeasureVolume {
		t.Errorf("display units = %+v", got)
	}

	// An unrecognised value falls back to following the device rather than
	// being stored as-is.
	rec = a.do(http.MethodPost, "/api/config/display-units",
		map[string]string{"system": "cubits", "measure": "nonsense"})
	assertStatus(t, rec, http.StatusOK)
	rec = a.do(http.MethodGet, "/api/config/display-units", nil)
	a.decode(rec, &got)
	if got.System != store.DisplaySystemDevice || got.Measure != store.DisplayMeasureDevice {
		t.Errorf("junk was accepted: %+v", got)
	}
}

func TestKegListCarriesConvertedDisplayBlock(t *testing.T) {
	a := newTestAPI(t)
	metricVolumeKeg(a, "keg-1")
	setUS(t, a)

	rec := a.do(http.MethodGet, "/api/kegs", nil)
	assertStatus(t, rec, http.StatusOK)
	var kegs []*store.Keg
	a.decode(rec, &kegs)
	if len(kegs) != 1 {
		t.Fatalf("got %d kegs, want 1", len(kegs))
	}
	assertConvertedToUS(t, kegs[0])
}

func TestGetKegCarriesConvertedDisplayBlock(t *testing.T) {
	a := newTestAPI(t)
	metricVolumeKeg(a, "keg-1")
	setUS(t, a)

	rec := a.do(http.MethodGet, "/api/kegs/keg-1", nil)
	assertStatus(t, rec, http.StatusOK)
	var k store.Keg
	a.decode(rec, &k)
	assertConvertedToUS(t, &k)
}

// The canonical fields must stay in the device's units: they are what the
// setup page writes back and what BarHelper is fed.
func assertConvertedToUS(t *testing.T, k *store.Keg) {
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
	if got := *k.Display.KegTemperature; got < 39.1 || got > 39.3 {
		t.Errorf("display temperature = %v, want 39.2", got)
	}
	if k.Display.TemperatureUnit != "°F" {
		t.Errorf("display temperature unit = %q, want °F", k.Display.TemperatureUnit)
	}

	if *k.AmountLeft != 10 {
		t.Errorf("amount_left = %v, want the device's own 10", *k.AmountLeft)
	}
	if k.BeerLeftUnit != "litre" {
		t.Errorf("beer_left_unit = %q, want litre", k.BeerLeftUnit)
	}
	if *k.KegTemperature != 4 {
		t.Errorf("keg_temperature = %v, want the device's own 4", *k.KegTemperature)
	}
}

func TestHistoryHonoursDisplayUnits(t *testing.T) {
	a := newTestAPI(t)
	k := metricVolumeKeg(a, "keg-1")
	if err := a.store.AppendLog(k, time.Now()); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}
	setUS(t, a)

	rec := a.do(http.MethodGet, "/api/kegs/keg-1/log", nil)
	assertStatus(t, rec, http.StatusOK)
	var entries []store.LogEntry
	a.decode(rec, &entries)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if got := *entries[0].AmountLeft; got < 2.64 || got > 2.65 {
		t.Errorf("amount = %v, want ~2.642 gal", got)
	}
	if got := *entries[0].KegTemperature; got < 39.1 || got > 39.3 {
		t.Errorf("temperature = %v, want 39.2", got)
	}
}

// The CSV is a data export: it records what was stored and what the device and
// BarHelper saw, and it has no unit column, so a display preference must not
// rescale it.
func TestHistoryCSVStaysInDeviceUnits(t *testing.T) {
	a := newTestAPI(t)
	k := metricVolumeKeg(a, "keg-1")
	if err := a.store.AppendLog(k, time.Now()); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}
	setUS(t, a)

	rec := a.do(http.MethodGet, "/api/kegs/keg-1/log/csv", nil)
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if !strings.Contains(body, "10") || !strings.Contains(body, "4") {
		t.Errorf("csv lost the device-unit readings:\n%s", body)
	}
	if strings.Contains(body, "2.64") || strings.Contains(body, "39.2") {
		t.Errorf("csv was converted to the display units:\n%s", body)
	}
}

// The ESP32 display has fixed firmware and must never see a converted value.
func TestGetKegForDisplayIgnoresDisplayUnits(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x003.000", "vw\x0062\x004.000", "vw\x0076\x0019.0",
		"vw\x0071\x001", "vw\x0075\x001")
	if err := a.store.SaveTap(&store.Tap{
		ID: "tap-1", Name: "Pale Ale", KegID: "keg-1", DeviceID: "AB12CD",
	}); err != nil {
		t.Fatalf("SaveTap: %v", err)
	}

	rec := a.do(http.MethodGet, "/get_keg/ab12cd", nil)
	assertStatus(t, rec, http.StatusOK)
	var before displayTap
	a.decode(rec, &before)

	setUS(t, a)

	rec = a.do(http.MethodGet, "/get_keg/ab12cd", nil)
	assertStatus(t, rec, http.StatusOK)
	var after displayTap
	a.decode(rec, &after)

	if *before.CurrentWeight != *after.CurrentWeight {
		t.Errorf("current_weight moved from %v to %v after a display-unit change",
			*before.CurrentWeight, *after.CurrentWeight)
	}
	if *before.KegCapacity != *after.KegCapacity {
		t.Errorf("keg_capacity moved from %v to %v after a display-unit change",
			*before.KegCapacity, *after.KegCapacity)
	}
}

// The overlay strength is stored as whole percent, matching the settings
// slider and what that page reads back into it, but a CSS colour needs the
// 0-1 fraction.
func TestThemeCSSConvertsOpacityPercentToAlpha(t *testing.T) {
	tests := []struct {
		name    string
		stored  string
		wantCSS string
	}{
		{"whole percent", "15", "--bg-opacity: 0.15"},
		{"half", "50", "--bg-opacity: 0.5"},
		{"none", "0", "--bg-opacity: 0"},
		{"full", "100", "--bg-opacity: 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAPI(t)
			rec := a.do(http.MethodPost, "/api/config/theme",
				map[string]string{"bg_opacity": tt.stored})
			assertStatus(t, rec, http.StatusOK)

			rec = a.do(http.MethodGet, "/theme.css", nil)
			assertStatus(t, rec, http.StatusOK)
			if body := rec.Body.String(); !strings.Contains(body, tt.wantCSS) {
				t.Errorf("theme.css does not contain %q:\n%s", tt.wantCSS, body)
			}
		})
	}
}

// Out-of-range and unparseable values leave the declaration out entirely
// rather than being clamped.
func TestThemeCSSDropsAnUnusableOpacity(t *testing.T) {
	for _, stored := range []string{"-1", "101", "", "abc"} {
		a := newTestAPI(t)
		rec := a.do(http.MethodPost, "/api/config/theme",
			map[string]string{"bg_opacity": stored})
		assertStatus(t, rec, http.StatusOK)

		rec = a.do(http.MethodGet, "/theme.css", nil)
		if body := rec.Body.String(); strings.Contains(body, "--bg-opacity") {
			t.Errorf("opacity %q produced a declaration:\n%s", stored, body)
		}
	}
}

// The settings page posts every theme field as a string. A boolean or a
// number for these two made the whole body fail to decode, which is what made
// Save report failure however valid the rest of the form was.
func TestThemeAcceptsTheSettingsPagePayload(t *testing.T) {
	a := newTestAPI(t)

	rec := a.do(http.MethodPost, "/api/config/theme", map[string]any{
		"accent_color": "#f59e0b",
		"bg_color":     "#020617",
		"card_bg":      "#0f172a",
		"text_color":   "#dfdfdf",
		"font_family":  "Outfit",
		"bg_image":     "1",
		"bg_opacity":   "15",
	})
	assertStatus(t, rec, http.StatusOK)

	rec = a.do(http.MethodGet, "/api/config/theme", nil)
	assertStatus(t, rec, http.StatusOK)
	var theme store.Theme
	a.decode(rec, &theme)
	if theme.AccentColor != "#f59e0b" || theme.BgOpacity != "15" || theme.BgImage != "1" {
		t.Errorf("theme did not round trip: %+v", theme)
	}
}
