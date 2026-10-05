package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/matt-freed/open-plaato-keg/internal/blynk"
	"github.com/matt-freed/open-plaato-keg/internal/config"
	"github.com/matt-freed/open-plaato-keg/internal/events"
	"github.com/matt-freed/open-plaato-keg/internal/keg"
	"github.com/matt-freed/open-plaato-keg/internal/logbuf"
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
	logs    *logbuf.Buffer
	level   *slog.LevelVar
}

func newTestAPI(t *testing.T) *testAPI {
	t.Helper()

	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	bus := events.NewBus()
	commander := keg.NewCommander(keg.NewRegistry())
	hub := ws.NewHub(st, nil)
	logs, level := logbuf.New(), new(slog.LevelVar)
	cfg := config.Config{BarHelper: config.BarHelperConfig{APIKey: "test-api-key"}}
	srv := NewServer(st, commander, hub, bus, "test", web.Static(), System{
		Logs: logs, Level: level, Env: cfg.Settings(),
	})

	return &testAPI{t: t, handler: srv.Handler(), store: st, bus: bus, logs: logs, level: level}
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

func TestSetLabel(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x001.000")

	rec := a.do(http.MethodPost, "/api/kegs/keg-1/label", map[string]string{"value": "  Pale Ale  "})
	assertStatus(t, rec, http.StatusOK)

	k, _ := a.store.GetKeg("keg-1")
	if k.Label != "Pale Ale" {
		t.Errorf("Label = %q, want it trimmed", k.Label)
	}
}

// A missing value is a 400, not a crash.
func TestCommandWithoutValueIsRejected(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x001.000")

	for _, path := range []string{"co2-capacity", "max-keg-volume", "temperature-offset"} {
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
		rec := a.do(http.MethodPost, "/api/kegs/keg-1/co2-capacity", map[string]any{"value": value})
		assertStatus(t, rec, http.StatusOK)

		k, _ := a.store.GetKeg("keg-1")
		if k.CO2Capacity == nil || *k.CO2Capacity != 1.052 {
			t.Errorf("value %#v: CO2Capacity = %v, want 1.052", value, k.CO2Capacity)
		}
		if _, err := a.store.UpdateKeg("keg-1", func(k *store.Keg) { k.CO2Capacity = nil }); err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
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

// Clearing a keg's history empties every range and keeps the keg.
func TestClearKegHistory(t *testing.T) {
	a := newTestAPI(t)
	k := a.storeKeg("keg-1", "vw\x0051\x003.000")
	now := time.Now()
	for _, at := range []time.Time{now, now.Add(-48 * time.Hour)} {
		if err := a.store.AppendLog(k, at); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
	}

	rec := a.do(http.MethodPost, "/api/kegs/keg-1/log/clear", nil)
	assertStatus(t, rec, http.StatusOK)
	var resp struct {
		Deleted int `json:"deleted"`
	}
	a.decode(rec, &resp)
	if resp.Deleted != 2 {
		t.Errorf("deleted = %d, want 2", resp.Deleted)
	}

	rec = a.do(http.MethodGet, "/api/kegs/keg-1/log?range=30d", nil)
	var entries []store.LogEntry
	a.decode(rec, &entries)
	if len(entries) != 0 {
		t.Errorf("got %d entries after clearing, want 0", len(entries))
	}
	assertStatus(t, a.do(http.MethodGet, "/api/kegs/keg-1", nil), http.StatusOK)

	rec = a.do(http.MethodPost, "/api/kegs/nope/log/clear", nil)
	assertStatus(t, rec, http.StatusNotFound)
}

// Long ranges are averaged to their step for the chart, and say so in a
// header, while the CSV export still carries every stored row.
func TestLongHistoryRangeIsSampled(t *testing.T) {
	a := newTestAPI(t)
	k := a.storeKeg("keg-1", "vw\x0051\x003.000")
	// Ten readings a minute apart, 200 days ago, all inside one 8-hour step.
	start := time.Now().Add(-200 * 24 * time.Hour).Truncate(8 * time.Hour).Add(time.Hour)
	for i := range 10 {
		if err := a.store.AppendLog(k, start.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
	}

	rec := a.do(http.MethodGet, "/api/kegs/keg-1/log?range=1y", nil)
	assertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("X-Log-Step-Seconds"); got != "28800" {
		t.Errorf("X-Log-Step-Seconds = %q, want 28800", got)
	}
	var entries []store.LogEntry
	a.decode(rec, &entries)
	if len(entries) != 1 {
		t.Errorf("got %d entries for 1y, want the readings averaged into 1", len(entries))
	}

	// A range short enough to chart every reading reports a step of zero.
	rec = a.do(http.MethodGet, "/api/kegs/keg-1/log?range=24h", nil)
	if got := rec.Header().Get("X-Log-Step-Seconds"); got != "0" {
		t.Errorf("24h X-Log-Step-Seconds = %q, want 0", got)
	}

	rec = a.do(http.MethodGet, "/api/kegs/keg-1/log/csv?range=1y", nil)
	assertStatus(t, rec, http.StatusOK)
	if lines := strings.Count(rec.Body.String(), "\n"); lines != 11 {
		t.Errorf("csv has %d lines, want a header and all 10 rows", lines)
	}
}

// The step a window is averaged to depends only on its length, so a custom
// window gets the same step as a preset of the same length, and no window
// sends more than maxChartPoints readings.
func TestSampleStep(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		window time.Duration
		want   time.Duration
	}{
		{time.Hour, 0},
		{24 * time.Hour, 0},
		{36 * time.Hour, 10 * time.Minute},
		{7 * day, 10 * time.Minute},
		{30 * day, 30 * time.Minute},
		{90 * day, 2 * time.Hour},
		{180 * day, 3 * time.Hour},
		{365 * day, 8 * time.Hour},
		{3 * 365 * day, 24 * time.Hour},
		{10 * 365 * day, 3 * day},
	}
	for _, c := range cases {
		got := sampleStep(c.window)
		if got != c.want {
			t.Errorf("sampleStep(%v) = %v, want %v", c.window, got, c.want)
		}
		if got > 0 && c.window/got > maxChartPoints {
			t.Errorf("sampleStep(%v) = %v gives %d points", c.window, got, c.window/got)
		}
	}
}

// A custom window reads exactly the times asked for, and its pours too.
func TestCustomHistoryWindow(t *testing.T) {
	a := newTestAPI(t)
	k := a.storeKeg("keg-1", "vw\x0051\x003.000")
	base := time.Now().Add(-100 * 24 * time.Hour).Truncate(time.Hour)
	for i := range 5 {
		if err := a.store.AppendLog(k, base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
	}

	// Hours 1 to 3 of the five, two hours long, so every reading is sent.
	from, to := base.Add(time.Hour).Unix(), base.Add(3*time.Hour).Unix()
	q := "?from=" + strconv.FormatInt(from, 10) + "&to=" + strconv.FormatInt(to, 10)

	rec := a.do(http.MethodGet, "/api/kegs/keg-1/log"+q, nil)
	assertStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("X-Log-Step-Seconds"); got != "0" {
		t.Errorf("X-Log-Step-Seconds = %q, want 0 for a two-hour window", got)
	}
	var entries []store.LogEntry
	a.decode(rec, &entries)
	if len(entries) != 3 {
		t.Errorf("got %d entries, want the 3 inside the window", len(entries))
	}

	rec = a.do(http.MethodGet, "/api/kegs/keg-1/log/csv"+q, nil)
	assertStatus(t, rec, http.StatusOK)
	if lines := strings.Count(rec.Body.String(), "\n"); lines != 4 {
		t.Errorf("csv has %d lines, want a header and 3 rows", lines)
	}

	assertStatus(t, a.do(http.MethodGet, "/api/kegs/keg-1/pours"+q, nil), http.StatusOK)
}

func TestCustomHistoryWindowRejectsBadTimes(t *testing.T) {
	a := newTestAPI(t)
	a.storeKeg("keg-1", "vw\x0051\x003.000")
	for _, q := range []string{
		"?from=100",        // to missing
		"?from=abc&to=200", // not a number
		"?from=200&to=100", // ends before it starts
		"?from=200&to=200", // empty
	} {
		for _, path := range []string{"/log", "/log/csv", "/pours"} {
			rec := a.do(http.MethodGet, "/api/kegs/keg-1"+path+q, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s%s: status %d, want 400", path, q, rec.Code)
			}
		}
	}
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

func TestTapSRM(t *testing.T) {
	a := newTestAPI(t)

	rec := a.do(http.MethodPost, "/api/taps/new", map[string]any{"name": "Stout", "srm": "38"})
	assertStatus(t, rec, http.StatusOK)
	var created struct {
		Tap store.Tap `json:"tap"`
	}
	a.decode(rec, &created)
	if created.Tap.SRM == nil || *created.Tap.SRM != 38 {
		t.Errorf("SRM = %v, want 38", created.Tap.SRM)
	}

	rec = a.do(http.MethodPost, "/api/taps/new", map[string]any{"name": "Oops", "srm": -1})
	assertStatus(t, rec, http.StatusBadRequest)
}

// A drink's colour is an SRM or a named preset, never both.
func TestColorPresetValidation(t *testing.T) {
	a := newTestAPI(t)
	const path = "/api/taps/new"
	rec := a.do(http.MethodPost, path, map[string]any{"name": "Water", "color_preset": "clear"})
	assertStatus(t, rec, http.StatusOK)
	var created struct {
		Tap store.Tap `json:"tap"`
	}
	a.decode(rec, &created)
	if created.Tap.ColorPreset != "clear" || created.Tap.SRM != nil {
		t.Errorf("saved %+v", created.Tap)
	}

	rec = a.do(http.MethodPost, path, map[string]any{"name": "Oops", "color_preset": "chartreuse"})
	assertStatus(t, rec, http.StatusBadRequest)
	rec = a.do(http.MethodPost, path, map[string]any{"name": "Oops", "color_preset": "pink", "srm": 4})
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestTapKeggedDate(t *testing.T) {
	a := newTestAPI(t)

	rec := a.do(http.MethodPost, "/api/taps/new", map[string]any{"name": "Stout", "kegged_date": "03.09.2026"})
	assertStatus(t, rec, http.StatusOK)
	var created struct {
		Tap store.Tap `json:"tap"`
	}
	a.decode(rec, &created)
	if created.Tap.KeggedDate != "2026-09-03" {
		t.Errorf("kegged_date = %q, want it stored as 2026-09-03", created.Tap.KeggedDate)
	}

	rec = a.do(http.MethodPost, "/api/taps/new", map[string]any{"name": "Oops", "kegged_date": "next Friday"})
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestTapOrder(t *testing.T) {
	a := newTestAPI(t)
	for _, id := range []string{"a", "b", "c"} {
		if err := a.store.SaveTap(&store.Tap{ID: id}); err != nil {
			t.Fatalf("SaveTap: %v", err)
		}
	}

	rec := a.do(http.MethodPost, "/api/taps/order",
		map[string]any{"ordered_ids": []string{"b", "c", "a"}})
	assertStatus(t, rec, http.StatusOK)

	taps, _ := a.store.ListTaps()
	want := []string{"b", "c", "a"}
	for i, tap := range taps {
		if tap.ID != want[i] {
			t.Errorf("position %d = %q, want %q", i, tap.ID, want[i])
		}
	}

	rec = a.do(http.MethodPost, "/api/taps/order", map[string]any{"ordered_ids": []string{}})
	assertStatus(t, rec, http.StatusBadRequest)
	rec = a.do(http.MethodPost, "/api/taps/order", map[string]any{"ordered_ids": []string{"nope"}})
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
	if rec.Header().Get("Location") != "/kegs.html" {
		t.Errorf("Location = %q, want /kegs.html", rec.Header().Get("Location"))
	}
}

func TestThemeCSS(t *testing.T) {
	a := newTestAPI(t)

	rec := a.do(http.MethodPost, "/api/config/theme", map[string]string{
		"accent_color": "#ff0000", "font_family": "Inter, sans-serif",
	})
	assertStatus(t, rec, http.StatusOK)

	rec = a.do(http.MethodGet, "/theme.css", nil)
	assertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"--accent-color: #ff0000", "fonts.googleapis.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("theme.css does not contain %q:\n%s", want, body)
		}
	}
}

// The settings page stores bare family names. theme.css serves them as full
// stacks, so a font that fails to load falls back to the system font, and
// "System" is the system stack with nothing fetched from Google Fonts.
func TestThemeCSSFontStacks(t *testing.T) {
	a := newTestAPI(t)

	rec := a.do(http.MethodPost, "/api/config/theme", map[string]string{
		"font_family": "System", "taplist_title_font": "Playfair Display",
	})
	assertStatus(t, rec, http.StatusOK)

	body := a.do(http.MethodGet, "/theme.css", nil).Body.String()
	for _, want := range []string{
		"--font-family: system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif;",
		"--taplist-title-font: 'Playfair Display', system-ui,",
		"family=Playfair+Display",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("theme.css does not contain %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "family=System") {
		t.Errorf("theme.css imports System from Google Fonts:\n%s", body)
	}
}

// Theme values are interpolated into a stylesheet, so anything that could
// close the declaration must be dropped.
func TestThemeCSSRejectsInjection(t *testing.T) {
	a := newTestAPI(t)

	rec := a.do(http.MethodPost, "/api/config/theme", map[string]string{
		"accent_color": "red; } body { display: none; } :root { --x: y",
		"bg_color":     "url(https://example.com/track.png)",
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
	for _, path := range []string{"/kegs.html", "/taplist.html", "/style.css"} {
		rec := a.do(http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s: empty body", path)
		}
	}
}

// The amount display is one setting for every keg, defaulting to the amount
// left; anything unrecognised falls back to that default.
func TestAmountDisplay(t *testing.T) {
	a := newTestAPI(t)

	var got map[string]string
	a.decode(a.do(http.MethodGet, "/api/config/amount-display", nil), &got)
	if got["amount_display"] != store.AmountDisplayAmount {
		t.Errorf("default = %q, want %q", got["amount_display"], store.AmountDisplayAmount)
	}

	rec := a.do(http.MethodPost, "/api/config/amount-display", map[string]string{"amount_display": "percent"})
	assertStatus(t, rec, http.StatusOK)
	if cfg, _ := a.store.GetAppConfig(); cfg.AmountDisplay != store.AmountDisplayPercent {
		t.Errorf("AmountDisplay = %q after saving percent", cfg.AmountDisplay)
	}

	a.do(http.MethodPost, "/api/config/amount-display", map[string]string{"amount_display": "sideways"})
	if cfg, _ := a.store.GetAppConfig(); cfg.AmountDisplay != store.AmountDisplayAmount {
		t.Errorf("AmountDisplay = %q after an unknown value, want the default", cfg.AmountDisplay)
	}
}

// Linking a scale that another tap already uses is a 409 that names that tap,
// so Tap Setup can say where to unlink it.
func TestTapKegConflict(t *testing.T) {
	a := newTestAPI(t)
	rec := a.do(http.MethodPost, "/api/taps/tap-1", map[string]any{
		"tap_number": 3, "name": "Red Barn Amber", "keg_id": "keg-1",
	})
	assertStatus(t, rec, http.StatusOK)

	rec = a.do(http.MethodPost, "/api/taps/tap-2", map[string]any{
		"tap_number": 4, "name": "Pils", "keg_id": "keg-1",
	})
	assertStatus(t, rec, http.StatusConflict)
	var body errorResponse
	a.decode(rec, &body)
	if body.Error != "keg_in_use" || !strings.Contains(body.Detail, "Tap 3 (Red Barn Amber)") {
		t.Errorf("body = %+v, want keg_in_use naming Tap 3 (Red Barn Amber)", body)
	}
}

func TestTapLinks(t *testing.T) {
	a := newTestAPI(t)
	for _, tap := range []*store.Tap{
		{ID: "a", Name: "1", KegID: "keg-1"},
		{ID: "b", Name: "2", KegID: "keg-2"},
		{ID: "c", Name: "3"},
	} {
		if err := a.store.SaveTap(tap); err != nil {
			t.Fatalf("SaveTap: %v", err)
		}
	}

	rec := a.do(http.MethodPost, "/api/taps/links", map[string]any{"links": []map[string]string{
		{"tap_id": "a", "keg_id": "keg-2"},
		{"tap_id": "b", "keg_id": "keg-1"},
	}})
	assertStatus(t, rec, http.StatusOK)
	var resp struct {
		Taps []store.Tap `json:"taps"`
	}
	a.decode(rec, &resp)
	if len(resp.Taps) != 3 || resp.Taps[0].KegID != "keg-2" || resp.Taps[1].KegID != "keg-1" {
		t.Errorf("taps after swap = %+v", resp.Taps)
	}

	rec = a.do(http.MethodPost, "/api/taps/links", map[string]any{"links": []map[string]string{
		{"tap_id": "c", "keg_id": "keg-1"},
	}})
	assertStatus(t, rec, http.StatusConflict)

	rec = a.do(http.MethodPost, "/api/taps/links", map[string]any{"links": []map[string]string{}})
	assertStatus(t, rec, http.StatusBadRequest)
	rec = a.do(http.MethodPost, "/api/taps/links", map[string]any{"links": []map[string]string{
		{"tap_id": "nope", "keg_id": ""},
	}})
	assertStatus(t, rec, http.StatusNotFound)
}
