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

// Paging limits for the history editor, which lists stored rows unaveraged.
const (
	defaultLogPageSize = 100
	maxLogPageSize     = 500
)

// handleKegLogRows serves one page of a keg's stored readings for the history
// editor, in the display units, with the unit labels the page heads its
// columns with. ?after= or ?before= moves the page along the window.
func (s *Server) handleKegLogRows(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	keg, err := s.store.GetKeg(id)
	if err != nil {
		writeStoreError(w, err, "keg")
		return
	}
	from, to, ok := historyWindow(w, r, "24h")
	if !ok {
		return
	}

	q := r.URL.Query()
	var after, before int64
	limit := defaultLogPageSize
	for name, dst := range map[string]*int64{"after": &after, "before": &before} {
		if v := q.Get(name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 {
				writeError(w, http.StatusBadRequest, "invalid_value", name+" must be unix seconds")
				return
			}
			*dst = n
		}
	}
	if after > 0 && before > 0 {
		writeError(w, http.StatusBadRequest, "invalid_value", "give after or before, not both")
		return
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_value", "limit must be a positive whole number")
			return
		}
		limit = min(n, maxLogPageSize)
	}

	page, err := s.store.ReadLogPage(id, from, to, after, before, limit)
	if err != nil {
		writeStoreError(w, err, "keg history")
		return
	}
	units := s.displayUnits()
	store.ConvertLogEntries(page.Entries, keg, units)
	keg.SetDisplay(units)
	writeJSON(w, http.StatusOK, map[string]any{
		"entries":          page.Entries,
		"has_earlier":      page.HasEarlier,
		"has_later":        page.HasLater,
		"amount_unit":      keg.Display.AmountUnit,
		"temperature_unit": keg.Display.TemperatureUnit,
	})
}

// handleUpdateKegLog applies the history editor's changes. Each entry names a
// stored reading by its timestamp and carries only the values that changed:
// an absent value is left as stored and a null one is cleared. Amounts and
// temperatures arrive in the display units and are stored in the device's.
func (s *Server) handleUpdateKegLog(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	keg, err := s.store.GetKeg(id)
	if err != nil {
		writeStoreError(w, err, "keg")
		return
	}
	var body struct {
		Entries []map[string]json.RawMessage `json:"entries"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.Entries) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_value", "entries must list at least one reading")
		return
	}

	edits := make([]store.LogEdit, len(body.Entries))
	for i, fields := range body.Entries {
		edit, err := parseLogEdit(fields)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_value", err.Error())
			return
		}
		edits[i] = edit
	}
	store.ConvertLogEditsToDevice(edits, keg, s.displayUnits())

	if err := s.store.UpdateLogEntries(id, edits); err != nil {
		if errors.Is(err, store.ErrLogEntryMissing) {
			writeError(w, http.StatusConflict, "conflict",
				"some readings no longer exist; reload and try again")
			return
		}
		writeStoreError(w, err, "keg history")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "updated": len(edits)})
}

// parseLogEdit reads one entry of an update, telling an absent value from a
// null one.
func parseLogEdit(fields map[string]json.RawMessage) (store.LogEdit, error) {
	var e store.LogEdit
	raw, ok := fields["timestamp"]
	if !ok || json.Unmarshal(raw, &e.Timestamp) != nil || e.Timestamp <= 0 {
		return e, errors.New("each entry needs the timestamp of the reading it changes")
	}
	for name, raw := range fields {
		var err error
		switch name {
		case "timestamp":
		case "amount_left":
			err = parseLogFloat(raw, &e.AmountLeft)
		case "keg_temperature":
			err = parseLogFloat(raw, &e.KegTemperature)
		case "percent_of_beer_left":
			err = parseLogFloat(raw, &e.PercentOfBeerLeft)
		case "is_pouring":
			e.IsPouring.Set = true
			err = json.Unmarshal(raw, &e.IsPouring.Value)
		default:
			return e, fmt.Errorf("%s cannot be edited", name)
		}
		if err != nil {
			return e, fmt.Errorf("%s has an invalid value", name)
		}
	}
	return e, nil
}

func parseLogFloat(raw json.RawMessage, dst *store.LogValue[float64]) error {
	dst.Set = true
	// JSON has no NaN or infinity, so any number that parses is finite.
	return json.Unmarshal(raw, &dst.Value)
}

// handleDeleteKegLogEntries deletes the history editor's selected readings.
// The keg's pours are kept.
func (s *Server) handleDeleteKegLogEntries(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.GetKeg(id); err != nil {
		writeStoreError(w, err, "keg")
		return
	}
	var body struct {
		Timestamps []int64 `json:"timestamps"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.Timestamps) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_value", "timestamps must list at least one reading")
		return
	}
	removed, err := s.store.DeleteLogEntries(id, body.Timestamps)
	if err != nil {
		writeStoreError(w, err, "keg history")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "deleted": removed})
}
