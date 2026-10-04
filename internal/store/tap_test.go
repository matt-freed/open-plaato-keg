package store

import (
	"database/sql"
	"errors"
	"testing"
)

func intPtr(n int) *int           { return &n }
func floatPtr(f float64) *float64 { return &f }

func TestTapCRUD(t *testing.T) {
	s := newTestStore(t)

	tap := &Tap{ID: NewID(), TapNumber: intPtr(1), Name: "Pale Ale", Brewery: "Home",
		ABV: floatPtr(5.2), KegID: "keg-1"}
	if err := s.SaveTap(tap); err != nil {
		t.Fatalf("SaveTap: %v", err)
	}

	got, err := s.GetTap(tap.ID)
	if err != nil {
		t.Fatalf("GetTap: %v", err)
	}
	if got.Name != "Pale Ale" || got.KegID != "keg-1" {
		t.Errorf("got %+v", got)
	}
	if got.Color != DefaultTapColor {
		t.Errorf("Color = %q, want the default %q", got.Color, DefaultTapColor)
	}

	tap.Name = "IPA"
	if err := s.SaveTap(tap); err != nil {
		t.Fatalf("SaveTap: %v", err)
	}
	got, _ = s.GetTap(tap.ID)
	if got.Name != "IPA" {
		t.Errorf("Name = %q, want the update to stick", got.Name)
	}

	if err := s.DeleteTap(tap.ID); err != nil {
		t.Fatalf("DeleteTap: %v", err)
	}
	if _, err := s.GetTap(tap.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTap after delete = %v, want ErrNotFound", err)
	}
}

// Unnumbered taps sort after numbered ones rather than first.
func TestListTapsOrdering(t *testing.T) {
	s := newTestStore(t)
	for _, tap := range []*Tap{
		{ID: "c", Name: "third", TapNumber: intPtr(3)},
		{ID: "a", Name: "first", TapNumber: intPtr(1)},
		{ID: "z", Name: "unnumbered"},
		{ID: "b", Name: "second", TapNumber: intPtr(2)},
	} {
		if err := s.SaveTap(tap); err != nil {
			t.Fatalf("SaveTap: %v", err)
		}
	}

	taps, err := s.ListTaps()
	if err != nil {
		t.Fatalf("ListTaps: %v", err)
	}
	want := []string{"a", "b", "c", "z"}
	if len(taps) != len(want) {
		t.Fatalf("got %d taps, want %d", len(taps), len(want))
	}
	for i, tap := range taps {
		if tap.ID != want[i] {
			t.Errorf("position %d = %q, want %q", i, tap.ID, want[i])
		}
	}
}

func TestTapSRMRoundTrips(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveTap(&Tap{ID: "a", SRM: floatPtr(12.5)}); err != nil {
		t.Fatalf("SaveTap: %v", err)
	}
	if err := s.SaveTap(&Tap{ID: "b"}); err != nil {
		t.Fatalf("SaveTap: %v", err)
	}
	if got, _ := s.GetTap("a"); got.SRM == nil || *got.SRM != 12.5 {
		t.Errorf("SRM = %v, want 12.5", got.SRM)
	}
	// Unset stays unset, so the tap list can fall back to the old colour.
	if got, _ := s.GetTap("b"); got.SRM != nil {
		t.Errorf("SRM = %v, want nil", *got.SRM)
	}
}

// A database created before taps had an srm column gains it on open, and its
// existing taps survive. Tables and columns this version no longer uses, such
// as beverages, tap_handles, handle_image and device_id, are left in place and
// must not get in the way.
func TestMigrateOldTaps(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`CREATE TABLE taps (
		id TEXT PRIMARY KEY, tap_number INTEGER, name TEXT NOT NULL DEFAULT '',
		brewery TEXT NOT NULL DEFAULT '', style TEXT NOT NULL DEFAULT '', abv REAL, ibu REAL,
		color TEXT NOT NULL DEFAULT '#c9a849', description TEXT NOT NULL DEFAULT '',
		tasting_notes TEXT NOT NULL DEFAULT '', expiration_date TEXT NOT NULL DEFAULT '',
		keg_id TEXT NOT NULL DEFAULT '', handle_image TEXT NOT NULL DEFAULT '',
		device_id TEXT NOT NULL DEFAULT '');
		INSERT INTO taps (id, name, color, handle_image) VALUES ('old', 'Pale Ale', '#e8b33a', 'h.jpg');
		CREATE TABLE beverages (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '');
		INSERT INTO beverages (id, name) VALUES ('b', 'Saison');
		CREATE TABLE tap_handles (filename TEXT PRIMARY KEY, uploaded_at INTEGER NOT NULL DEFAULT 0);
		INSERT INTO tap_handles (filename) VALUES ('h.jpg');`); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ { // the second pass must be a no-op
		if err := applySchema(db, schema); err != nil {
			t.Fatalf("applySchema pass %d: %v", i+1, err)
		}
	}

	s := &Store{db: db}
	got, err := s.GetTap("old")
	if err != nil {
		t.Fatalf("GetTap: %v", err)
	}
	if got.Name != "Pale Ale" || got.Color != "#e8b33a" || got.SRM != nil || got.ColorPreset != "" {
		t.Errorf("migrated tap = %+v", got)
	}
	if err := s.SaveTap(&Tap{ID: "old", ColorPreset: "clear"}); err != nil {
		t.Fatalf("SaveTap after migration: %v", err)
	}
	if got, _ := s.GetTap("old"); got.ColorPreset != "clear" {
		t.Errorf("ColorPreset = %q after migration, want clear", got.ColorPreset)
	}
}

func TestOrderTaps(t *testing.T) {
	s := newTestStore(t)
	for _, tap := range []*Tap{
		{ID: "a", TapNumber: intPtr(1)},
		{ID: "b", TapNumber: intPtr(2)},
		{ID: "c"},
	} {
		if err := s.SaveTap(tap); err != nil {
			t.Fatalf("SaveTap: %v", err)
		}
	}

	if err := s.OrderTaps([]string{"c", "a", "b"}); err != nil {
		t.Fatalf("OrderTaps: %v", err)
	}
	taps, _ := s.ListTaps()
	want := []string{"c", "a", "b"}
	for i, tap := range taps {
		if tap.ID != want[i] || tap.TapNumber == nil || *tap.TapNumber != i+1 {
			t.Errorf("position %d = %q (number %v), want %q numbered %d",
				i, tap.ID, tap.TapNumber, want[i], i+1)
		}
	}

	// An unknown id rolls the whole reorder back.
	if err := s.OrderTaps([]string{"b", "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("OrderTaps with an unknown id = %v, want ErrNotFound", err)
	}
	if got, _ := s.GetTap("b"); *got.TapNumber != 3 {
		t.Errorf("tap b number = %d, want 3 after the failed reorder", *got.TapNumber)
	}
}

// Gravities arrive both as specific gravity and as the points the hardware
// reports.
// Deleting a handle must clear it from any tap using it, or the tap list shows
// a broken image.
func TestAppConfigDefaultsAndUpdates(t *testing.T) {
	s := newTestStore(t)

	cfg, err := s.GetAppConfig()
	if err != nil {
		t.Fatalf("GetAppConfig: %v", err)
	}
	if cfg.HomePage != HomePageTapList || cfg.TimeFormat != TimeFormat12h {
		t.Errorf("defaults = %+v", cfg)
	}

	if err := s.SetHomePage(HomePageKegs); err != nil {
		t.Fatalf("SetHomePage: %v", err)
	}
	if err := s.SetTimeFormat(TimeFormat24h); err != nil {
		t.Fatalf("SetTimeFormat: %v", err)
	}
	if err := s.SetTheme(Theme{AccentColor: "#ff0000", CardBg: "#111111"}); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}

	cfg, err = s.GetAppConfig()
	if err != nil {
		t.Fatalf("GetAppConfig: %v", err)
	}
	if cfg.HomePage != HomePageKegs || cfg.TimeFormat != TimeFormat24h {
		t.Errorf("got %+v", cfg)
	}
	if cfg.Theme.AccentColor != "#ff0000" || cfg.Theme.CardBg != "#111111" {
		t.Errorf("Theme = %+v", cfg.Theme)
	}
}

// Unrecognised values fall back to a supported one rather than being stored.
func TestAppConfigNormalisesInput(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetHomePage("nonsense"); err != nil {
		t.Fatalf("SetHomePage: %v", err)
	}
	if err := s.SetTimeFormat("48h"); err != nil {
		t.Fatalf("SetTimeFormat: %v", err)
	}
	cfg, _ := s.GetAppConfig()
	if cfg.HomePage != HomePageTapList {
		t.Errorf("HomePage = %q, want the taplist fallback", cfg.HomePage)
	}
	if cfg.TimeFormat != TimeFormat12h {
		t.Errorf("TimeFormat = %q, want the 12h fallback", cfg.TimeFormat)
	}
}

func TestNewIDIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewID()
		if seen[id] {
			t.Fatalf("NewID returned a duplicate: %q", id)
		}
		seen[id] = true
	}
}

// A keg feeds one tap. Another tap cannot take it until the first lets go, but
// a tap may be re-saved with its own keg, and any number may have none.
func TestKegFeedsOneTap(t *testing.T) {
	s := newTestStore(t)
	first := &Tap{ID: "tap-1", TapNumber: intPtr(3), Name: "Red Barn Amber", KegID: "keg-1"}
	second := &Tap{ID: "tap-2", TapNumber: intPtr(4), Name: "Pils", KegID: "keg-1"}

	if err := s.SaveTap(first); err != nil {
		t.Fatalf("SaveTap first: %v", err)
	}
	var inUse *KegInUseError
	if err := s.SaveTap(second); !errors.As(err, &inUse) {
		t.Fatalf("SaveTap second = %v, want a KegInUseError", err)
	}
	if inUse.Tap.ID != "tap-1" || inUse.Tap.Name != "Red Barn Amber" {
		t.Errorf("conflicting tap = %+v, want tap-1", inUse.Tap)
	}
	if _, err := s.GetTap("tap-2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the refused tap was saved anyway: %v", err)
	}

	first.Name = "Amber"
	if err := s.SaveTap(first); err != nil {
		t.Errorf("re-saving a tap with its own keg: %v", err)
	}

	second.KegID = ""
	if err := s.SaveTap(second); err != nil {
		t.Fatalf("SaveTap with no keg: %v", err)
	}
	if err := s.SaveTap(&Tap{ID: "tap-3", TapNumber: intPtr(5), Name: "Cider"}); err != nil {
		t.Errorf("a second tap with no keg: %v", err)
	}

	first.KegID = ""
	if err := s.SaveTap(first); err != nil {
		t.Fatalf("unlinking: %v", err)
	}
	second.KegID = "keg-1"
	if err := s.SaveTap(second); err != nil {
		t.Errorf("linking the freed keg to another tap: %v", err)
	}
}

func TestLinkTaps(t *testing.T) {
	s := newTestStore(t)
	for _, tap := range []*Tap{
		{ID: "a", TapNumber: intPtr(1), Name: "IPA", KegID: "keg-1"},
		{ID: "b", TapNumber: intPtr(2), Name: "Stout", KegID: "keg-2"},
		{ID: "c", TapNumber: intPtr(3), Name: "Cider"},
	} {
		if err := s.SaveTap(tap); err != nil {
			t.Fatalf("SaveTap: %v", err)
		}
	}
	kegOf := func(id string) string {
		t.Helper()
		tap, err := s.GetTap(id)
		if err != nil {
			t.Fatalf("GetTap %s: %v", id, err)
		}
		return tap.KegID
	}
	want := func(links map[string]string) {
		t.Helper()
		for id, keg := range links {
			if got := kegOf(id); got != keg {
				t.Errorf("tap %s keg = %q, want %q", id, got, keg)
			}
		}
	}

	// A swap would trip the one-tap-per-keg check partway through if the links
	// were checked one at a time.
	if err := s.LinkTaps([]TapLink{{"a", "keg-2"}, {"b", "keg-1"}}); err != nil {
		t.Fatalf("swap: %v", err)
	}
	want(map[string]string{"a": "keg-2", "b": "keg-1"})

	if err := s.LinkTaps([]TapLink{{"c", "keg-3"}}); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := s.LinkTaps([]TapLink{{"c", ""}}); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	want(map[string]string{"c": ""})

	// A keg left on two taps refuses the whole call.
	var inUse *KegInUseError
	err := s.LinkTaps([]TapLink{{"c", "keg-3"}, {"a", "keg-1"}})
	if !errors.As(err, &inUse) {
		t.Fatalf("conflict = %v, want a KegInUseError", err)
	}
	if inUse.Tap.ID != "b" {
		t.Errorf("conflicting tap = %q, want b", inUse.Tap.ID)
	}
	want(map[string]string{"a": "keg-2", "b": "keg-1", "c": ""})

	if err := s.LinkTaps([]TapLink{{"c", "keg-3"}, {"nope", "keg-4"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown tap = %v, want ErrNotFound", err)
	}
	want(map[string]string{"c": ""})
}

// A keg already on two taps, from before the check existed, does not stop
// other kegs being rearranged, and moving one of its taps away resolves it.
func TestLinkTapsWithLegacyDuplicate(t *testing.T) {
	s := newTestStore(t)
	for _, tap := range []*Tap{
		{ID: "a", TapNumber: intPtr(1)},
		{ID: "b", TapNumber: intPtr(2)},
		{ID: "c", TapNumber: intPtr(3)},
	} {
		if err := s.SaveTap(tap); err != nil {
			t.Fatalf("SaveTap: %v", err)
		}
	}
	if _, err := s.db.Exec("UPDATE taps SET keg_id = 'dup' WHERE id IN ('a', 'b')"); err != nil {
		t.Fatal(err)
	}

	if err := s.LinkTaps([]TapLink{{"c", "keg-9"}}); err != nil {
		t.Errorf("linking an unrelated keg: %v", err)
	}
	if err := s.LinkTaps([]TapLink{{"b", ""}}); err != nil {
		t.Errorf("unlinking a duplicate: %v", err)
	}
}
