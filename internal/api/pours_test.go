package api

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/matt-freed/open-plaato-keg/internal/store"
)

// pourFrom plays one 0.5 litre pour on a metric-volume keg holding 10 litres.
func pourFrom(a *testAPI, id string) {
	metricVolumeKeg(a, id)
	a.storeKeg(id, "vw\x0049\x00255")
	a.storeKeg(id, "vw\x0051\x009.500")
	a.storeKeg(id, "vw\x0049\x000")
}

func TestKegPoursAndAllPours(t *testing.T) {
	a := newTestAPI(t)
	pourFrom(a, "keg-1")
	pourFrom(a, "keg-2")

	rec := a.do(http.MethodGet, "/api/kegs/keg-1/pours?range=1h", nil)
	assertStatus(t, rec, http.StatusOK)
	var pours []store.Pour
	a.decode(rec, &pours)
	if len(pours) != 1 || pours[0].KegID != "keg-1" {
		t.Fatalf("keg pours = %+v, want keg-1's one pour", pours)
	}
	if d := pours[0].Display; d == nil || d.Unit != "ml" || d.Amount < 499.9 || d.Amount > 500.1 {
		t.Errorf("display = %+v, want 500 ml", d)
	}

	rec = a.do(http.MethodGet, "/api/pours?range=all", nil)
	assertStatus(t, rec, http.StatusOK)
	a.decode(rec, &pours)
	if len(pours) != 2 {
		t.Errorf("all pours = %d, want 2", len(pours))
	}
}

func TestKegPoursForUnknownKegIs404(t *testing.T) {
	a := newTestAPI(t)
	assertStatus(t, a.do(http.MethodGet, "/api/kegs/nope/pours", nil), http.StatusNotFound)
}

func TestPoursHonourDisplayUnits(t *testing.T) {
	a := newTestAPI(t)
	pourFrom(a, "keg-1")
	setUS(t, a)

	var pours []store.Pour
	a.decode(a.do(http.MethodGet, "/api/pours", nil), &pours)
	if len(pours) != 1 {
		t.Fatalf("got %d pours, want 1", len(pours))
	}
	// 0.5 litre is 16.9 US fl oz; the stored value stays in litres.
	if d := pours[0].Display; d == nil || d.Unit != "oz" || d.Amount < 16.8 || d.Amount > 17.0 {
		t.Errorf("display = %+v, want about 16.9 oz", d)
	}
	if pours[0].Unit != "litre" || pours[0].Amount < 0.499 || pours[0].Amount > 0.501 {
		t.Errorf("stored = %v %s, want 0.5 litre", pours[0].Amount, pours[0].Unit)
	}
}

func TestPoursCSVStaysInDeviceUnits(t *testing.T) {
	a := newTestAPI(t)
	pourFrom(a, "keg-1")
	setUS(t, a)

	rec := a.do(http.MethodGet, "/api/pours/csv?range=all", nil)
	assertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q", ct)
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], ",litre,") {
		t.Errorf("csv = %q, want one row in litres", rec.Body.String())
	}
}

func TestDeletePourOverHTTP(t *testing.T) {
	a := newTestAPI(t)
	pourFrom(a, "keg-1")
	var pours []store.Pour
	a.decode(a.do(http.MethodGet, "/api/pours", nil), &pours)

	path := "/api/pours/" + strconv.FormatInt(pours[0].ID, 10)
	assertStatus(t, a.do(http.MethodDelete, path, nil), http.StatusOK)
	assertStatus(t, a.do(http.MethodDelete, path, nil), http.StatusNotFound)
	assertStatus(t, a.do(http.MethodDelete, "/api/pours/abc", nil), http.StatusNotFound)

	a.decode(a.do(http.MethodGet, "/api/pours", nil), &pours)
	if len(pours) != 0 {
		t.Errorf("got %d pours after deleting, want 0", len(pours))
	}
}

// Clearing a keg's history takes its pours off that keg's history only.
func TestClearHistoryKeepsPoursInTheFullList(t *testing.T) {
	a := newTestAPI(t)
	pourFrom(a, "keg-1")
	assertStatus(t, a.do(http.MethodDelete, "/api/kegs/keg-1/log", nil), http.StatusOK)

	var pours []store.Pour
	a.decode(a.do(http.MethodGet, "/api/kegs/keg-1/pours", nil), &pours)
	if len(pours) != 0 {
		t.Errorf("keg pours = %d after clearing, want 0", len(pours))
	}
	a.decode(a.do(http.MethodGet, "/api/pours", nil), &pours)
	if len(pours) != 1 || !pours[0].HiddenFromKeg {
		t.Errorf("all pours = %+v, want the pour kept and marked hidden", pours)
	}
}

func TestMinPourEndpoint(t *testing.T) {
	a := newTestAPI(t)

	if got := a.getConfig().MinPour; got != store.DefaultMinPour {
		t.Errorf("default = %+v, want %+v", got, store.DefaultMinPour)
	}

	rec := a.patchConfig("min_pour", map[string]any{"value": -5, "unit": "gallons"})
	assertStatus(t, rec, http.StatusOK)
	if got := a.getConfig().MinPour; got != (store.MinPour{Value: 0, Unit: store.MinPourUnitOz}) {
		t.Errorf("junk normalised to %+v, want 0 oz", got)
	}

	assertStatus(t, a.patchConfig("min_pour", map[string]any{"value": 60, "unit": "ml"}), http.StatusOK)
	if cfg := a.getConfig(); cfg.MinPour != (store.MinPour{Value: 60, Unit: store.MinPourUnitMl}) {
		t.Errorf("config MinPour = %+v, want 60 ml", cfg.MinPour)
	}
}

// Both keg handlers carry the newest pour still in the keg's history.
func TestKegsCarryTheLatestPour(t *testing.T) {
	a := newTestAPI(t)
	pourFrom(a, "keg-1")
	metricVolumeKeg(a, "keg-2")

	var kegs []store.Keg
	a.decode(a.do(http.MethodGet, "/api/kegs", nil), &kegs)
	for _, k := range kegs {
		switch k.ID {
		case "keg-1":
			if k.LatestPour == nil || k.LatestPour.Display == nil || k.LatestPour.Display.Unit != "ml" {
				t.Errorf("keg-1 latest_pour = %+v, want its 500 ml pour", k.LatestPour)
			}
		case "keg-2":
			if k.LatestPour != nil {
				t.Errorf("keg-2 latest_pour = %+v, want none", k.LatestPour)
			}
		}
	}

	var k store.Keg
	a.decode(a.do(http.MethodGet, "/api/kegs/keg-1", nil), &k)
	if k.LatestPour == nil {
		t.Fatal("GET /api/kegs/keg-1 has no latest_pour")
	}

	// Clearing the history clears the tile's last pour too.
	assertStatus(t, a.do(http.MethodDelete, "/api/kegs/keg-1/log", nil), http.StatusOK)
	k = store.Keg{}
	a.decode(a.do(http.MethodGet, "/api/kegs/keg-1", nil), &k)
	if k.LatestPour != nil {
		t.Errorf("latest_pour = %+v after clearing, want none", k.LatestPour)
	}
}
