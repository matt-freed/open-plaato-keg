package store

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLogThrottleAdmitsOnePerInterval(t *testing.T) {
	th := NewLogThrottle()
	base := time.Unix(1_700_000_000, 0)

	if !th.Allow("keg-1", base) {
		t.Fatal("first reading was rejected")
	}
	if th.Allow("keg-1", base.Add(30*time.Second)) {
		t.Error("a reading 30s later was admitted; the interval is one minute")
	}
	if !th.Allow("keg-1", base.Add(LogInterval)) {
		t.Error("a reading a full interval later was rejected")
	}
	// Kegs are throttled independently.
	if !th.Allow("keg-2", base.Add(30*time.Second)) {
		t.Error("a different keg was throttled by keg-1's reading")
	}
}

func TestHasLoggableReading(t *testing.T) {
	amount, temp, pct := 1.0, 2.0, 3.0
	pouring := false

	if (&Keg{ID: "keg-1"}).HasLoggableReading() {
		t.Error("a keg with no readings reported as loggable")
	}
	// Announcing a firmware version is not a reading.
	fw := "2.0.11b"
	if (&Keg{ID: "keg-1", FirmwareVersion: &fw}).HasLoggableReading() {
		t.Error("a keg that has only announced its firmware reported as loggable")
	}
	// A genuine zero is a reading, not an absence.
	zero := 0.0
	if !(&Keg{ID: "keg-1", AmountLeft: &zero}).HasLoggableReading() {
		t.Error("an empty keg reporting zero was treated as having no reading")
	}
	for name, k := range map[string]*Keg{
		"amount":      {AmountLeft: &amount},
		"temperature": {KegTemperature: &temp},
		"percent":     {PercentOfBeerLeft: &pct},
		"pouring":     {IsPouring: &pouring},
	} {
		if !k.HasLoggableReading() {
			t.Errorf("a keg reporting %s was treated as having no reading", name)
		}
	}
}

func TestAppendAndReadLog(t *testing.T) {
	s := newTestStore(t)
	base := time.Unix(1_700_000_000, 0)

	amounts := []float64{5.0, 4.5, 4.0}
	for i, a := range amounts {
		amount := a
		k := &Keg{ID: "keg-1", AmountLeft: &amount}
		if err := s.AppendLog(k, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
	}

	entries, err := s.ReadLog("keg-1", base, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReadLog: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	for i, e := range entries {
		if e.AmountLeft == nil || *e.AmountLeft != amounts[i] {
			t.Errorf("entry %d amount = %v, want %v", i, e.AmountLeft, amounts[i])
		}
		if i > 0 && e.Timestamp <= entries[i-1].Timestamp {
			t.Errorf("entries are not ordered oldest first: %d after %d", e.Timestamp, entries[i-1].Timestamp)
		}
	}
}

func TestReadLogRespectsWindow(t *testing.T) {
	s := newTestStore(t)
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < 5; i++ {
		if err := s.AppendLog(&Keg{ID: "keg-1"}, base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
	}

	entries, err := s.ReadLog("keg-1", base.Add(time.Hour), base.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("ReadLog: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("got %d entries for a 2-hour inclusive window, want 3", len(entries))
	}
}

func TestReadLogIsPerKeg(t *testing.T) {
	s := newTestStore(t)
	base := time.Unix(1_700_000_000, 0)
	if err := s.AppendLog(&Keg{ID: "keg-1"}, base); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}
	if err := s.AppendLog(&Keg{ID: "keg-2"}, base); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}

	entries, err := s.ReadLog("keg-1", base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReadLog: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries, want only keg-1's", len(entries))
	}
}

func TestPruneLog(t *testing.T) {
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0)
	retention := 30 * 24 * time.Hour

	if err := s.AppendLog(&Keg{ID: "keg-1"}, now.Add(-retention-time.Hour)); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}
	if err := s.AppendLog(&Keg{ID: "keg-1"}, now.Add(-time.Hour)); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}

	removed, err := s.PruneLog(now, retention)
	if err != nil {
		t.Fatalf("PruneLog: %v", err)
	}
	if removed != 1 {
		t.Errorf("pruned %d rows, want 1", removed)
	}

	entries, err := s.ReadLog("keg-1", time.Unix(0, 0), now)
	if err != nil {
		t.Fatalf("ReadLog: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries after pruning, want 1", len(entries))
	}
}

// Deleting a keg must take its history with it.
// Clearing one keg's history leaves the keg and every other keg's history.
func TestClearLog(t *testing.T) {
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0)
	for _, id := range []string{"keg-1", "keg-1", "keg-2"} {
		now = now.Add(time.Minute)
		if err := s.AppendLog(&Keg{ID: id}, now); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
	}

	removed, err := s.ClearLog("keg-1")
	if err != nil {
		t.Fatalf("ClearLog: %v", err)
	}
	if removed != 2 {
		t.Errorf("cleared %d rows, want 2", removed)
	}
	for id, want := range map[string]int{"keg-1": 0, "keg-2": 1} {
		entries, err := s.ReadLog(id, time.Unix(0, 0), now)
		if err != nil {
			t.Fatalf("ReadLog(%s): %v", id, err)
		}
		if len(entries) != want {
			t.Errorf("%s has %d entries after clearing keg-1, want %d", id, len(entries), want)
		}
	}
}

func TestDeleteKegRemovesLog(t *testing.T) {
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0)
	if _, err := s.ApplyPacket("keg-1", decode(t, "vw\x0051\x001.000")); err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}
	if err := s.AppendLog(&Keg{ID: "keg-1"}, now); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}
	if err := s.DeleteKeg("keg-1"); err != nil {
		t.Fatalf("DeleteKeg: %v", err)
	}

	entries, err := s.ReadLog("keg-1", time.Unix(0, 0), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReadLog: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries after deleting the keg, want 0", len(entries))
	}
}

func TestWriteLogCSV(t *testing.T) {
	amount, temp := 3.802, 22.875
	pouring := true
	entries := []LogEntry{
		{Timestamp: 1_700_000_000, AmountLeft: &amount, KegTemperature: &temp, IsPouring: &pouring},
		{Timestamp: 1_700_000_060}, // a reading with nothing recorded
	}

	var buf bytes.Buffer
	if err := WriteLogCSV(&buf, entries); err != nil {
		t.Fatalf("WriteLogCSV: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want a header and two rows:\n%s", len(lines), buf.String())
	}
	if lines[0] != "timestamp,amount_left,keg_temperature,percent_of_beer_left,is_pouring" {
		t.Errorf("header = %q", lines[0])
	}
	if !strings.Contains(lines[1], "3.802") || !strings.Contains(lines[1], "true") {
		t.Errorf("row = %q", lines[1])
	}
	// Missing values are empty fields, not zeros.
	if !strings.HasSuffix(strings.TrimSpace(lines[2]), ",,,,") {
		t.Errorf("empty row = %q, want empty fields rather than zeros", lines[2])
	}
}

// A retention of zero means keep history forever.
func TestPruneLogZeroRetentionKeepsEverything(t *testing.T) {
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0)

	if err := s.AppendLog(&Keg{ID: "keg-1"}, now.Add(-10*365*24*time.Hour)); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}

	removed, err := s.PruneLog(now, 0)
	if err != nil {
		t.Fatalf("PruneLog: %v", err)
	}
	if removed != 0 {
		t.Errorf("pruned %d rows with zero retention, want 0", removed)
	}
}

// logAt records one reading for id at ts.
func logAt(t *testing.T, s *Store, id string, ts time.Time, amount, temp float64, pouring bool) {
	t.Helper()
	if err := s.AppendLog(&Keg{ID: id, AmountLeft: &amount, KegTemperature: &temp, IsPouring: &pouring}, ts); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}
}

func TestReadLogSampledAveragesEachStep(t *testing.T) {
	s := newTestStore(t)
	base := time.Unix(1_700_000_000, 0).Truncate(time.Hour)

	// Two steps of an hour: the first has three readings, one of them pouring;
	// the second has one reading with no temperature.
	logAt(t, s, "keg-1", base, 10, 4, false)
	logAt(t, s, "keg-1", base.Add(20*time.Minute), 9, 5, true)
	logAt(t, s, "keg-1", base.Add(40*time.Minute), 8, 6, false)
	amount := 7.0
	if err := s.AppendLog(&Keg{ID: "keg-1", AmountLeft: &amount}, base.Add(90*time.Minute)); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}
	// Another keg's readings stay out of it.
	logAt(t, s, "keg-2", base, 100, 100, true)

	entries, err := s.ReadLogSampled("keg-1", base, base.Add(2*time.Hour), time.Hour)
	if err != nil {
		t.Fatalf("ReadLogSampled: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	first := entries[0]
	if first.Timestamp != base.Add(20*time.Minute).Unix() {
		t.Errorf("first timestamp = %d, want the average time %d", first.Timestamp, base.Add(20*time.Minute).Unix())
	}
	if first.AmountLeft == nil || *first.AmountLeft != 9 {
		t.Errorf("first amount = %v, want 9", first.AmountLeft)
	}
	if first.KegTemperature == nil || *first.KegTemperature != 5 {
		t.Errorf("first temperature = %v, want 5", first.KegTemperature)
	}
	if first.IsPouring == nil || !*first.IsPouring {
		t.Error("first step should be pouring, as one of its readings was")
	}

	second := entries[1]
	if second.AmountLeft == nil || *second.AmountLeft != 7 {
		t.Errorf("second amount = %v, want 7", second.AmountLeft)
	}
	if second.KegTemperature != nil {
		t.Errorf("second temperature = %v, want nil as none was reported", *second.KegTemperature)
	}
	if second.IsPouring != nil {
		t.Errorf("second pouring = %v, want nil as none was reported", *second.IsPouring)
	}
}

func TestReadLogSampledWithoutStepReturnsEveryReading(t *testing.T) {
	s := newTestStore(t)
	base := time.Unix(1_700_000_000, 0)
	for i := range 3 {
		logAt(t, s, "keg-1", base.Add(time.Duration(i)*time.Minute), 10, 4, false)
	}

	entries, err := s.ReadLogSampled("keg-1", base, base.Add(time.Hour), 0)
	if err != nil {
		t.Fatalf("ReadLogSampled: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("got %d entries, want all 3", len(entries))
	}
}

func TestCompactLog(t *testing.T) {
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0)
	compactAfter := 30 * 24 * time.Hour
	old := now.Add(-40 * 24 * time.Hour).Truncate(time.Hour)

	// An old hour of minute readings, half of them pouring, for two kegs.
	for i := range 60 {
		at := old.Add(time.Duration(i) * time.Minute)
		logAt(t, s, "keg-1", at, float64(60-i), 4, i%2 == 0)
		logAt(t, s, "keg-2", at, 1, 1, false)
	}
	// A single old reading that is not on the hour.
	logAt(t, s, "keg-1", old.Add(5*time.Hour+7*time.Minute), 3, 2, false)
	// Recent readings, inside the full-resolution window.
	for i := range 5 {
		logAt(t, s, "keg-1", now.Add(-time.Duration(i+1)*time.Minute), 1, 1, false)
	}

	removed, err := s.CompactLog(now, compactAfter)
	if err != nil {
		t.Fatalf("CompactLog: %v", err)
	}
	// 59 from each keg's full hour; the lone reading is moved, not removed.
	if removed != 118 {
		t.Errorf("removed %d rows, want 118", removed)
	}

	entries, err := s.ReadLog("keg-1", time.Unix(0, 0), now)
	if err != nil {
		t.Fatalf("ReadLog: %v", err)
	}
	if len(entries) != 7 {
		t.Fatalf("keg-1 has %d entries, want 1 + 1 compacted and 5 recent", len(entries))
	}
	hour := entries[0]
	if hour.Timestamp != old.Unix() {
		t.Errorf("compacted timestamp = %d, want the start of the hour %d", hour.Timestamp, old.Unix())
	}
	if hour.AmountLeft == nil || *hour.AmountLeft != 30.5 {
		t.Errorf("compacted amount = %v, want the average 30.5", hour.AmountLeft)
	}
	if hour.IsPouring == nil || !*hour.IsPouring {
		t.Error("compacted hour should be pouring")
	}
	if entries[1].Timestamp != old.Add(5*time.Hour).Unix() {
		t.Errorf("lone reading at %d, want it moved to the start of its hour %d",
			entries[1].Timestamp, old.Add(5*time.Hour).Unix())
	}

	other, err := s.ReadLog("keg-2", time.Unix(0, 0), now)
	if err != nil {
		t.Fatalf("ReadLog: %v", err)
	}
	if len(other) != 1 {
		t.Errorf("keg-2 has %d entries, want its hour compacted separately to 1", len(other))
	}

	again, err := s.CompactLog(now, compactAfter)
	if err != nil {
		t.Fatalf("CompactLog again: %v", err)
	}
	if again != 0 {
		t.Errorf("a second pass removed %d rows, want 0", again)
	}
}

func TestCompactLogZeroKeepsEveryReading(t *testing.T) {
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0)
	old := now.Add(-400 * 24 * time.Hour)
	logAt(t, s, "keg-1", old, 1, 1, false)
	logAt(t, s, "keg-1", old.Add(time.Minute), 1, 1, false)

	removed, err := s.CompactLog(now, 0)
	if err != nil {
		t.Fatalf("CompactLog: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed %d rows with compaction off, want 0", removed)
	}
}

// seedLog writes n readings for a keg a minute apart from base, the i-th with
// amount i.
func seedLog(t *testing.T, s *Store, id string, base time.Time, n int) {
	t.Helper()
	for i := range n {
		amount, temp := float64(i), 4.0
		k := &Keg{ID: id, AmountLeft: &amount, KegTemperature: &temp}
		if err := s.AppendLog(k, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("AppendLog: %v", err)
		}
	}
}

func pageTimes(p LogPage) []int64 {
	ts := make([]int64, len(p.Entries))
	for i, e := range p.Entries {
		ts[i] = e.Timestamp
	}
	return ts
}

func TestReadLogPage(t *testing.T) {
	s := newTestStore(t)
	base := time.Unix(1_700_000_000, 0)
	seedLog(t, s, "keg-1", base, 10)
	seedLog(t, s, "keg-2", base, 3)
	at := func(i int) int64 { return base.Unix() + int64(i)*60 }
	// The window leaves out the first and last readings.
	from, to := base.Add(time.Minute), base.Add(8*time.Minute)

	check := func(name string, p LogPage, err error, want []int64, earlier, later bool) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := pageTimes(p); !slices.Equal(got, want) {
			t.Errorf("%s: times = %v, want %v", name, got, want)
		}
		if p.HasEarlier != earlier || p.HasLater != later {
			t.Errorf("%s: earlier, later = %v, %v, want %v, %v", name, p.HasEarlier, p.HasLater, earlier, later)
		}
	}

	p, err := s.ReadLogPage("keg-1", from, to, 0, 0, 3)
	check("first", p, err, []int64{at(1), at(2), at(3)}, false, true)
	p, err = s.ReadLogPage("keg-1", from, to, at(3), 0, 3)
	check("after", p, err, []int64{at(4), at(5), at(6)}, true, true)
	p, err = s.ReadLogPage("keg-1", from, to, at(6), 0, 3)
	check("last", p, err, []int64{at(7), at(8)}, true, false)
	p, err = s.ReadLogPage("keg-1", from, to, 0, at(7), 3)
	check("before", p, err, []int64{at(4), at(5), at(6)}, true, true)
	p, err = s.ReadLogPage("keg-1", from, to, 0, at(3), 3)
	check("before start", p, err, []int64{at(1), at(2)}, false, true)
	p, err = s.ReadLogPage("keg-1", from, to, at(8), 0, 3)
	check("past the end", p, err, []int64{}, true, false)
	p, err = s.ReadLogPage("nobody", from, to, 0, 0, 3)
	check("no readings", p, err, []int64{}, false, false)
}

func TestUpdateLogEntriesChangesOnlyWhatIsSet(t *testing.T) {
	s := newTestStore(t)
	base := time.Unix(1_700_000_000, 0)
	seedLog(t, s, "keg-1", base, 2)
	pouring := true

	err := s.UpdateLogEntries("keg-1", []LogEdit{{
		Timestamp:      base.Unix(),
		AmountLeft:     LogValue[float64]{Set: true, Value: f64(9.5)},
		KegTemperature: LogValue[float64]{Set: true}, // cleared
		IsPouring:      LogValue[bool]{Set: true, Value: &pouring},
	}})
	if err != nil {
		t.Fatalf("UpdateLogEntries: %v", err)
	}
	entries, err := s.ReadLog("keg-1", base, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReadLog: %v", err)
	}
	e := entries[0]
	if e.AmountLeft == nil || *e.AmountLeft != 9.5 {
		t.Errorf("amount = %v, want 9.5", e.AmountLeft)
	}
	if e.KegTemperature != nil {
		t.Errorf("temperature = %v, want cleared", *e.KegTemperature)
	}
	if e.IsPouring == nil || !*e.IsPouring {
		t.Errorf("is_pouring = %v, want true", e.IsPouring)
	}
	if e.PercentOfBeerLeft != nil {
		t.Errorf("percent = %v, want left unset", *e.PercentOfBeerLeft)
	}
	if other := entries[1]; *other.AmountLeft != 1 || *other.KegTemperature != 4 {
		t.Errorf("an unedited row changed: %+v", other)
	}
}

// One edit to a reading that is gone undoes the whole batch.
func TestUpdateLogEntriesRollsBackWhenARowIsMissing(t *testing.T) {
	s := newTestStore(t)
	base := time.Unix(1_700_000_000, 0)
	seedLog(t, s, "keg-1", base, 1)

	err := s.UpdateLogEntries("keg-1", []LogEdit{
		{Timestamp: base.Unix(), AmountLeft: LogValue[float64]{Set: true, Value: f64(7)}},
		{Timestamp: base.Unix() + 30, AmountLeft: LogValue[float64]{Set: true, Value: f64(7)}},
	})
	if !errors.Is(err, ErrLogEntryMissing) {
		t.Fatalf("err = %v, want ErrLogEntryMissing", err)
	}
	entries, _ := s.ReadLog("keg-1", base, base.Add(time.Hour))
	if *entries[0].AmountLeft != 0 {
		t.Errorf("amount = %v after a failed batch, want 0 unchanged", *entries[0].AmountLeft)
	}
}

func TestDeleteLogEntries(t *testing.T) {
	s := newTestStore(t)
	base := time.Unix(1_700_000_000, 0)
	seedLog(t, s, "keg-1", base, 3)
	seedLog(t, s, "keg-2", base, 3)

	removed, err := s.DeleteLogEntries("keg-1", []int64{base.Unix(), base.Unix() + 120, base.Unix() + 5})
	if err != nil {
		t.Fatalf("DeleteLogEntries: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed %d rows, want 2", removed)
	}
	for id, want := range map[string]int{"keg-1": 1, "keg-2": 3} {
		entries, _ := s.ReadLog(id, base, base.Add(time.Hour))
		if len(entries) != want {
			t.Errorf("%s has %d entries, want %d", id, len(entries), want)
		}
	}
}
