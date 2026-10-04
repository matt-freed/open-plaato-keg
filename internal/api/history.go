package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/matt-freed/open-plaato-keg/internal/store"
)

// logRange is one window the history UI offers, and the step its chart is
// averaged to.
type logRange struct {
	window time.Duration
	// step is zero for ranges short enough to chart every reading. Longer
	// ranges are averaged so no chart exceeds 1,440 points. Past 30 days the
	// step is a whole number of hours, so each point averages the same number
	// of the hourly rows that store.CompactLog leaves behind.
	step time.Duration
}

var logRanges = map[string]logRange{
	"1h":   {window: time.Hour},
	"6h":   {window: 6 * time.Hour},
	"24h":  {window: 24 * time.Hour},
	"7d":   {window: 7 * 24 * time.Hour, step: 10 * time.Minute},
	"30d":  {window: 30 * 24 * time.Hour, step: 30 * time.Minute},
	"90d":  {window: 90 * 24 * time.Hour, step: 2 * time.Hour},
	"180d": {window: 180 * 24 * time.Hour, step: 3 * time.Hour},
	"1y":   {window: 365 * 24 * time.Hour, step: 8 * time.Hour},
}

// lookupRange resolves the ?range= parameter, falling back to def.
func lookupRange(r *http.Request, def string) logRange {
	if lr, ok := logRanges[r.URL.Query().Get("range")]; ok {
		return lr
	}
	return logRanges[def]
}

// parseRange resolves the ?range= parameter to its window, falling back to
// def.
func parseRange(r *http.Request, def string) time.Duration {
	return lookupRange(r, def).window
}

// handleKegLog serves a keg's history for the chart, averaged to the range's
// step. X-Log-Step-Seconds carries that step, zero when every reading is
// sent, so the page can tell a gap in the data from the spacing of its points.
func (s *Server) handleKegLog(w http.ResponseWriter, r *http.Request) {
	entries, keg, ok := s.readLog(w, r, "24h", true)
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
	//
	// Not averaged either: the export is every row stored for the range. Rows
	// older than the compaction cutoff are already hourly averages.
	entries, _, ok := s.readLog(w, r, "30d", false)
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

// handleClearKegLog deletes every reading recorded for a keg, across all
// ranges. The keg is kept and starts logging afresh with its next report.
func (s *Server) handleClearKegLog(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.GetKeg(id); err != nil {
		writeStoreError(w, err, "keg")
		return
	}
	removed, err := s.store.ClearLog(id)
	if err != nil {
		writeStoreError(w, err, "keg history")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "deleted": removed})
}

// readLog reads one keg's history, returning the keg alongside it so the
// caller can decide whether to present the readings in the display units.
// When sampled, the readings are averaged to the range's step, which is also
// reported in the X-Log-Step-Seconds header.
func (s *Server) readLog(w http.ResponseWriter, r *http.Request, defaultRange string, sampled bool) ([]store.LogEntry, *store.Keg, bool) {
	id := chi.URLParam(r, "id")
	keg, err := s.store.GetKeg(id)
	if err != nil {
		writeStoreError(w, err, "keg")
		return nil, nil, false
	}

	lr := lookupRange(r, defaultRange)
	to := time.Now()
	from := to.Add(-lr.window)

	var step time.Duration
	if sampled {
		step = lr.step
		w.Header().Set("X-Log-Step-Seconds", strconv.FormatInt(int64(step/time.Second), 10))
	}
	entries, err := s.store.ReadLogSampled(id, from, to, step)
	if err != nil {
		writeStoreError(w, err, "keg history")
		return nil, nil, false
	}
	return entries, keg, true
}
