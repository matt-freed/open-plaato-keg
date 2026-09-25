package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/matt-freed/open-plaato-keg/internal/blynk"
	"github.com/matt-freed/open-plaato-keg/internal/config"
	"github.com/matt-freed/open-plaato-keg/internal/events"
	"github.com/matt-freed/open-plaato-keg/internal/keg"
	"github.com/matt-freed/open-plaato-keg/internal/plaato"
	"github.com/matt-freed/open-plaato-keg/internal/store"
	"github.com/matt-freed/open-plaato-keg/internal/ws"
	"github.com/matt-freed/open-plaato-keg/web"
)

type testAPI struct {
	t       *testing.T
	handler http.Handler
	store   *store.Store
	bus     *events.Bus
	dataDir string
}

func newTestAPI(t *testing.T) *testAPI {
	t.Helper()

	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	dataDir := t.TempDir()
	cfg := config.Config{DatabaseFilePath: dataDir + "/test.db"}

	bus := events.NewBus()
	commander := keg.NewCommander(keg.NewRegistry())
	hub := ws.NewHub(st)
	srv := NewServer(st, commander, hub, bus, cfg, "test", web.Static())

	return &testAPI{t: t, handler: srv.Handler(), store: st, bus: bus, dataDir: dataDir}
}

func (a *testAPI) do(method, path string, body any) *httptest.ResponseRecorder {
	a.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			a.t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

func (a *testAPI) decode(rec *httptest.ResponseRecorder, dst any) {
	a.t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		a.t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
}

// storeKeg seeds a keg as if the device had reported it.
func (a *testAPI) storeKeg(id string, bodies ...string) *store.Keg {
	a.t.Helper()
	frames := make([]blynk.Frame, len(bodies))
	for i, b := range bodies {
		frames[i] = blynk.Frame{Cmd: blynk.CmdHardware, MsgID: uint16(i + 1), Body: []byte(b)}
	}
	k, err := a.store.ApplyPacket(id, plaato.Decode(frames, false))
	if err != nil {
		a.t.Fatalf("ApplyPacket: %v", err)
	}
	return k
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
}

func TestAlive(t *testing.T) {
	a := newTestAPI(t)
	rec := a.do(http.MethodGet, "/api/alive", nil)
	assertStatus(t, rec, http.StatusOK)

	var body map[string]string
	a.decode(rec, &body)
	if body["status"] != "ok" || body["version"] != "test" {
		t.Errorf("body = %v", body)
	}
}

func TestListKegsIsAlwaysAnArray(t *testing.T) {
	a := newTestAPI(t)

	// An empty installation must return [], not null, so the UI can iterate.
	rec := a.do(http.MethodGet, "/api/kegs", nil)
	assertStatus(t, rec, http.StatusOK)
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("body = %s, want []", got)
	}

	a.storeKeg("keg-1", "vw\x0051\x003.802")
	rec = a.do(http.MethodGet, "/api/kegs", nil)
	var kegs []store.Keg
	a.decode(rec, &kegs)
	if len(kegs) != 1 || kegs[0].ID != "keg-1" {
		t.Errorf("kegs = %+v", kegs)
	}
}

// Values are real JSON types, so the UI does not have to parse strings.
func TestKegJSONIsTyped(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x003.802", "vw\x0049\x00255", "vw\x0071\x001")

	rec := a.do(http.MethodGet, "/api/kegs/keg-1", nil)
	assertStatus(t, rec, http.StatusOK)

	var raw map[string]any
	a.decode(rec, &raw)

	if _, ok := raw["amount_left"].(float64); !ok {
		t.Errorf("amount_left = %#v (%T), want a number", raw["amount_left"], raw["amount_left"])
	}
	if pouring, ok := raw["is_pouring"].(bool); !ok || !pouring {
		t.Errorf("is_pouring = %#v (%T), want true as a bool", raw["is_pouring"], raw["is_pouring"])
	}
	if _, ok := raw["unit"].(float64); !ok {
		t.Errorf("unit = %#v (%T), want a number", raw["unit"], raw["unit"])
	}
	// A pin the device never sent is null rather than a zero.
	if raw["keg_temperature"] != nil {
		t.Errorf("keg_temperature = %#v, want null", raw["keg_temperature"])
	}
}

// An unknown id is a 404 rather than an empty record.
func TestGetUnknownKegIs404(t *testing.T) {
	a := newTestAPI(t)
	rec := a.do(http.MethodGet, "/api/kegs/does-not-exist", nil)
	assertStatus(t, rec, http.StatusNotFound)

	var body errorResponse
	a.decode(rec, &body)
	if body.Error != "not_found" {
		t.Errorf("error = %q, want not_found", body.Error)
	}
}

func TestListKegIDsAndConnected(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x001.000")

	rec := a.do(http.MethodGet, "/api/kegs/devices", nil)
	assertStatus(t, rec, http.StatusOK)
	var ids []string
	a.decode(rec, &ids)
	if len(ids) != 1 || ids[0] != "keg-1" {
		t.Errorf("devices = %v", ids)
	}

	// Nothing is connected in this test, so the list is empty but present.
	rec = a.do(http.MethodGet, "/api/kegs/connected", nil)
	assertStatus(t, rec, http.StatusOK)
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("connected = %s, want []", got)
	}
}

// "devices" and "connected" must not be captured by the /{id} route.
func TestReservedKegPathsAreNotTreatedAsIDs(t *testing.T) {
	a := newTestAPI(t)
	for _, path := range []string{"/api/kegs/devices", "/api/kegs/connected"} {
		rec := a.do(http.MethodGet, path, nil)
		assertStatus(t, rec, http.StatusOK)
		if strings.Contains(rec.Body.String(), "not_found") {
			t.Errorf("%s was routed as a keg id", path)
		}
	}
}

func TestSetLabelAndDisplayMode(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x001.000")

	rec := a.do(http.MethodPost, "/api/kegs/keg-1/label", map[string]string{"value": "  Pale Ale  "})
	assertStatus(t, rec, http.StatusOK)

	k, _ := a.store.GetKeg("keg-1")
	if k.Label != "Pale Ale" {
		t.Errorf("Label = %q, want it trimmed", k.Label)
	}

	rec = a.do(http.MethodPost, "/api/kegs/keg-1/display-mode",
		map[string]string{"value": store.DisplayPercentPrimary})
	assertStatus(t, rec, http.StatusOK)

	rec = a.do(http.MethodPost, "/api/kegs/keg-1/display-mode", map[string]string{"value": "sideways"})
	assertStatus(t, rec, http.StatusBadRequest)
}

// A missing value is a 400, not a crash.
func TestCommandWithoutValueIsRejected(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x001.000")

	for _, path := range []string{"og", "fg", "co2-capacity", "max-keg-volume", "temperature-offset"} {
		rec := a.do(http.MethodPost, "/api/kegs/keg-1/"+path, map[string]any{})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s without a value: status = %d, want 400", path, rec.Code)
		}
	}
}

// The UI posts numbers from text inputs, so both forms must work.
func TestCommandAcceptsNumberOrString(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x001.000")

	for _, value := range []any{1.052, "1.052"} {
		rec := a.do(http.MethodPost, "/api/kegs/keg-1/og", map[string]any{"value": value})
		assertStatus(t, rec, http.StatusOK)

		k, _ := a.store.GetKeg("keg-1")
		if k.OG == nil || *k.OG != 1.052 {
			t.Errorf("value %#v: OG = %v, want 1.052", value, k.OG)
		}
		if _, err := a.store.UpdateKeg("keg-1", func(k *store.Keg) { k.OG = nil }); err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
}

func TestSetABVComputesFromGravities(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x001.000")

	rec := a.do(http.MethodPost, "/api/kegs/keg-1/abv",
		map[string]any{"og": "1.050", "fg": "1.010"})
	assertStatus(t, rec, http.StatusOK)

	var body map[string]any
	a.decode(rec, &body)
	abv, ok := body["value"].(float64)
	if !ok {
		t.Fatalf("value = %#v, want a number", body["value"])
	}
	if diff := abv - 5.25; diff > 0.001 || diff < -0.001 {
		t.Errorf("abv = %v, want 5.25", abv)
	}

	k, _ := a.store.GetKeg("keg-1")
	if k.ABV == nil || k.OG == nil || k.FG == nil {
		t.Errorf("gravities were not stored: %+v", k)
	}

	rec = a.do(http.MethodPost, "/api/kegs/keg-1/abv", map[string]any{"og": "1.050"})
	assertStatus(t, rec, http.StatusBadRequest)
}

// A command for a keg with no live connection is a 503, so the UI can say the
// keg is offline rather than reporting a generic failure.
func TestCommandToDisconnectedKegIs503(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x001.000")

	rec := a.do(http.MethodPost, "/api/kegs/keg-1/tare", nil)
	assertStatus(t, rec, http.StatusServiceUnavailable)

	var body errorResponse
	a.decode(rec, &body)
	if body.Error != "not_connected" {
		t.Errorf("error = %q, want not_connected", body.Error)
	}
}

func TestEnumCommandValidation(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x001.000")

	// A bad value is rejected before the keg's connection is even considered.
	rec := a.do(http.MethodPost, "/api/kegs/keg-1/unit", map[string]string{"value": "furlongs"})
	assertStatus(t, rec, http.StatusBadRequest)

	rec = a.do(http.MethodPost, "/api/kegs/keg-1/sensitivity", map[string]string{"value": "9"})
	assertStatus(t, rec, http.StatusBadRequest)

	// A valid value gets as far as the disconnected keg.
	rec = a.do(http.MethodPost, "/api/kegs/keg-1/unit", map[string]string{"value": "metric"})
	assertStatus(t, rec, http.StatusServiceUnavailable)
}

// Beer style is kept locally because the device never reports the pin back.
func TestBeerStyleIsStoredEvenWhenOffline(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x001.000")

	rec := a.do(http.MethodPost, "/api/kegs/keg-1/beer-style", map[string]string{"value": "Saison"})
	assertStatus(t, rec, http.StatusOK)

	var body map[string]any
	a.decode(rec, &body)
	if body["sent_to_device"] != false {
		t.Errorf("sent_to_device = %v, want false for an offline keg", body["sent_to_device"])
	}

	k, _ := a.store.GetKeg("keg-1")
	if k.BeerStyle != "Saison" {
		t.Errorf("BeerStyle = %q, want it stored locally", k.BeerStyle)
	}
}

func TestKegOrder(t *testing.T) {
	a := newTestAPI(t)
	for _, id := range []string{"a", "b", "c"} {
		a.storeKeg(id, "vw\x0051\x001.000")
	}

	rec := a.do(http.MethodPost, "/api/kegs/order",
		map[string]any{"ordered_ids": []string{"c", "a", "b"}})
	assertStatus(t, rec, http.StatusOK)

	kegs, _ := a.store.ListKegs()
	want := []string{"c", "a", "b"}
	for i, k := range kegs {
		if k.ID != want[i] {
			t.Errorf("position %d = %q, want %q", i, k.ID, want[i])
		}
	}

	rec = a.do(http.MethodPost, "/api/kegs/order", map[string]any{"ordered_ids": []string{}})
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestDeleteKegPublishesRemoval(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x001.000")

	sub, cancel := a.bus.Subscribe()
	defer cancel()

	rec := a.do(http.MethodPost, "/api/kegs/keg-1/delete", nil)
	assertStatus(t, rec, http.StatusOK)

	select {
	case e := <-sub:
		if e.Kind != events.KegRemoved || e.KegID != "keg-1" {
			t.Errorf("event = %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no removal event")
	}

	rec = a.do(http.MethodGet, "/api/kegs/keg-1", nil)
	assertStatus(t, rec, http.StatusNotFound)

	// Deleting it again is a 404, not a second success.
	rec = a.do(http.MethodPost, "/api/kegs/keg-1/delete", nil)
	assertStatus(t, rec, http.StatusNotFound)
}

func TestKegHistory(t *testing.T) {
	a := newTestAPI(t)
	k := a.storeKeg("keg-1", "vw\x0051\x003.000")

	now := time.Now()
	for i := 0; i < 3; i++ {
		if err := a.store.AppendLog(k, now.Add(-time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
	}

	rec := a.do(http.MethodGet, "/api/kegs/keg-1/log?range=24h", nil)
	assertStatus(t, rec, http.StatusOK)
	var entries []store.LogEntry
	a.decode(rec, &entries)
	if len(entries) != 3 {
		t.Errorf("got %d entries, want 3", len(entries))
	}

	rec = a.do(http.MethodGet, "/api/kegs/keg-1/log/csv", nil)
	assertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "keg-keg-1-log.csv") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if !strings.HasPrefix(rec.Body.String(), "timestamp,amount_left") {
		t.Errorf("csv = %q", rec.Body.String())
	}

	// History for an unknown keg is a 404.
	rec = a.do(http.MethodGet, "/api/kegs/nope/log", nil)
	assertStatus(t, rec, http.StatusNotFound)
}

// An unrecognised range falls back to the default rather than erroring.
func TestUnknownHistoryRangeFallsBack(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x001.000")
	rec := a.do(http.MethodGet, "/api/kegs/keg-1/log?range=nonsense", nil)
	assertStatus(t, rec, http.StatusOK)
}

func TestTapCRUDOverHTTP(t *testing.T) {
	a := newTestAPI(t)

	rec := a.do(http.MethodPost, "/api/taps/new", map[string]any{
		"tap_number": 1, "name": "Pale Ale", "brewery": "Home", "abv": "5.2",
	})
	assertStatus(t, rec, http.StatusOK)

	var created map[string]any
	a.decode(rec, &created)
	id, _ := created["id"].(string)
	if id == "" || id == "new" {
		t.Fatalf("id = %q, want a generated id", id)
	}

	rec = a.do(http.MethodGet, "/api/taps/"+id, nil)
	assertStatus(t, rec, http.StatusOK)
	var tap store.Tap
	a.decode(rec, &tap)
	if tap.Name != "Pale Ale" || tap.ABV == nil || *tap.ABV != 5.2 {
		t.Errorf("tap = %+v", tap)
	}
	if tap.Color != store.DefaultTapColor {
		t.Errorf("Color = %q, want the default", tap.Color)
	}

	rec = a.do(http.MethodGet, "/api/taps", nil)
	assertStatus(t, rec, http.StatusOK)
	var taps []store.Tap
	a.decode(rec, &taps)
	if len(taps) != 1 {
		t.Errorf("got %d taps, want 1", len(taps))
	}

	rec = a.do(http.MethodPost, "/api/taps/"+id+"/delete", nil)
	assertStatus(t, rec, http.StatusOK)
	rec = a.do(http.MethodGet, "/api/taps/"+id, nil)
	assertStatus(t, rec, http.StatusNotFound)
}

func TestBeverageDerivesABVFromGravities(t *testing.T) {
	a := newTestAPI(t)

	rec := a.do(http.MethodPost, "/api/beverages/new", map[string]any{
		"name": "Saison", "og": "1.055", "fg": "1.010",
	})
	assertStatus(t, rec, http.StatusOK)

	var created struct {
		Beverage store.Beverage `json:"beverage"`
	}
	a.decode(rec, &created)
	if created.Beverage.ABV == nil {
		t.Fatal("ABV was not derived from the gravities")
	}
	if diff := *created.Beverage.ABV - 5.906; diff > 0.01 || diff < -0.01 {
		t.Errorf("ABV = %v, want about 5.9", *created.Beverage.ABV)
	}
}

// An explicit strength must not be overwritten by the derived one.
func TestBeverageKeepsExplicitABV(t *testing.T) {
	a := newTestAPI(t)
	rec := a.do(http.MethodPost, "/api/beverages/new", map[string]any{
		"name": "Saison", "abv": 6.5, "og": "1.055", "fg": "1.010",
	})
	assertStatus(t, rec, http.StatusOK)

	var created struct {
		Beverage store.Beverage `json:"beverage"`
	}
	a.decode(rec, &created)
	if created.Beverage.ABV == nil || *created.Beverage.ABV != 6.5 {
		t.Errorf("ABV = %v, want the supplied 6.5", created.Beverage.ABV)
	}
}

// The open-tap display fetches its tap by the device id it was configured with.
func TestGetKegForDisplay(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x003.000", "vw\x0062\x004.000", "vw\x0076\x0019.0",
		"vw\x0071\x001", "vw\x0075\x001")

	if err := a.store.SaveTap(&store.Tap{
		ID: "tap-1", Name: "Pale Ale", Brewery: "Home", Description: "Crisp",
		TastingNotes: "Citrus", KegID: "keg-1", DeviceID: "AB12CD",
	}); err != nil {
		t.Fatalf("SaveTap: %v", err)
	}

	rec := a.do(http.MethodGet, "/get_keg/ab12cd", nil)
	assertStatus(t, rec, http.StatusOK)

	var out displayTap
	a.decode(rec, &out)
	if out.Name != "Pale Ale - Home" {
		t.Errorf("Name = %q", out.Name)
	}
	if out.Description != "Crisp | Citrus" {
		t.Errorf("Description = %q", out.Description)
	}
	if out.CurrentWeight == nil || *out.CurrentWeight != 7 {
		t.Errorf("CurrentWeight = %v, want 7 (4 kg empty + 3 kg beer)", out.CurrentWeight)
	}

	rec = a.do(http.MethodGet, "/get_keg/unknown", nil)
	assertStatus(t, rec, http.StatusNotFound)
}

func TestAppConfigEndpoints(t *testing.T) {
	a := newTestAPI(t)

	rec := a.do(http.MethodGet, "/api/config", nil)
	assertStatus(t, rec, http.StatusOK)
	var cfg store.AppConfig
	a.decode(rec, &cfg)
	if cfg.HomePage != store.HomePageTapList {
		t.Errorf("HomePage = %q, want the taplist default", cfg.HomePage)
	}

	rec = a.do(http.MethodPost, "/api/config/home-page", map[string]string{"home_page": "kegs"})
	assertStatus(t, rec, http.StatusOK)
	rec = a.do(http.MethodPost, "/api/config/time-format", map[string]string{"time_format": "24h"})
	assertStatus(t, rec, http.StatusOK)

	rec = a.do(http.MethodGet, "/api/config", nil)
	a.decode(rec, &cfg)
	if cfg.HomePage != store.HomePageKegs || cfg.TimeFormat != store.TimeFormat24h {
		t.Errorf("config = %+v", cfg)
	}
}

// The root redirects to whichever page the user chose.
func TestRootRedirectsToHomePage(t *testing.T) {
	a := newTestAPI(t)

	rec := a.do(http.MethodGet, "/", nil)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/taplist.html" {
		t.Errorf("status %d location %q, want a redirect to the taplist",
			rec.Code, rec.Header().Get("Location"))
	}

	if err := a.store.SetHomePage(store.HomePageKegs); err != nil {
		t.Fatalf("SetHomePage: %v", err)
	}
	rec = a.do(http.MethodGet, "/", nil)
	if rec.Header().Get("Location") != "/index.html" {
		t.Errorf("Location = %q, want /index.html", rec.Header().Get("Location"))
	}
}

func TestThemeCSS(t *testing.T) {
	a := newTestAPI(t)

	rec := a.do(http.MethodPost, "/api/config/theme", map[string]string{
		"accent_color": "#ff0000", "bg_opacity": "50", "font_family": "Inter, sans-serif",
	})
	assertStatus(t, rec, http.StatusOK)

	rec = a.do(http.MethodGet, "/theme.css", nil)
	assertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"--accent-color: #ff0000", "--bg-opacity: 0.5", "fonts.googleapis.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("theme.css does not contain %q:\n%s", want, body)
		}
	}
}

// Theme values are interpolated into a stylesheet, so anything that could
// close the declaration must be dropped.
func TestThemeCSSRejectsInjection(t *testing.T) {
	a := newTestAPI(t)

	rec := a.do(http.MethodPost, "/api/config/theme", map[string]string{
		"accent_color": "red; } body { display: none; } :root { --x: y",
		"bg_image":     "url(https://example.com/track.png)",
		"bg_opacity":   "150",
	})
	assertStatus(t, rec, http.StatusOK)

	rec = a.do(http.MethodGet, "/theme.css", nil)
	body := rec.Body.String()
	if strings.Contains(body, "display: none") || strings.Contains(body, "}") != strings.Contains(body, "}\n") {
		t.Errorf("injected CSS survived:\n%s", body)
	}
	if strings.Contains(strings.ToLower(body), "url(") {
		t.Errorf("a url() value survived:\n%s", body)
	}
	if strings.Contains(body, "--bg-opacity") {
		t.Errorf("an out-of-range opacity survived:\n%s", body)
	}
}

// jpegBytes builds a JPEG of the given size, for upload tests.
func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func (a *testAPI) upload(path, filename, contentType string, content []byte) *httptest.ResponseRecorder {
	a.t.Helper()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	header := make(map[string][]string)
	header["Content-Disposition"] = []string{
		fmt.Sprintf(`form-data; name="image"; filename=%q`, filename)}
	header["Content-Type"] = []string{contentType}

	part, err := mw.CreatePart(header)
	if err != nil {
		a.t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		a.t.Fatalf("write part: %v", err)
	}
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

func TestTapHandleUpload(t *testing.T) {
	a := newTestAPI(t)

	rec := a.upload("/api/tap-handles/upload", "handle.jpg", "image/jpeg",
		jpegBytes(t, TapHandleSize, TapHandleSize))
	assertStatus(t, rec, http.StatusOK)

	var body map[string]string
	a.decode(rec, &body)
	filename := body["filename"]
	if !strings.HasSuffix(filename, ".jpg") {
		t.Fatalf("filename = %q", filename)
	}

	if _, err := os.Stat(a.dataDir + "/tap-handles/" + filename); err != nil {
		t.Errorf("the uploaded file was not written: %v", err)
	}

	rec = a.do(http.MethodGet, "/api/tap-handles", nil)
	assertStatus(t, rec, http.StatusOK)
	var handles []store.TapHandle
	a.decode(rec, &handles)
	if len(handles) != 1 || handles[0].Filename != filename {
		t.Errorf("handles = %+v", handles)
	}

	rec = a.do(http.MethodGet, "/uploads/tap-handles/"+filename, nil)
	assertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q", ct)
	}

	rec = a.do(http.MethodPost, "/api/tap-handles/"+filename+"/delete", nil)
	assertStatus(t, rec, http.StatusOK)
	if _, err := os.Stat(a.dataDir + "/tap-handles/" + filename); !os.IsNotExist(err) {
		t.Error("the file survived the delete")
	}
}

// The tap list lays handles out on a fixed grid, so the size is enforced.
func TestTapHandleUploadRejectsWrongSize(t *testing.T) {
	a := newTestAPI(t)
	rec := a.upload("/api/tap-handles/upload", "handle.jpg", "image/jpeg", jpegBytes(t, 150, 150))
	assertStatus(t, rec, http.StatusBadRequest)

	var body errorResponse
	a.decode(rec, &body)
	if body.Error != "invalid_dimensions" {
		t.Errorf("error = %q, want invalid_dimensions", body.Error)
	}
}

func TestTapHandleUploadRejectsNonJPEG(t *testing.T) {
	a := newTestAPI(t)
	rec := a.upload("/api/tap-handles/upload", "handle.png", "image/png", []byte("not an image"))
	assertStatus(t, rec, http.StatusBadRequest)
}

// A crafted filename must never reach outside the uploads directory.
func TestTapHandlePathTraversalIsRejected(t *testing.T) {
	a := newTestAPI(t)
	for _, name := range []string{"..%2f..%2fetc%2fpasswd", "no-extension", "evil.sh"} {
		rec := a.do(http.MethodGet, "/uploads/tap-handles/"+name, nil)
		if rec.Code == http.StatusOK {
			t.Errorf("%q was served", name)
		}
	}
}

func TestBackgroundUploadAndDelete(t *testing.T) {
	a := newTestAPI(t)

	rec := a.do(http.MethodGet, "/uploads/background", nil)
	assertStatus(t, rec, http.StatusNotFound)

	rec = a.upload("/api/uploads/background", "bg.jpg", "image/jpeg", jpegBytes(t, 64, 64))
	assertStatus(t, rec, http.StatusOK)

	rec = a.do(http.MethodGet, "/uploads/background", nil)
	assertStatus(t, rec, http.StatusOK)

	rec = a.do(http.MethodDelete, "/api/uploads/background", nil)
	assertStatus(t, rec, http.StatusOK)

	rec = a.do(http.MethodGet, "/uploads/background", nil)
	assertStatus(t, rec, http.StatusNotFound)
}

// Replacing a background with a different format must not leave the old one
// behind to be served instead.
func TestBackgroundReplacementRemovesTheOldFormat(t *testing.T) {
	a := newTestAPI(t)

	rec := a.upload("/api/uploads/background", "bg.jpg", "image/jpeg", jpegBytes(t, 64, 64))
	assertStatus(t, rec, http.StatusOK)
	if _, err := os.Stat(a.dataDir + "/background.jpg"); err != nil {
		t.Fatalf("the jpeg was not written: %v", err)
	}

	rec = a.upload("/api/uploads/background", "bg.png", "image/png", []byte("\x89PNG\r\n\x1a\n fake"))
	assertStatus(t, rec, http.StatusOK)

	if _, err := os.Stat(a.dataDir + "/background.jpg"); !os.IsNotExist(err) {
		t.Error("the previous jpeg background was left in place")
	}
}

func TestMalformedJSONIsRejected(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x001.000")

	req := httptest.NewRequest(http.MethodPost, "/api/kegs/keg-1/label",
		strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestStaticUIIsServed(t *testing.T) {
	a := newTestAPI(t)
	for _, path := range []string{"/index.html", "/taplist.html", "/style.css"} {
		rec := a.do(http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s: empty body", path)
		}
	}
}
