package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/matt-freed/open-plaato-keg/internal/store"
)

// pourRanges are the windows the All Pours page offers. Pours are never
// pruned, so it reaches further back than the minute log; "all" is no lower
// bound at all.
var pourRanges = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
	"90d": 90 * 24 * time.Hour,
	"1y":  365 * 24 * time.Hour,
	"all": 0,
}

// pourWindow resolves the All Pours ?range= parameter, defaulting to 30 days.
// A zero from means all time.
func pourWindow(r *http.Request) (from, to time.Time) {
	to = time.Now()
	d, ok := pourRanges[r.URL.Query().Get("range")]
	if !ok {
		d = pourRanges["30d"]
	}
	if d == 0 {
		return time.Time{}, to
	}
	return to.Add(-d), to
}

// handleKegPours lists the pours in one keg's history for the History page,
// over the same window as its log.
func (s *Server) handleKegPours(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.GetKeg(id); err != nil {
		writeStoreError(w, err, "keg")
		return
	}
	from, to, ok := historyWindow(w, r, "24h")
	if !ok {
		return
	}
	pours, err := s.store.ListKegPours(id, from, to)
	if err != nil {
		writeStoreError(w, err, "pours")
		return
	}
	store.ConvertPours(pours, s.displayUnits())
	writeJSON(w, http.StatusOK, pours)
}

// pourFilter reads All Pours' ?range=, ?beer= and ?keg=. A ?beer= that is
// present but empty selects the pours with no beer on tap.
func pourFilter(r *http.Request) store.PourFilter {
	q := r.URL.Query()
	from, to := pourWindow(r)
	f := store.PourFilter{From: from, To: to, KegID: q.Get("keg")}
	if q.Has("beer") {
		beer := q.Get("beer")
		f.Beer = &beer
	}
	return f
}

// Paging limits for All Pours.
const maxPourPageSize = 500

// handleListPours lists every pour from every keg, including those hidden from
// a keg's history when it was cleared, narrowed by the All Pours filters.
// With ?limit= it returns one page, skipping ?offset=; X-Total-Count always
// carries how many pours the filters match.
func (s *Server) handleListPours(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := 0, 0
	for name, dst := range map[string]*int{"limit": &limit, "offset": &offset} {
		if v := q.Get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "invalid_value", name+" must be a whole number")
				return
			}
			*dst = n
		}
	}
	limit = min(limit, maxPourPageSize)

	f := pourFilter(r)
	units := s.displayUnits()
	pours, err := s.store.ListPoursPage(f, limit, offset)
	if err != nil {
		writeStoreError(w, err, "pours")
		return
	}
	total := len(pours)
	if limit > 0 {
		sum, err := s.store.SummarizePours(f, units)
		if err != nil {
			writeStoreError(w, err, "pours")
			return
		}
		total = sum.Count
	}
	store.ConvertPours(pours, units)
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	writeJSON(w, http.StatusOK, pours)
}

// handleSummarizePours serves All Pours' figures for the filtered pours, in
// the display units, with the beers and scales its filters offer.
func (s *Server) handleSummarizePours(w http.ResponseWriter, r *http.Request) {
	f := pourFilter(r)
	sum, err := s.store.SummarizePours(f, s.displayUnits())
	if err != nil {
		writeStoreError(w, err, "pours")
		return
	}
	beers, scales, err := s.store.PourFilterOptions(f.From, f.To)
	if err != nil {
		writeStoreError(w, err, "pours")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count":   sum.Count,
		"totals":  sum.Totals,
		"by_beer": sum.ByBeer,
		"beers":   beers,
		"scales":  scales,
	})
}

func (s *Server) handleListPoursCSV(w http.ResponseWriter, r *http.Request) {
	// Not converted, for the same reason as the log export: it records what
	// the scales reported, and every row names its own unit. Never paged, but
	// filtered like the page it is downloaded from.
	pours, err := s.store.ListPoursPage(pourFilter(r), 0, 0)
	if err != nil {
		writeStoreError(w, err, "pours")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="pours.csv"`)
	if err := store.WritePoursCSV(w, pours); err != nil {
		// The response is already in flight, so this can only be logged.
		return
	}
}

func (s *Server) handleDeletePour(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "pour does not exist")
		return
	}
	if err := s.store.DeletePour(id); err != nil {
		writeStoreError(w, err, "pour")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// maxPourText caps the text fields a pour edit can set.
const maxPourText = 200

// handleUpdatePours applies All Pours' edits. Each entry names a pour by id
// and carries only the values that changed: an absent value is left as stored
// and a null one is cleared. Amounts arrive in the display units' pour-sized
// sub-unit and are stored in the pour's own unit.
func (s *Server) handleUpdatePours(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pours []map[string]json.RawMessage `json:"pours"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.Pours) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_value", "pours must list at least one pour")
		return
	}
	edits := make([]store.PourEdit, len(body.Pours))
	for i, fields := range body.Pours {
		edit, err := parsePourEdit(fields)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_value", err.Error())
			return
		}
		edits[i] = edit
	}
	if err := s.store.UpdatePours(edits, s.displayUnits()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusConflict, "conflict", "some pours no longer exist; reload and try again")
			return
		}
		writeStoreError(w, err, "pours")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "updated": len(edits)})
}

// parsePourEdit reads and validates one entry of a pour update, telling an
// absent value from a null one.
func parsePourEdit(fields map[string]json.RawMessage) (store.PourEdit, error) {
	var e store.PourEdit
	raw, ok := fields["id"]
	if !ok || json.Unmarshal(raw, &e.ID) != nil || e.ID <= 0 {
		return e, errors.New("each entry needs the id of the pour it changes")
	}
	for name, raw := range fields {
		var err error
		switch name {
		case "id":
		case "amount":
			if err = unmarshalSet(raw, &e.Amount); err == nil && (e.Amount.Value == nil || *e.Amount.Value <= 0) {
				return e, errors.New("amount must be more than zero")
			}
		case "abv":
			if err = unmarshalSet(raw, &e.ABV); err == nil && e.ABV.Value != nil && (*e.ABV.Value < 0 || *e.ABV.Value > 100) {
				return e, errors.New("abv must be between 0 and 100")
			}
		case "tap_number":
			if err = unmarshalSet(raw, &e.TapNumber); err == nil && e.TapNumber.Value != nil && *e.TapNumber.Value < 0 {
				return e, errors.New("tap_number cannot be negative")
			}
		case "beer_name":
			err = unmarshalText(raw, &e.BeerName)
		case "beer_style":
			err = unmarshalText(raw, &e.BeerStyle)
		case "scale_label":
			err = unmarshalText(raw, &e.ScaleLabel)
		default:
			return e, fmt.Errorf("%s cannot be edited", name)
		}
		if err != nil {
			return e, fmt.Errorf("%s has an invalid value", name)
		}
	}
	return e, nil
}

// unmarshalSet marks dst as changed and reads its value. JSON has no NaN or
// infinity, and a fractional number does not unmarshal into an int, so any
// number that parses is finite and of the right kind.
func unmarshalSet[T any](raw json.RawMessage, dst *store.LogValue[T]) error {
	dst.Set = true
	return json.Unmarshal(raw, &dst.Value)
}

func unmarshalText(raw json.RawMessage, dst *store.LogValue[string]) error {
	if err := unmarshalSet(raw, dst); err != nil {
		return err
	}
	if dst.Value != nil && len([]rune(*dst.Value)) > maxPourText {
		return fmt.Errorf("longer than %d characters", maxPourText)
	}
	return nil
}

// handleDeletePours deletes the pours All Pours has selected.
func (s *Server) handleDeletePours(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_value", "ids must list at least one pour")
		return
	}
	removed, err := s.store.DeletePours(body.IDs)
	if err != nil {
		writeStoreError(w, err, "pours")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "deleted": removed})
}
