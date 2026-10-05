package api

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/matt-freed/open-plaato-keg/internal/store"
)

type logRowsResponse struct {
	Entries         []store.LogEntry `json:"entries"`
	HasEarlier      bool             `json:"has_earlier"`
	HasLater        bool             `json:"has_later"`
	AmountUnit      string           `json:"amount_unit"`
	TemperatureUnit string           `json:"temperature_unit"`
}

// seedEditLog stores n readings of the keg a minute apart, ending a minute
// ago, and returns the query for a window holding all of them.
func seedEditLog(t *testing.T, a *testAPI, k *store.Keg, n int) (base time.Time, window string) {
	t.Helper()
	base = time.Now().Add(-time.Duration(n+1) * time.Minute).Truncate(time.Second)
	for i := range n {
		if err := a.store.AppendLog(k, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
	}
	return base, "from=" + strconv.FormatInt(base.Unix()-1, 10) +
		"&to=" + strconv.FormatInt(time.Now().Unix(), 10)
}

func TestLogRowsArePagedInTheDisplayUnits(t *testing.T) {
	a := newTestAPI(t)
	k := metricVolumeKeg(a, "keg-1")
	base, window := seedEditLog(t, a, k, 5)
	setUS(t, a)

	rec := a.do(http.MethodGet, "/api/kegs/keg-1/log/rows?"+window+"&limit=2", nil)
	assertStatus(t, rec, http.StatusOK)
	var page logRowsResponse
	a.decode(rec, &page)
	if len(page.Entries) != 2 || page.Entries[0].Timestamp != base.Unix() {
		t.Fatalf("entries = %+v, want the first two", page.Entries)
	}
	if page.HasEarlier || !page.HasLater {
		t.Errorf("earlier, later = %v, %v, want false, true", page.HasEarlier, page.HasLater)
	}
	if page.AmountUnit != "gal" || page.TemperatureUnit != "°F" {
		t.Errorf("units = %q, %q, want gal, °F", page.AmountUnit, page.TemperatureUnit)
	}
	if got := *page.Entries[0].AmountLeft; got < 2.64 || got > 2.65 {
		t.Errorf("amount = %v, want ~2.642 gal", got)
	}

	last := strconv.FormatInt(page.Entries[1].Timestamp, 10)
	rec = a.do(http.MethodGet, "/api/kegs/keg-1/log/rows?"+window+"&limit=10&after="+last, nil)
	a.decode(rec, &page)
	if len(page.Entries) != 3 || !page.HasEarlier || page.HasLater {
		t.Errorf("after page: %d entries, earlier %v, later %v", len(page.Entries), page.HasEarlier, page.HasLater)
	}

	for _, bad := range []string{"&limit=0", "&after=x", "&after=5&before=9"} {
		assertStatus(t, a.do(http.MethodGet, "/api/kegs/keg-1/log/rows?"+window+bad, nil), http.StatusBadRequest)
	}
	assertStatus(t, a.do(http.MethodGet, "/api/kegs/nope/log/rows", nil), http.StatusNotFound)
}

// Edits arrive in the display units and are stored in the device's; a value
// the request leaves out is not touched.
func TestUpdateLogConvertsToDeviceUnits(t *testing.T) {
	a := newTestAPI(t)
	k := metricVolumeKeg(a, "keg-1")
	base, _ := seedEditLog(t, a, k, 2)
	setUS(t, a)

	rec := a.do(http.MethodPost, "/api/kegs/keg-1/log/update", map[string]any{
		"entries": []map[string]any{{
			"timestamp":       base.Unix(),
			"amount_left":     5.0,  // gal
			"keg_temperature": 50.0, // °F
			"is_pouring":      nil,
		}, {
			// Only the temperature: the amount must not take a lossy trip
			// through gallons and back.
			"timestamp":       base.Unix() + 60,
			"keg_temperature": 41.0,
		}},
	})
	assertStatus(t, rec, http.StatusOK)

	entries, err := a.store.ReadLog("keg-1", base, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReadLog: %v", err)
	}
	e := entries[0]
	if got := *e.AmountLeft; math.Abs(got-18.92705) > 1e-4 {
		t.Errorf("stored amount = %v, want 18.927 litres", got)
	}
	if got := *e.KegTemperature; math.Abs(got-10) > 1e-9 {
		t.Errorf("stored temperature = %v, want 10 °C", got)
	}
	if e.IsPouring != nil {
		t.Errorf("is_pouring = %v, want cleared", *e.IsPouring)
	}
	if got := *entries[1].AmountLeft; got != 10 {
		t.Errorf("untouched amount = %v, want exactly 10", got)
	}
	if got := *entries[1].KegTemperature; math.Abs(got-5) > 1e-9 {
		t.Errorf("stored temperature = %v, want 5 °C", got)
	}
}

func TestUpdateLogRejectsBadEdits(t *testing.T) {
	a := newTestAPI(t)
	k := metricVolumeKeg(a, "keg-1")
	base, _ := seedEditLog(t, a, k, 1)
	ts := base.Unix()

	for name, body := range map[string]any{
		"empty":         map[string]any{"entries": []any{}},
		"no timestamp":  map[string]any{"entries": []any{map[string]any{"amount_left": 1}}},
		"timestamp":     map[string]any{"entries": []any{map[string]any{"timestamp": ts, "ts": 1}}},
		"keg id":        map[string]any{"entries": []any{map[string]any{"timestamp": ts, "keg_id": "x"}}},
		"not a number":  map[string]any{"entries": []any{map[string]any{"timestamp": ts, "amount_left": "1"}}},
		"not a boolean": map[string]any{"entries": []any{map[string]any{"timestamp": ts, "is_pouring": 1}}},
	} {
		rec := a.do(http.MethodPost, "/api/kegs/keg-1/log/update", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
	// JSON cannot carry NaN or infinity; an overflowing number is refused.
	rec := a.do(http.MethodPost, "/api/kegs/keg-1/log/update", nil)
	assertStatus(t, rec, http.StatusBadRequest)
	req := `{"entries":[{"timestamp":` + strconv.FormatInt(ts, 10) + `,"amount_left":1e999}]}`
	rec = a.doRaw(http.MethodPost, "/api/kegs/keg-1/log/update", req)
	assertStatus(t, rec, http.StatusBadRequest)

	rec = a.do(http.MethodPost, "/api/kegs/keg-1/log/update",
		map[string]any{"entries": []any{map[string]any{"timestamp": ts + 7, "amount_left": 1}}})
	assertStatus(t, rec, http.StatusConflict)
	rec = a.do(http.MethodPost, "/api/kegs/nope/log/update",
		map[string]any{"entries": []any{map[string]any{"timestamp": ts}}})
	assertStatus(t, rec, http.StatusNotFound)
}

func TestDeleteLogEntriesEndpoint(t *testing.T) {
	a := newTestAPI(t)
	k := metricVolumeKeg(a, "keg-1")
	base, window := seedEditLog(t, a, k, 3)

	rec := a.do(http.MethodPost, "/api/kegs/keg-1/log/delete",
		map[string]any{"timestamps": []int64{base.Unix(), base.Unix() + 60}})
	assertStatus(t, rec, http.StatusOK)
	var resp struct {
		Deleted int `json:"deleted"`
	}
	a.decode(rec, &resp)
	if resp.Deleted != 2 {
		t.Errorf("deleted = %d, want 2", resp.Deleted)
	}
	var page logRowsResponse
	a.decode(a.do(http.MethodGet, "/api/kegs/keg-1/log/rows?"+window, nil), &page)
	if len(page.Entries) != 1 {
		t.Errorf("%d entries left, want 1", len(page.Entries))
	}

	assertStatus(t, a.do(http.MethodPost, "/api/kegs/keg-1/log/delete",
		map[string]any{"timestamps": []int64{}}), http.StatusBadRequest)
	assertStatus(t, a.do(http.MethodPost, "/api/kegs/nope/log/delete",
		map[string]any{"timestamps": []int64{1}}), http.StatusNotFound)
}

// doRaw sends a body exactly as given.
func (a *testAPI) doRaw(method, path, body string) *httptest.ResponseRecorder {
	a.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}
