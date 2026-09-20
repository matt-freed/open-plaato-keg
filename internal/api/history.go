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
	entries, ok := s.readLog(w, r, "24h")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleKegLogCSV(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	entries, ok := s.readLog(w, r, "30d")
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

func (s *Server) readLog(w http.ResponseWriter, r *http.Request, defaultRange string) ([]store.LogEntry, bool) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.GetKeg(id); err != nil {
		writeStoreError(w, err, "keg")
		return nil, false
	}

	to := time.Now()
	from := to.Add(-parseRange(r, defaultRange))

	entries, err := s.store.ReadLog(id, from, to)
	if err != nil {
		writeStoreError(w, err, "keg history")
		return nil, false
	}
	return entries, true
}
