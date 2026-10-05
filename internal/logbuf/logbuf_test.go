package logbuf

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func newLogger(level slog.Level) (*slog.Logger, *Buffer, *bytes.Buffer) {
	var out bytes.Buffer
	buf := New()
	inner := slog.NewTextHandler(&out, &slog.HandlerOptions{Level: level})
	return slog.New(NewHandler(inner, buf)), buf, &out
}

func TestCapturesAndPassesThrough(t *testing.T) {
	log, buf, out := newLogger(slog.LevelInfo)

	log.Debug("hidden")
	log.Info("hello", "keg", "abc", "n", 3)

	if !strings.Contains(out.String(), "msg=hello") {
		t.Errorf("inner handler did not receive the record: %q", out.String())
	}
	page := buf.Since(0)
	records, oldest := page.Records, page.Oldest
	if page.Latest != 1 {
		t.Errorf("latest = %d, want 1", page.Latest)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1 (debug should not be captured)", len(records))
	}
	r := records[0]
	if r.Seq != 1 || oldest != 1 {
		t.Errorf("seq = %d, oldest = %d, want 1 and 1", r.Seq, oldest)
	}
	if r.Level != "INFO" || r.Message != "hello" {
		t.Errorf("record = %+v", r)
	}
	want := []Attr{{"keg", "abc"}, {"n", "3"}}
	if len(r.Attrs) != len(want) || r.Attrs[0] != want[0] || r.Attrs[1] != want[1] {
		t.Errorf("attrs = %v, want %v", r.Attrs, want)
	}
}

func TestWrapsAround(t *testing.T) {
	log, buf, _ := newLogger(slog.LevelInfo)

	for i := 0; i < Capacity+5; i++ {
		log.Info("line")
	}
	page := buf.Since(0)
	records, oldest := page.Records, page.Oldest
	if len(records) != Capacity {
		t.Fatalf("got %d records, want %d", len(records), Capacity)
	}
	if oldest != 6 || records[0].Seq != 6 || records[len(records)-1].Seq != Capacity+5 {
		t.Errorf("oldest = %d, first = %d, last = %d", oldest, records[0].Seq, records[len(records)-1].Seq)
	}

	recent := buf.Since(Capacity + 3).Records
	if len(recent) != 2 || recent[0].Seq != Capacity+4 {
		t.Errorf("Since returned %+v", recent)
	}
}

func TestEmpty(t *testing.T) {
	page := New().Since(0)
	if page.Records == nil || len(page.Records) != 0 || page.Oldest != 0 || page.Latest != 0 {
		t.Errorf("page = %+v", page)
	}
}

func TestGroupsAndWithAttrs(t *testing.T) {
	log, buf, _ := newLogger(slog.LevelInfo)

	log.With("conn", 7).WithGroup("req").Info("x", "path", "/a", slog.Group("q", "id", 2))

	got := buf.Since(0).Records[0].Attrs
	want := []Attr{{"conn", "7"}, {"req.path", "/a"}, {"req.q.id", "2"}}
	if len(got) != len(want) {
		t.Fatalf("attrs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("attrs[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestConcurrent(t *testing.T) {
	log, buf, _ := newLogger(slog.LevelInfo)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				log.Info("line", "i", i)
				buf.Since(0)
			}
		}()
	}
	wg.Wait()

	records := buf.Since(0).Records
	if len(records) != Capacity || records[len(records)-1].Seq != 1600 {
		t.Errorf("got %d records ending at %d", len(records), records[len(records)-1].Seq)
	}
}
