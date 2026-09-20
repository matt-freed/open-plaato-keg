package store

import (
	"errors"
	"testing"
	"time"
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

func TestGetTapByDeviceID(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveTap(&Tap{ID: "a", Name: "Pale Ale", DeviceID: "AB12CD"}); err != nil {
		t.Fatalf("SaveTap: %v", err)
	}
	if err := s.SaveTap(&Tap{ID: "b", Name: "Stout"}); err != nil {
		t.Fatalf("SaveTap: %v", err)
	}

	// The firmware's casing is not guaranteed.
	for _, id := range []string{"AB12CD", "ab12cd", "Ab12Cd"} {
		got, err := s.GetTapByDeviceID(id)
		if err != nil {
			t.Fatalf("GetTapByDeviceID(%q): %v", id, err)
		}
		if got.ID != "a" {
			t.Errorf("GetTapByDeviceID(%q) = %q, want a", id, got.ID)
		}
	}

	// An empty device id must not match the taps that have none.
	if _, err := s.GetTapByDeviceID(""); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty device id = %v, want ErrNotFound", err)
	}
	if _, err := s.GetTapByDeviceID("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown device id = %v, want ErrNotFound", err)
	}
}

func TestSaveTapTruncatesDeviceID(t *testing.T) {
	s := newTestStore(t)
	tap := &Tap{ID: "a", DeviceID: "TOOLONGFORTHEDEVICE"}
	if err := s.SaveTap(tap); err != nil {
		t.Fatalf("SaveTap: %v", err)
	}
	got, _ := s.GetTap("a")
	if len(got.DeviceID) != DeviceIDMaxLen {
		t.Errorf("DeviceID = %q, want it truncated to %d characters", got.DeviceID, DeviceIDMaxLen)
	}
}

func TestBeverageCRUD(t *testing.T) {
	s := newTestStore(t)

	b := &Beverage{ID: NewID(), Name: "Saison", ABV: floatPtr(6.1), CreatedAt: time.Now().Unix()}
	if err := s.SaveBeverage(b); err != nil {
		t.Fatalf("SaveBeverage: %v", err)
	}
	got, err := s.GetBeverage(b.ID)
	if err != nil {
		t.Fatalf("GetBeverage: %v", err)
	}
	if got.Name != "Saison" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Source != "manual" {
		t.Errorf("Source = %q, want the default manual", got.Source)
	}

	if err := s.DeleteBeverage(b.ID); err != nil {
		t.Fatalf("DeleteBeverage: %v", err)
	}
	if _, err := s.GetBeverage(b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetBeverage after delete = %v, want ErrNotFound", err)
	}
}

func TestListBeveragesSortedCaseInsensitively(t *testing.T) {
	s := newTestStore(t)
	for _, name := range []string{"zebra", "Apple", "mango"} {
		if err := s.SaveBeverage(&Beverage{ID: name, Name: name}); err != nil {
			t.Fatalf("SaveBeverage: %v", err)
		}
	}
	list, err := s.ListBeverages()
	if err != nil {
		t.Fatalf("ListBeverages: %v", err)
	}
	want := []string{"Apple", "mango", "zebra"}
	for i, b := range list {
		if b.Name != want[i] {
			t.Errorf("position %d = %q, want %q", i, b.Name, want[i])
		}
	}
}

// Gravities arrive both as specific gravity and as the points the hardware
// reports.
func TestEstimateABV(t *testing.T) {
	tests := []struct {
		og, fg, want float64
	}{
		{1.050, 1.010, 5.25},
		{1050, 1010, 5.25},
		{1.055, 1.015, 5.25},
	}
	for _, tt := range tests {
		// The result is rounded, so it must match exactly rather than
		// approximately — an unrounded value carries floating point noise into
		// the API.
		if got := EstimateABV(tt.og, tt.fg); got != tt.want {
			t.Errorf("EstimateABV(%v, %v) = %v, want %v", tt.og, tt.fg, got, tt.want)
		}
	}
}

func TestTapHandles(t *testing.T) {
	s := newTestStore(t)
	now := time.Unix(1_700_000_000, 0)

	if err := s.AddTapHandle("one.jpg", now); err != nil {
		t.Fatalf("AddTapHandle: %v", err)
	}
	if err := s.AddTapHandle("two.jpg", now.Add(time.Minute)); err != nil {
		t.Fatalf("AddTapHandle: %v", err)
	}

	handles, err := s.ListTapHandles()
	if err != nil {
		t.Fatalf("ListTapHandles: %v", err)
	}
	if len(handles) != 2 || handles[0].Filename != "two.jpg" {
		t.Errorf("got %+v, want the newest first", handles)
	}

	if ok, _ := s.HasTapHandle("one.jpg"); !ok {
		t.Error("HasTapHandle = false for an uploaded handle")
	}
	if ok, _ := s.HasTapHandle("nope.jpg"); ok {
		t.Error("HasTapHandle = true for a handle that was never uploaded")
	}
}

// Deleting a handle must clear it from any tap using it, or the tap list shows
// a broken image.
func TestDeleteTapHandleClearsReferences(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddTapHandle("handle.jpg", time.Now()); err != nil {
		t.Fatalf("AddTapHandle: %v", err)
	}
	if err := s.SaveTap(&Tap{ID: "a", HandleImage: "handle.jpg"}); err != nil {
		t.Fatalf("SaveTap: %v", err)
	}
	if err := s.SaveTap(&Tap{ID: "b", HandleImage: "other.jpg"}); err != nil {
		t.Fatalf("SaveTap: %v", err)
	}

	if err := s.DeleteTapHandle("handle.jpg"); err != nil {
		t.Fatalf("DeleteTapHandle: %v", err)
	}

	a, _ := s.GetTap("a")
	if a.HandleImage != "" {
		t.Errorf("tap a still references %q", a.HandleImage)
	}
	b, _ := s.GetTap("b")
	if b.HandleImage != "other.jpg" {
		t.Errorf("tap b's unrelated handle was cleared")
	}
}

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
	if err := s.SetTheme(Theme{AccentColor: "#ff0000", BgOpacity: "0.5"}); err != nil {
		t.Fatalf("SetTheme: %v", err)
	}

	cfg, err = s.GetAppConfig()
	if err != nil {
		t.Fatalf("GetAppConfig: %v", err)
	}
	if cfg.HomePage != HomePageKegs || cfg.TimeFormat != TimeFormat24h {
		t.Errorf("got %+v", cfg)
	}
	if cfg.Theme.AccentColor != "#ff0000" || cfg.Theme.BgOpacity != "0.5" {
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
