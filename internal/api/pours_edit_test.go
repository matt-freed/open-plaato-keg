package api

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/matt-freed/open-plaato-keg/internal/store"
)

type poursSummary struct {
	Count  int               `json:"count"`
	Totals []store.PourTotal `json:"totals"`
	ByBeer []store.BeerPours `json:"by_beer"`
	Beers  []string          `json:"beers"`
	Scales []store.PourScale `json:"scales"`
}

// threePours records one 0.5 litre pour on each of three metric scales and
// names the first one's beer.
func threePours(t *testing.T, a *testAPI) []store.Pour {
	t.Helper()
	for _, id := range []string{"keg-1", "keg-2", "keg-3"} {
		pourFrom(a, id)
	}
	var pours []store.Pour
	a.decode(a.do(http.MethodGet, "/api/pours?range=all", nil), &pours)
	if len(pours) != 3 {
		t.Fatalf("got %d pours, want 3", len(pours))
	}
	rec := a.do(http.MethodPatch, "/api/pours", map[string]any{
		"pours": []map[string]any{{"id": pours[2].ID, "beer_name": "Amber"}},
	})
	assertStatus(t, rec, http.StatusOK)
	return pours
}

func TestAllPoursArePagedAndFiltered(t *testing.T) {
	a := newTestAPI(t)
	threePours(t, a)

	rec := a.do(http.MethodGet, "/api/pours?range=all&limit=2&offset=0", nil)
	assertStatus(t, rec, http.StatusOK)
	var page []store.Pour
	a.decode(rec, &page)
	if len(page) != 2 || rec.Header().Get("X-Total-Count") != "3" {
		t.Errorf("page = %d pours of %s, want 2 of 3", len(page), rec.Header().Get("X-Total-Count"))
	}
	a.decode(a.do(http.MethodGet, "/api/pours?range=all&limit=2&offset=2", nil), &page)
	if len(page) != 1 {
		t.Errorf("second page = %d pours, want 1", len(page))
	}

	for query, want := range map[string]int{"&beer=Amber": 1, "&beer=": 2, "&keg=keg-2": 1, "&keg=nope": 0} {
		rec := a.do(http.MethodGet, "/api/pours?range=all&limit=10"+query, nil)
		a.decode(rec, &page)
		if len(page) != want || rec.Header().Get("X-Total-Count") != strconv.Itoa(want) {
			t.Errorf("%s: %d pours (total %s), want %d", query, len(page), rec.Header().Get("X-Total-Count"), want)
		}
	}
	assertStatus(t, a.do(http.MethodGet, "/api/pours?limit=-1", nil), http.StatusBadRequest)

	rec = a.do(http.MethodGet, "/api/pours/csv?range=all&beer=Amber", nil)
	if lines := strings.Count(strings.TrimSpace(rec.Body.String()), "\n"); lines != 1 {
		t.Errorf("filtered csv has %d rows, want 1:\n%s", lines, rec.Body.String())
	}
}

func TestPoursSummaryInDisplayUnits(t *testing.T) {
	a := newTestAPI(t)
	threePours(t, a)
	setUS(t, a)

	var sum poursSummary
	a.decode(a.do(http.MethodGet, "/api/pours/summary?range=all", nil), &sum)
	if sum.Count != 3 || len(sum.Totals) != 1 || sum.Totals[0].Unit != "oz" {
		t.Fatalf("summary = %+v, want 3 pours totalled in oz", sum)
	}
	if got, want := sum.Totals[0].Amount, 1.5/3.78541*128; math.Abs(got-want) > 0.01 {
		t.Errorf("total = %v oz, want %v", got, want)
	}
	if len(sum.Beers) != 2 || len(sum.Scales) != 3 {
		t.Errorf("filter options = %q / %+v", sum.Beers, sum.Scales)
	}

	// The options ignore the filters; the figures do not.
	a.decode(a.do(http.MethodGet, "/api/pours/summary?range=all&beer=Amber", nil), &sum)
	if sum.Count != 1 || len(sum.Beers) != 2 || len(sum.ByBeer) != 1 {
		t.Errorf("filtered summary = %+v", sum)
	}
}

// An amount edited in oz is stored in the pour's own litres.
func TestUpdatePoursConvertsTheAmount(t *testing.T) {
	a := newTestAPI(t)
	pours := threePours(t, a)
	setUS(t, a)

	rec := a.do(http.MethodPatch, "/api/pours", map[string]any{
		"pours": []map[string]any{{"id": pours[0].ID, "amount": 16, "abv": 6.2, "tap_number": 3}},
	})
	assertStatus(t, rec, http.StatusOK)

	var csvPours []store.Pour
	a.decode(a.do(http.MethodGet, "/api/pours?range=all&keg="+pours[0].KegID, nil), &csvPours)
	p := csvPours[0]
	if math.Abs(p.Amount-16.0/128*3.78541) > 1e-9 || p.Unit != "litre" {
		t.Errorf("stored amount = %v %s, want 16 oz in litres", p.Amount, p.Unit)
	}
	if p.ABV == nil || *p.ABV != 6.2 || p.TapNumber == nil || *p.TapNumber != 3 {
		t.Errorf("abv, tap = %v, %v", p.ABV, p.TapNumber)
	}
	if p.Display == nil || math.Abs(p.Display.Amount-16) > 1e-9 {
		t.Errorf("display = %+v, want 16 oz back", p.Display)
	}
}

func TestUpdatePoursRejectsBadEdits(t *testing.T) {
	a := newTestAPI(t)
	id := threePours(t, a)[0].ID

	for name, entry := range map[string]map[string]any{
		"no id":          {"amount": 1},
		"zero amount":    {"id": id, "amount": 0},
		"null amount":    {"id": id, "amount": nil},
		"abv over 100":   {"id": id, "abv": 101},
		"fractional tap": {"id": id, "tap_number": 1.5},
		"negative tap":   {"id": id, "tap_number": -1},
		"keg id":         {"id": id, "keg_id": "keg-9"},
		"start time":     {"id": id, "started_at": 1},
		"unit":           {"id": id, "unit": "gal"},
		"long text":      {"id": id, "beer_name": strings.Repeat("x", 201)},
		"number name":    {"id": id, "beer_name": 5},
	} {
		rec := a.do(http.MethodPatch, "/api/pours", map[string]any{"pours": []any{entry}})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
	assertStatus(t, a.do(http.MethodPatch, "/api/pours", map[string]any{"pours": []any{}}), http.StatusBadRequest)
	assertStatus(t, a.do(http.MethodPatch, "/api/pours",
		map[string]any{"pours": []any{map[string]any{"id": 9999, "beer_name": "x"}}}), http.StatusConflict)
}

func TestDeletePoursInBulk(t *testing.T) {
	a := newTestAPI(t)
	pours := threePours(t, a)

	rec := a.do(http.MethodDelete, "/api/pours", map[string]any{"ids": []int64{pours[0].ID, pours[1].ID}})
	assertStatus(t, rec, http.StatusOK)
	var resp struct {
		Deleted int `json:"deleted"`
	}
	a.decode(rec, &resp)
	var left []store.Pour
	a.decode(a.do(http.MethodGet, "/api/pours?range=all", nil), &left)
	if resp.Deleted != 2 || len(left) != 1 {
		t.Errorf("deleted %d, %d left; want 2 and 1", resp.Deleted, len(left))
	}
	assertStatus(t, a.do(http.MethodDelete, "/api/pours", map[string]any{"ids": []int64{}}), http.StatusBadRequest)
}
