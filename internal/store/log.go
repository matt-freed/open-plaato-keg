package store

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"
)

// LogEntry is one recorded keg reading.
type LogEntry struct {
	Timestamp         int64    `json:"timestamp"`
	AmountLeft        *float64 `json:"amount_left"`
	KegTemperature    *float64 `json:"keg_temperature"`
	PercentOfBeerLeft *float64 `json:"percent_of_beer_left"`
	IsPouring         *bool    `json:"is_pouring"`
}

// LogInterval is the minimum gap between recorded readings for one keg. A keg
// reports continuously, so without throttling the history table would grow by
// thousands of near-identical rows an hour.
const LogInterval = time.Minute

// LogRetention is how long history is kept before Prune discards it.
const LogRetention = 90 * 24 * time.Hour

// LogThrottle tracks when each keg was last recorded.
type LogThrottle struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// NewLogThrottle returns a throttle that admits one reading per keg per
// LogInterval.
func NewLogThrottle() *LogThrottle {
	return &LogThrottle{last: map[string]time.Time{}}
}

// Allow reports whether a reading for id should be recorded now, and records
// the decision.
func (t *LogThrottle) Allow(id string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if last, ok := t.last[id]; ok && now.Sub(last) < LogInterval {
		return false
	}
	t.last[id] = now
	return true
}

// HasLoggableReading reports whether the keg carries any of the values the
// history records.
//
// A keg that has only announced itself and sent its firmware version has
// nothing worth a row, and recording one would put an empty point at the start
// of every chart and a blank line in every CSV export.
func (k *Keg) HasLoggableReading() bool {
	return k.AmountLeft != nil || k.KegTemperature != nil ||
		k.PercentOfBeerLeft != nil || k.IsPouring != nil
}

// AppendLog records the keg's current readings at ts.
func (s *Store) AppendLog(k *Keg, ts time.Time) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO keg_log
		 (keg_id, ts, amount_left, keg_temperature, percent_of_beer_left, is_pouring)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		k.ID, ts.Unix(), k.AmountLeft, k.KegTemperature, k.PercentOfBeerLeft, k.IsPouring)
	return err
}

// ReadLog returns the readings for a keg between two times, oldest first.
func (s *Store) ReadLog(id string, from, to time.Time) ([]LogEntry, error) {
	rows, err := s.db.Query(
		`SELECT ts, amount_left, keg_temperature, percent_of_beer_left, is_pouring
		 FROM keg_log WHERE keg_id = ? AND ts >= ? AND ts <= ? ORDER BY ts`,
		id, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []LogEntry{}
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.Timestamp, &e.AmountLeft, &e.KegTemperature,
			&e.PercentOfBeerLeft, &e.IsPouring); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// PruneLog deletes readings older than LogRetention and returns how many rows
// were removed.
func (s *Store) PruneLog(now time.Time) (int64, error) {
	res, err := s.db.Exec("DELETE FROM keg_log WHERE ts < ?", now.Add(-LogRetention).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// WriteLogCSV writes entries as CSV, matching the column order of the JSON
// history endpoint.
func WriteLogCSV(w io.Writer, entries []LogEntry) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{
		"timestamp", "amount_left", "keg_temperature", "percent_of_beer_left", "is_pouring",
	}); err != nil {
		return err
	}
	for _, e := range entries {
		record := []string{
			time.Unix(e.Timestamp, 0).UTC().Format(time.RFC3339),
			formatFloat(e.AmountLeft),
			formatFloat(e.KegTemperature),
			formatFloat(e.PercentOfBeerLeft),
			formatBool(e.IsPouring),
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func formatFloat(f *float64) string {
	if f == nil {
		return ""
	}
	return strconv.FormatFloat(*f, 'f', -1, 64)
}

func formatBool(b *bool) string {
	if b == nil {
		return ""
	}
	return fmt.Sprintf("%t", *b)
}
