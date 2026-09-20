package store

import (
	"bytes"
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

	if err := s.AppendLog(&Keg{ID: "keg-1"}, now.Add(-LogRetention-time.Hour)); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}
	if err := s.AppendLog(&Keg{ID: "keg-1"}, now.Add(-time.Hour)); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}

	removed, err := s.PruneLog(now)
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
