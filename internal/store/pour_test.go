package store

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// kegIn puts a keg into beer mode with the given unit system (1 metric, 2 US)
// and measure (1 weight, 2 volume), holding amount.
func kegIn(t *testing.T, s *Store, id string, unit, measure int, amount float64) {
	t.Helper()
	apply(t, s, id,
		fmt.Sprintf("vw\x0071\x00%d", unit),
		fmt.Sprintf("vw\x0075\x00%d", measure),
		"vw\x0088\x001",
		fmt.Sprintf("vw\x0051\x00%.3f", amount))
}

func apply(t *testing.T, s *Store, id string, bodies ...string) *Keg {
	t.Helper()
	k, err := s.ApplyPacket(id, decode(t, bodies...))
	if err != nil {
		t.Fatalf("ApplyPacket: %v", err)
	}
	return k
}

// pourDown plays one pouring window that takes the keg down to end, the way
// the firmware reports it: the flag, falling readings, the settled reading,
// then the flag clearing.
func pourDown(t *testing.T, s *Store, id string, end float64) {
	t.Helper()
	apply(t, s, id, "vw\x0049\x00255")
	apply(t, s, id, fmt.Sprintf("vw\x0051\x00%.3f", end))
	apply(t, s, id, "vw\x0049\x000")
}

func allPours(t *testing.T, s *Store) []*Pour {
	t.Helper()
	pours, err := s.ListPours(time.Time{}, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("ListPours: %v", err)
	}
	return pours
}

func TestPouringWindowRecordsOnePourWithASnapshot(t *testing.T) {
	s := newTestStore(t)
	const id = "keg-1"
	if err := s.SaveTap(&Tap{ID: "t1", TapNumber: intPtr(3), Name: "Midnight Oil",
		Style: "Oatmeal Stout", ABV: floatPtr(5.9), KegID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateKeg(id, func(k *Keg) { k.Label = "Garage Left" }); err != nil {
		t.Fatal(err)
	}
	kegIn(t, s, id, 1, 2, 18.0)
	pourDown(t, s, id, 17.6)

	pours := allPours(t, s)
	if len(pours) != 1 {
		t.Fatalf("got %d pours, want 1", len(pours))
	}
	p := pours[0]
	if !nearly(p.Amount, 0.4) || p.Unit != "litre" {
		t.Errorf("pour = %v %s, want 0.4 litre", p.Amount, p.Unit)
	}
	if p.BeerName != "Midnight Oil" || p.BeerStyle != "Oatmeal Stout" ||
		p.ABV == nil || *p.ABV != 5.9 || p.TapNumber == nil || *p.TapNumber != 3 ||
		p.ScaleLabel != "Garage Left" || p.KegID != id {
		t.Errorf("snapshot = %+v", p)
	}
	if p.StartedAt == 0 || p.EndedAt < p.StartedAt {
		t.Errorf("times = %d..%d", p.StartedAt, p.EndedAt)
	}

	// Kegging a new beer on the tap leaves the recorded pour alone.
	if err := s.SaveTap(&Tap{ID: "t1", TapNumber: intPtr(3), Name: "Backyard Pils", KegID: id}); err != nil {
		t.Fatal(err)
	}
	if got := allPours(t, s)[0].BeerName; got != "Midnight Oil" {
		t.Errorf("BeerName = %q after the tap changed, want the snapshot kept", got)
	}
}

// A pour that starts in the same packet as the first falling reading is
// measured from the amount before that packet.
func TestPourStartingMidPacketUsesThePriorAmount(t *testing.T) {
	s := newTestStore(t)
	const id = "keg-1"
	kegIn(t, s, id, 1, 2, 18.0)
	apply(t, s, id, "vw\x0049\x00255", "vw\x0051\x0017.900")
	apply(t, s, id, "vw\x0051\x0017.500", "vw\x0049\x000")

	pours := allPours(t, s)
	if len(pours) != 1 || !nearly(pours[0].Amount, 0.5) {
		t.Fatalf("pours = %+v, want one of 0.5", pours)
	}
}

func TestJitterWithoutThePouringFlagIsNotAPour(t *testing.T) {
	s := newTestStore(t)
	const id = "keg-1"
	kegIn(t, s, id, 1, 2, 18.0)
	for _, a := range []string{"17.900", "17.950", "17.800", "17.850"} {
		apply(t, s, id, "vw\x0051\x00"+a)
	}
	if n := len(allPours(t, s)); n != 0 {
		t.Errorf("got %d pours from jitter, want 0", n)
	}
}

func TestPourBelowTheMinimumIsDropped(t *testing.T) {
	s := newTestStore(t)
	const id = "keg-1"
	kegIn(t, s, id, 2, 2, 5.0) // US gallons; the 2 oz default is 0.0156 gal

	pourDown(t, s, id, 4.99) // about 1.3 oz
	if n := len(allPours(t, s)); n != 0 {
		t.Fatalf("got %d pours, want the 1.3 oz window dropped", n)
	}
	pourDown(t, s, id, 4.89) // about 12.8 oz
	if n := len(allPours(t, s)); n != 1 {
		t.Fatalf("got %d pours, want the 12.8 oz window recorded", n)
	}
}

func TestChangingTheMinimumOnlyAffectsFuturePours(t *testing.T) {
	s := newTestStore(t)
	const id = "keg-1"
	kegIn(t, s, id, 1, 2, 18.0)
	pourDown(t, s, id, 17.85) // 150 ml

	if err := s.SetMinPour(MinPour{Value: 200, Unit: MinPourUnitMl}); err != nil {
		t.Fatal(err)
	}
	if n := len(allPours(t, s)); n != 1 {
		t.Fatalf("got %d pours after raising the minimum, want the existing one kept", n)
	}
	pourDown(t, s, id, 17.70) // another 150 ml, now below the minimum
	if n := len(allPours(t, s)); n != 1 {
		t.Errorf("got %d pours, want the new 150 ml window dropped", n)
	}
}

func TestPourCap(t *testing.T) {
	for _, tc := range []struct {
		name          string
		unit, measure int
		start, end    float64
		want          int
	}{
		{"100 oz in litres", 1, 2, 18.0, 18.0 - 100*0.0295735, 1},
		{"130 oz in litres", 1, 2, 18.0, 18.0 - 130*0.0295735, 0},
		{"100 oz in lbs", 2, 1, 40.0, 40.0 - 100.0/128*3.78541/0.453592, 1},
		{"130 oz in lbs", 2, 1, 40.0, 40.0 - 130.0/128*3.78541/0.453592, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			kegIn(t, s, "keg-1", tc.unit, tc.measure, tc.start)
			pourDown(t, s, "keg-1", tc.end)
			if n := len(allPours(t, s)); n != tc.want {
				t.Errorf("got %d pours, want %d", n, tc.want)
			}
		})
	}
}

func TestCO2ScaleRecordsNoPours(t *testing.T) {
	s := newTestStore(t)
	const id = "keg-1"
	kegIn(t, s, id, 1, 1, 4.0)
	apply(t, s, id, "vw\x0088\x002")
	pourDown(t, s, id, 3.5)
	if n := len(allPours(t, s)); n != 0 {
		t.Errorf("got %d pours from a CO2 scale, want 0", n)
	}
}

func TestDisconnectEndsAPourInProgress(t *testing.T) {
	s := newTestStore(t)
	const id = "keg-1"
	kegIn(t, s, id, 1, 2, 18.0)
	apply(t, s, id, "vw\x0049\x00255")
	apply(t, s, id, "vw\x0051\x0017.600")

	if _, err := s.SetPouring(id, false); err != nil {
		t.Fatal(err)
	}
	pours := allPours(t, s)
	if len(pours) != 1 || !nearly(pours[0].Amount, 0.4) {
		t.Fatalf("pours = %+v, want one of 0.4", pours)
	}
	k, _ := s.GetKeg(id)
	if k.PourStartedAt != nil || k.PourStartAmount != nil {
		t.Errorf("open pour state = %v/%v, want cleared", k.PourStartedAt, k.PourStartAmount)
	}
}

func TestAPourInProgressSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keg.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const id = "keg-1"
	kegIn(t, s, id, 1, 2, 18.0)
	apply(t, s, id, "vw\x0049\x00255")
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	apply(t, s, id, "vw\x0051\x0017.700")
	apply(t, s, id, "vw\x0049\x000")
	pours := allPours(t, s)
	if len(pours) != 1 || !nearly(pours[0].Amount, 0.3) {
		t.Fatalf("pours = %+v, want one of 0.3", pours)
	}
}

func TestClearingHistoryHidesPoursFromTheKegOnly(t *testing.T) {
	s := newTestStore(t)
	kegIn(t, s, "keg-1", 1, 2, 18.0)
	kegIn(t, s, "keg-2", 1, 2, 18.0)
	pourDown(t, s, "keg-1", 17.6)
	pourDown(t, s, "keg-2", 17.6)

	if _, err := s.ClearLog("keg-1"); err != nil {
		t.Fatal(err)
	}
	from, to := time.Now().Add(-time.Hour), time.Now().Add(time.Minute)
	if got, _ := s.ListKegPours("keg-1", from, to); len(got) != 0 {
		t.Errorf("keg-1 history has %d pours after clearing, want 0", len(got))
	}
	if got, _ := s.ListKegPours("keg-2", from, to); len(got) != 1 {
		t.Errorf("keg-2 history has %d pours, want its own kept", len(got))
	}
	all := allPours(t, s)
	if len(all) != 2 {
		t.Fatalf("all pours = %d, want both kept", len(all))
	}
	for _, p := range all {
		if p.HiddenFromKeg != (p.KegID == "keg-1") {
			t.Errorf("pour on %s HiddenFromKeg = %v", p.KegID, p.HiddenFromKeg)
		}
	}

	// A later pour on the cleared keg shows in its history again.
	pourDown(t, s, "keg-1", 17.2)
	if got, _ := s.ListKegPours("keg-1", from, to); len(got) != 1 {
		t.Errorf("keg-1 history has %d pours after a new pour, want 1", len(got))
	}
}

func TestDeleteKegKeepsItsPoursInTheFullList(t *testing.T) {
	s := newTestStore(t)
	kegIn(t, s, "keg-1", 1, 2, 18.0)
	pourDown(t, s, "keg-1", 17.6)
	if err := s.DeleteKeg("keg-1"); err != nil {
		t.Fatal(err)
	}
	if all := allPours(t, s); len(all) != 1 || !all[0].HiddenFromKeg {
		t.Errorf("all pours = %+v, want the pour kept and hidden from the keg", all)
	}
}

func TestDeletePour(t *testing.T) {
	s := newTestStore(t)
	kegIn(t, s, "keg-1", 1, 2, 18.0)
	pourDown(t, s, "keg-1", 17.6)
	p := allPours(t, s)[0]

	if err := s.DeletePour(p.ID); err != nil {
		t.Fatalf("DeletePour: %v", err)
	}
	if n := len(allPours(t, s)); n != 0 {
		t.Errorf("got %d pours after deleting, want 0", n)
	}
	if err := s.DeletePour(p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting again = %v, want ErrNotFound", err)
	}
}

func TestListPoursRespectsTheWindow(t *testing.T) {
	s := newTestStore(t)
	for i, ended := range []int64{1_000, 2_000, 3_000} {
		if _, err := s.db.Exec(`INSERT INTO pours (keg_id, started_at, ended_at, amount, unit)
			VALUES ('keg-1', ?, ?, 0.4, 'litre')`, ended-10, ended); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	got, err := s.ListPours(time.Unix(1_500, 0), time.Unix(3_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].EndedAt != 3_000 || got[1].EndedAt != 2_000 {
		t.Errorf("pours = %+v, want 3000 then 2000", got)
	}
}

func TestConvertPours(t *testing.T) {
	pours := []*Pour{
		{Amount: 0.355, Unit: "litre"},
		{Amount: 0.1, Unit: "gal"},
	}
	ConvertPours(pours, follow())
	if d := pours[0].Display; !nearly(d.Amount, 355) || d.Unit != "ml" {
		t.Errorf("litre pour following the device = %+v, want 355 ml", d)
	}
	if d := pours[1].Display; !nearly(d.Amount, 12.8) || d.Unit != "oz" {
		t.Errorf("gal pour following the device = %+v, want 12.8 oz", d)
	}

	ConvertPours(pours, DisplayUnits{System: DisplaySystemUS, Measure: DisplayMeasureVolume})
	if d := pours[0].Display; !nearly(d.Amount, 0.355/3.78541*128) || d.Unit != "oz" {
		t.Errorf("litre pour shown in US = %+v", d)
	}
}

func TestMinPourIn(t *testing.T) {
	two := MinPour{Value: 2, Unit: MinPourUnitOz}
	if got := two.In("gal"); !nearly(got, 2.0/128) {
		t.Errorf("2 oz in gal = %v", got)
	}
	if got := two.In("litre"); !nearly(got, 2.0/128*3.78541) {
		t.Errorf("2 oz in litre = %v", got)
	}
	if got := (MinPour{Value: 60, Unit: MinPourUnitMl}).In("kg"); !nearly(got, 0.06) {
		t.Errorf("60 ml in kg = %v", got)
	}
}

func TestMinPourDefaultsAndRoundTrips(t *testing.T) {
	s := newTestStore(t)
	cfg, err := s.GetAppConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinPour != DefaultMinPour {
		t.Errorf("default MinPour = %+v, want %+v", cfg.MinPour, DefaultMinPour)
	}
	if err := s.SetMinPour(MinPour{Value: 90, Unit: "ML"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = s.GetAppConfig()
	if cfg.MinPour != (MinPour{Value: 90, Unit: MinPourUnitMl}) {
		t.Errorf("MinPour = %+v, want 90 ml", cfg.MinPour)
	}
}

func TestNormalizeMinPour(t *testing.T) {
	for _, tc := range []struct{ in, want MinPour }{
		{MinPour{3, "oz"}, MinPour{3, "oz"}},
		{MinPour{3, "cups"}, MinPour{3, "oz"}},
		{MinPour{-1, "ml"}, MinPour{0, "ml"}},
	} {
		if got := NormalizeMinPour(tc.in); got != tc.want {
			t.Errorf("NormalizeMinPour(%+v) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestWritePoursCSV(t *testing.T) {
	var buf bytes.Buffer
	tap := 2
	err := WritePoursCSV(&buf, []*Pour{{
		KegID: "keg-1", StartedAt: 1_700_000_000, EndedAt: 1_700_000_012,
		Amount: 0.4, Unit: "litre", BeerName: "Backyard Pils", BeerStyle: "German Pilsner",
		ABV: floatPtr(4.9), TapNumber: &tap, ScaleLabel: "Kegerator 2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	want := "2023-11-14T22:13:20Z,2023-11-14T22:13:32Z,0.4,litre,Backyard Pils,German Pilsner,4.9,2,Kegerator 2,keg-1"
	if lines[1] != want {
		t.Errorf("row = %q\nwant  %q", lines[1], want)
	}
}

func TestSetLatestPours(t *testing.T) {
	s := newTestStore(t)
	kegIn(t, s, "keg-1", 1, 2, 18.0)
	kegIn(t, s, "keg-2", 1, 2, 18.0)
	for i, ended := range []int64{1_000, 3_000, 2_000} {
		if _, err := s.db.Exec(`INSERT INTO pours (keg_id, started_at, ended_at, amount, unit)
			VALUES ('keg-1', ?, ?, ?, 'litre')`, ended-10, ended, 0.1*float64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	// The newest of all, but hidden from the keg's history.
	if _, err := s.db.Exec(`INSERT INTO pours (keg_id, started_at, ended_at, amount, unit, hidden_from_keg)
		VALUES ('keg-1', 3990, 4000, 0.9, 'litre', 1)`); err != nil {
		t.Fatal(err)
	}

	kegs, _ := s.ListKegs()
	if err := s.SetLatestPours(kegs, follow()); err != nil {
		t.Fatal(err)
	}
	for _, k := range kegs {
		switch k.ID {
		case "keg-1":
			if k.LatestPour == nil || k.LatestPour.EndedAt != 3_000 || !nearly(k.LatestPour.Display.Amount, 200) {
				t.Errorf("keg-1 latest = %+v, want the visible pour ending at 3000, 200 ml", k.LatestPour)
			}
		case "keg-2":
			if k.LatestPour != nil {
				t.Errorf("keg-2 latest = %+v, want none", k.LatestPour)
			}
		}
	}
}

// LatestPour is filled in for the browser only and never stored on the keg.
func TestLatestPourIsNeverStored(t *testing.T) {
	s := newTestStore(t)
	kegIn(t, s, "keg-1", 1, 2, 18.0)
	if _, err := s.UpdateKeg("keg-1", func(k *Keg) { k.LatestPour = &Pour{Amount: 1} }); err != nil {
		t.Fatal(err)
	}
	k, _ := s.GetKeg("keg-1")
	if k.LatestPour != nil {
		t.Errorf("LatestPour = %+v after a round trip, want nil", k.LatestPour)
	}
}
