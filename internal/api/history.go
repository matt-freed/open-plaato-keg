package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/matt-freed/open-plaato-keg/internal/store"
)

// logRanges are the preset windows the history UI offers. A custom window
// is given as ?from=&to= instead.
var logRanges = map[string]time.Duration{
	"1h":   time.Hour,
	"6h":   6 * time.Hour,
	"24h":  24 * time.Hour,
	"7d":   7 * 24 * time.Hour,
	"30d":  30 * 24 * time.Hour,
	"90d":  90 * 24 * time.Hour,
	"180d": 180 * 24 * time.Hour,
	"1y":   365 * 24 * time.Hour,
}

// parseRange resolves the ?range= parameter to its window, falling back to
// def.
func parseRange(r *http.Request, def string) time.Duration {
	if d, ok := logRanges[r.URL.Query().Get("range")]; ok {
		return d
	}
	return logRanges[def]
}

// historyWindow resolves the times a history request covers: ?from=&to= in
// unix seconds when both are given, otherwise the preset ?range= ending now.
// A custom window that is malformed, or ends before it starts, is answered
// with a 400 and reported as not ok.
func historyWindow(w http.ResponseWriter, r *http.Request, defaultRange string) (from, to time.Time, ok bool) {
	q := r.URL.Query()
	if q.Get("from") == "" && q.Get("to") == "" {
		to = time.Now()
		return to.Add(-parseRange(r, defaultRange)), to, true
	}
	f, errFrom := strconv.ParseInt(q.Get("from"), 10, 64)
	t, errTo := strconv.ParseInt(q.Get("to"), 10, 64)
	if errFrom != nil || errTo != nil {
		writeError(w, http.StatusBadRequest, "invalid_value", "from and to must both be unix seconds")
		return time.Time{}, time.Time{}, false
	}
	if t <= f {
		writeError(w, http.StatusBadRequest, "invalid_value", "to must be after from")
		return time.Time{}, time.Time{}, false
	}
	return time.Unix(f, 0), time.Unix(t, 0), true
}

// maxChartPoints caps how many readings a history chart is sent.
const maxChartPoints = 1440

// sampleSteps are the steps a window can be averaged to, finest first. Past
// an hour each is a whole number of hours, so a point never straddles part
// of an hourly row left by store.CompactLog.
var sampleSteps = []time.Duration{
	10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 3 * time.Hour, 4 * time.Hour,
	6 * time.Hour, 8 * time.Hour, 12 * time.Hour, 24 * time.Hour,
}

// sampleStep is the smallest step that keeps a window to maxChartPoints, or
// zero when every reading fits. Readings are logged at most once a minute,
// so a day or less is never averaged. Windows of more than four years use
// whole days.
func sampleStep(window time.Duration) time.Duration {
	need := window / maxChartPoints
	if need <= store.LogInterval {
		return 0
	}
	for _, step := range sampleSteps {
		if step >= need {
			return step
		}
	}
	day := 24 * time.Hour
	return (need + day - 1) / day * day
}

// handleKegLog serves a keg's history for the chart, averaged to the
// window's step. X-Log-Step-Seconds carries that step, zero when every reading is
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
// When sampled, the readings are averaged to the window's step, which is also
// reported in the X-Log-Step-Seconds header.
func (s *Server) readLog(w http.ResponseWriter, r *http.Request, defaultRange string, sampled bool) ([]store.LogEntry, *store.Keg, bool) {
	id := chi.URLParam(r, "id")
	keg, err := s.store.GetKeg(id)
	if err != nil {
		writeStoreError(w, err, "keg")
		return nil, nil, false
	}

	from, to, ok := historyWindow(w, r, defaultRange)
	if !ok {
		return nil, nil, false
	}

	var step time.Duration
	if sampled {
		step = sampleStep(to.Sub(from))
		w.Header().Set("X-Log-Step-Seconds", strconv.FormatInt(int64(step/time.Second), 10))
	}
	entries, err := s.store.ReadLogSampled(id, from, to, step)
	if err != nil {
		writeStoreError(w, err, "keg history")
		return nil, nil, false
	}
	return entries, keg, true
}
