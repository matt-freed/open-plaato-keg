package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/matt-freed/open-plaato-keg/internal/store"
)

// logRanges are the windows the history UI offers.
var logRanges = map[string]time.Duration{
	"1h":  time.Hour,
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
}

// parseRange resolves the ?range= parameter, falling back to def.
func parseRange(r *http.Request, def string) time.Duration {
	name := r.URL.Query().Get("range")
	if d, ok := logRanges[name]; ok {
		return d
	}
	return logRanges[def]
}

func (s *Server) handleKegLog(w http.ResponseWriter, r *http.Request) {
	entries, keg, ok := s.readLog(w, r, "24h")
	if !ok {
		return
	}
	store.ConvertLogEntries(entries, keg, s.displayUnits())
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleKegLogCSV(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	// Deliberately not converted. The export is a record of what was stored
	// and what the device and BarHelper saw, it carries no unit column, and
	// exported files get archived and re-imported — a display preference
	// silently rescaling their contents is exactly the inconsistency this
	// setting exists to avoid.
	entries, _, ok := s.readLog(w, r, "30d")
	if !ok {
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="keg-`+id+`-log.csv"`)
	if err := store.WriteLogCSV(w, entries); err != nil {
		// The response is already in flight, so this can only be logged.
		return
	}
}

// readLog reads one keg's history, returning the keg alongside it so the
// caller can decide whether to present the readings in the display units.
func (s *Server) readLog(w http.ResponseWriter, r *http.Request, defaultRange string) ([]store.LogEntry, *store.Keg, bool) {
	id := chi.URLParam(r, "id")
	keg, err := s.store.GetKeg(id)
	if err != nil {
		writeStoreError(w, err, "keg")
		return nil, nil, false
	}

	to := time.Now()
	from := to.Add(-parseRange(r, defaultRange))

	entries, err := s.store.ReadLog(id, from, to)
	if err != nil {
		writeStoreError(w, err, "keg history")
		return nil, nil, false
	}
	return entries, keg, true
}
