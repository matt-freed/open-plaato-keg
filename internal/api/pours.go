package api

import (
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

// handleListPours lists every pour from every keg, including those hidden from
// a keg's history when it was cleared.
func (s *Server) handleListPours(w http.ResponseWriter, r *http.Request) {
	pours, err := s.store.ListPours(pourWindow(r))
	if err != nil {
		writeStoreError(w, err, "pours")
		return
	}
	store.ConvertPours(pours, s.displayUnits())
	writeJSON(w, http.StatusOK, pours)
}

func (s *Server) handleListPoursCSV(w http.ResponseWriter, r *http.Request) {
	// Not converted, for the same reason as the log export: it records what
	// the scales reported, and every row names its own unit.
	pours, err := s.store.ListPours(pourWindow(r))
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
