package store

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/matt-freed/open-plaato-keg/internal/units"
)

// LogEntry is one recorded keg reading.
type LogEntry struct {
	Timestamp         int64    `json:"timestamp"`
	AmountLeft        *float64 `json:"amount_left"`
	KegTemperature    *float64 `json:"keg_temperature"`
	PercentOfBeerLeft *float64 `json:"percent_of_beer_left"`
	IsPouring         *bool    `json:"is_pouring"`
}

// ConvertLogEntries rewrites the readings in place into the display units.
//
// Logged rows record no unit of their own, so they are reinterpreted using the
// keg's current device unit. A scale switched between litres and gallons
// part-way through a keg already leaves a discontinuity in its stored data;
// this neither worsens nor repairs it. Only the two readings that carry a unit
// are touched, so percent and the pouring flag are left alone.
func ConvertLogEntries(entries []LogEntry, k *Keg, u DisplayUnits) {
	if k == nil || u.FollowsDevice() {
		return
	}
	c := k.resolveDisplay(u)

	for i := range entries {
		if v := entries[i].AmountLeft; v != nil {
			converted := units.ConvertAmount(*v, c.fromUnit, c.toUnit)
			entries[i].AmountLeft = &converted
		}
		if v := entries[i].KegTemperature; v != nil {
			converted := units.ConvertTemp(*v, c.fromF, c.toF)
			entries[i].KegTemperature = &converted
		}
	}
}

// LogInterval is the minimum gap between recorded readings for one keg. A keg
// reports continuously, so without throttling the history table would grow by
// thousands of near-identical rows an hour.
const LogInterval = time.Minute

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

// ReadLogSampled returns a keg's readings between two times, oldest first,
// averaged into one entry per step so a long range stays a chartable size. A
// step of zero or less returns every reading, as ReadLog does.
//
// Each entry is timestamped at the average time of the readings it covers,
// which keeps it where the data actually is and never in the future. A value
// no reading in the step reported stays nil, and the entry counts as pouring
// if any reading in the step was.
func (s *Store) ReadLogSampled(id string, from, to time.Time, step time.Duration) ([]LogEntry, error) {
	secs := int64(step / time.Second)
	if secs <= 0 {
		return s.ReadLog(id, from, to)
	}
	rows, err := s.db.Query(
		`SELECT CAST(AVG(ts) AS INTEGER), AVG(amount_left), AVG(keg_temperature),
		        AVG(percent_of_beer_left), MAX(is_pouring)
		 FROM keg_log WHERE keg_id = ? AND ts >= ? AND ts <= ?
		 GROUP BY ts / ? ORDER BY 1`,
		id, from.Unix(), to.Unix(), secs)
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

// ClearLog deletes every reading recorded for one keg and returns how many
// rows were removed. The keg itself, and every other keg's history, is kept.
//
// The keg's pours are hidden from its history rather than deleted: they are
// the long-term record and stay in the list of all pours.
func (s *Store) ClearLog(id string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec("DELETE FROM keg_log WHERE keg_id = ?", id)
	if err != nil {
		return 0, err
	}
	removed, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec("UPDATE pours SET hidden_from_keg = 1 WHERE keg_id = ?", id); err != nil {
		return 0, err
	}
	return removed, tx.Commit()
}

// PruneLog deletes readings older than retention and returns how many rows
// were removed. A retention of zero keeps everything. Pours are never pruned.
func (s *Store) PruneLog(now time.Time, retention time.Duration) (int64, error) {
	if retention <= 0 {
		return 0, nil
	}
	res, err := s.db.Exec("DELETE FROM keg_log WHERE ts < ?", now.Add(-retention).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CompactStep is the resolution history is reduced to once it is older than
// the compaction cutoff.
const CompactStep = time.Hour

// compactChunk is how much history one compaction transaction covers, so the
// first pass over a long history does not hold the write lock for long.
const compactChunk = 24 * time.Hour

// CompactLog replaces readings older than compactAfter with one averaged row
// per keg per CompactStep, timestamped at the start of the step, and returns
// how many rows that removed. A compactAfter of zero keeps every reading.
//
// Averages follow ReadLogSampled: a value no reading reported stays NULL, and
// the row is pouring if any reading was. Steps already reduced to one aligned
// row are left alone, so running it again changes nothing. Pours are recorded
// separately and are unaffected.
func (s *Store) CompactLog(now time.Time, compactAfter time.Duration) (int64, error) {
	if compactAfter <= 0 {
		return 0, nil
	}
	cutoff := now.Add(-compactAfter).Truncate(CompactStep).Unix()

	ids, err := s.logKegIDs()
	if err != nil {
		return 0, err
	}

	var removed int64
	for _, id := range ids {
		var oldest sql.NullInt64
		if err := s.db.QueryRow(
			"SELECT MIN(ts) FROM keg_log WHERE keg_id = ? AND ts < ?", id, cutoff,
		).Scan(&oldest); err != nil {
			return removed, err
		}
		if !oldest.Valid {
			continue
		}
		step := int64(CompactStep / time.Second)
		chunk := int64(compactChunk / time.Second)
		for start := oldest.Int64 / step * step; start < cutoff; start += chunk {
			n, err := s.compactRange(id, start, min(start+chunk, cutoff))
			if err != nil {
				return removed, err
			}
			removed += n
		}
	}
	return removed, nil
}

// logKegIDs lists every keg with recorded history.
func (s *Store) logKegIDs() ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT keg_id FROM keg_log")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// compactRange compacts one keg's readings in [from, to), both of which fall
// on a CompactStep boundary, in a single transaction.
func (s *Store) compactRange(id string, from, to int64) (int64, error) {
	step := int64(CompactStep / time.Second)

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(
		`SELECT ts / ? * ?, COUNT(*), AVG(amount_left), AVG(keg_temperature),
		        AVG(percent_of_beer_left), MAX(is_pouring)
		 FROM keg_log WHERE keg_id = ? AND ts >= ? AND ts < ?
		 GROUP BY ts / ?
		 HAVING COUNT(*) > 1 OR MIN(ts) % ? <> 0`,
		step, step, id, from, to, step, step)
	if err != nil {
		return 0, err
	}
	type bucket struct {
		start   int64
		count   int64
		amount  sql.NullFloat64
		temp    sql.NullFloat64
		percent sql.NullFloat64
		pouring sql.NullInt64
	}
	var buckets []bucket
	for rows.Next() {
		var b bucket
		if err := rows.Scan(&b.start, &b.count, &b.amount, &b.temp, &b.percent, &b.pouring); err != nil {
			rows.Close()
			return 0, err
		}
		buckets = append(buckets, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(buckets) == 0 {
		return 0, nil
	}

	var removed int64
	for _, b := range buckets {
		if _, err := tx.Exec("DELETE FROM keg_log WHERE keg_id = ? AND ts >= ? AND ts < ?",
			id, b.start, b.start+step); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(
			`INSERT INTO keg_log
			 (keg_id, ts, amount_left, keg_temperature, percent_of_beer_left, is_pouring)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			id, b.start, b.amount, b.temp, b.percent, b.pouring); err != nil {
			return 0, err
		}
		removed += b.count - 1
	}
	return removed, tx.Commit()
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
