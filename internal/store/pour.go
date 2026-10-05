package store

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/matt-freed/open-plaato-keg/internal/units"
)

// A pour is one pouring window reported by the keg: it starts when is_pouring
// turns on and ends when it turns off, or when the keg disconnects. Its size is
// the amount left just before the window opened minus the amount left when it
// closed. Scale jitter outside a window can therefore never become a pour.
//
// A window is recorded only if the keg is in beer mode, both amounts are
// known, and the size is between the configured minimum and maxPourGal.

// maxPourGal caps a plausible pour at 128 US fl oz, converted into the keg's
// own unit when checked. Anything larger is a lifted keg or a vibration spike
// rather than a pitcher or growler fill.
const maxPourGal = 1.0

// Pour is one recorded pour, with a copy of what was on tap at the time.
type Pour struct {
	ID        int64   `json:"id"`
	KegID     string  `json:"keg_id"`
	StartedAt int64   `json:"started_at"`
	EndedAt   int64   `json:"ended_at"`
	Amount    float64 `json:"amount"`
	// Unit is the keg's beer_left_unit when the pour happened. Pours carry
	// their own unit, unlike the minute log, so a scale switched between units
	// later does not reinterpret them.
	Unit       string   `json:"unit"`
	BeerName   string   `json:"beer_name"`
	BeerStyle  string   `json:"beer_style"`
	ABV        *float64 `json:"abv"`
	TapNumber  *int     `json:"tap_number"`
	ScaleLabel string   `json:"scale_label"`
	// HiddenFromKeg is set once the scale's history has been cleared.
	HiddenFromKeg bool `json:"hidden_from_keg"`
	// Display is filled in only where the browser reads pours.
	Display *PourDisplay `json:"display,omitempty"`
}

// PourDisplay is a pour's size in the user's display units, scaled to a
// pour-sized sub-unit such as oz or ml.
type PourDisplay struct {
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit"`
}

// trackPour advances the pour state machine after a change to k.
//
// wasPouring and prevAmount are the keg's values before the change, so a pour
// that starts in the same packet as the first falling reading is measured from
// the amount before it.
func trackPour(tx *sql.Tx, k *Keg, wasPouring bool, prevAmount *float64, now time.Time) error {
	pouring := k.IsPouring != nil && *k.IsPouring

	switch {
	case pouring && !wasPouring:
		k.PourStartedAt = nil
		k.PourStartAmount = nil
		if prevAmount != nil {
			at := now.Unix()
			start := *prevAmount
			k.PourStartedAt, k.PourStartAmount = &at, &start
		}
		return nil

	case !pouring && wasPouring:
		startedAt, startAmount := k.PourStartedAt, k.PourStartAmount
		k.PourStartedAt, k.PourStartAmount = nil, nil
		if startedAt == nil || startAmount == nil || k.AmountLeft == nil {
			return nil
		}
		if k.KegMode != nil && *k.KegMode == 2 {
			return nil // a CO2 scale's drops are gas, not beer
		}
		unit := k.DeriveBeerLeftUnit()
		amount := *startAmount - *k.AmountLeft

		minPour, err := minPourTx(tx)
		if err != nil {
			return err
		}
		lo, hi := minPour.In(unit), units.ConvertAmount(maxPourGal, "gal", unit)
		if amount < lo || amount <= 0 || amount > hi {
			slog.Debug("pouring window not recorded as a pour",
				"keg", k.ID, "amount", amount, "unit", unit, "min", lo, "max", hi)
			return nil
		}
		return insertPour(tx, k, *startedAt, now.Unix(), amount, unit)
	}
	return nil
}

// insertPour records a pour along with a copy of the first tap, by tap number,
// that draws from the keg — the same tap the History page labels it by.
func insertPour(tx *sql.Tx, k *Keg, startedAt, endedAt int64, amount float64, unit string) error {
	var name, style string
	var abv sql.NullFloat64
	var tapNumber sql.NullInt64
	err := tx.QueryRow(
		`SELECT name, style, abv, tap_number FROM taps WHERE keg_id = ?
		 ORDER BY tap_number IS NULL, tap_number, id LIMIT 1`, k.ID).
		Scan(&name, &style, &abv, &tapNumber)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(
		`INSERT INTO pours (keg_id, started_at, ended_at, amount, unit,
		  beer_name, beer_style, abv, tap_number, scale_label)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		k.ID, startedAt, endedAt, amount, unit, name, style, abv, tapNumber, k.Label)
	return err
}

const pourColumns = `id, keg_id, started_at, ended_at, amount, unit,
	beer_name, beer_style, abv, tap_number, scale_label, hidden_from_keg`

// queryPours returns the pours matching where, newest first, at most limit of
// them when limit is positive, skipping the first offset.
func (s *Store) queryPours(where string, limit, offset int, args ...any) ([]*Pour, error) {
	query := "SELECT " + pourColumns + " FROM pours WHERE " + where + " ORDER BY ended_at DESC, id DESC"
	if limit > 0 {
		query += " LIMIT " + strconv.Itoa(limit) + " OFFSET " + strconv.Itoa(max(offset, 0))
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pours := []*Pour{}
	for rows.Next() {
		p := &Pour{}
		var tapNumber sql.NullInt64
		if err := rows.Scan(&p.ID, &p.KegID, &p.StartedAt, &p.EndedAt, &p.Amount, &p.Unit,
			&p.BeerName, &p.BeerStyle, &p.ABV, &tapNumber, &p.ScaleLabel, &p.HiddenFromKeg); err != nil {
			return nil, err
		}
		if tapNumber.Valid {
			n := int(tapNumber.Int64)
			p.TapNumber = &n
		}
		pours = append(pours, p)
	}
	return pours, rows.Err()
}

// ListKegPours returns the pours shown in one keg's history between two
// times, newest first. Pours hidden by clearing that history are left out.
func (s *Store) ListKegPours(id string, from, to time.Time) ([]*Pour, error) {
	return s.queryPours("keg_id = ? AND hidden_from_keg = 0 AND ended_at >= ? AND ended_at <= ?", 0, 0,
		id, from.Unix(), to.Unix())
}

// ListPours returns every pour, from every keg, between two times, newest
// first. A zero from means all time.
func (s *Store) ListPours(from, to time.Time) ([]*Pour, error) {
	lo := int64(0)
	if !from.IsZero() {
		lo = from.Unix()
	}
	return s.queryPours("ended_at >= ? AND ended_at <= ?", 0, 0, lo, to.Unix())
}

// SetLatestPours fills in each keg's LatestPour: the newest pour in its
// history, converted into the display units, or nil if it has none. A pour
// hidden by clearing the history does not count, so a cleared scale shows no
// last pour until it pours again.
func (s *Store) SetLatestPours(kegs []*Keg, u DisplayUnits) error {
	for _, k := range kegs {
		pours, err := s.queryPours("keg_id = ? AND hidden_from_keg = 0", 1, 0, k.ID)
		if err != nil {
			return err
		}
		k.LatestPour = nil
		if len(pours) > 0 {
			ConvertPours(pours, u)
			k.LatestPour = pours[0]
		}
	}
	return nil
}

// DeletePour removes one pour, or returns ErrNotFound.
func (s *Store) DeletePour(id int64) error {
	res, err := s.db.Exec("DELETE FROM pours WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ConvertPours fills in each pour's display block.
//
// Each pour is converted from its own stored unit. Even when the display
// follows the device, the size is scaled to a sub-unit, so a pour reads as
// 12 oz rather than 0.094 gal.
func ConvertPours(pours []*Pour, u DisplayUnits) {
	for _, p := range pours {
		amount, label := pourDisplay(p.Amount, p.Unit, u)
		p.Display = &PourDisplay{Amount: amount, Unit: label}
	}
}

// pourDisplay converts a pour-sized amount stored in unit into the display
// units, scaled to a pour-sized sub-unit. It is linear, so a sum of pours in
// one unit converts the same as the sum of their conversions.
func pourDisplay(amount float64, unit string, u DisplayUnits) (float64, string) {
	to := displayAmountUnit(unit, u)
	mult, label := units.PourSubUnit(to)
	return units.ConvertAmount(amount, unit, to) * mult, label
}

// pourAmountToStored is the inverse of pourDisplay: an amount entered in the
// display sub-unit, converted back into the pour's stored unit.
func pourAmountToStored(amount float64, unit string, u DisplayUnits) float64 {
	to := displayAmountUnit(unit, u)
	mult, _ := units.PourSubUnit(to)
	return units.ConvertAmount(amount/mult, to, unit)
}

// WritePoursCSV writes pours as CSV in their stored units. Each row names its
// unit, so pours from scales set to different units stay unambiguous.
func WritePoursCSV(w io.Writer, pours []*Pour) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{
		"started_at", "ended_at", "amount", "unit", "beer_name", "beer_style",
		"abv", "tap_number", "scale_label", "keg_id",
	}); err != nil {
		return err
	}
	for _, p := range pours {
		tap := ""
		if p.TapNumber != nil {
			tap = strconv.Itoa(*p.TapNumber)
		}
		if err := cw.Write([]string{
			time.Unix(p.StartedAt, 0).UTC().Format(time.RFC3339),
			time.Unix(p.EndedAt, 0).UTC().Format(time.RFC3339),
			strconv.FormatFloat(p.Amount, 'f', -1, 64),
			p.Unit,
			p.BeerName,
			p.BeerStyle,
			formatFloat(p.ABV),
			tap,
			p.ScaleLabel,
			p.KegID,
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// PourFilter selects pours for All Pours. A zero From means all time. Beer,
// when set, matches the trimmed beer name, so "" selects pours with no beer.
// KegID, when set, selects one scale's pours.
type PourFilter struct {
	From, To time.Time
	Beer     *string
	KegID    string
}

// where builds the SQL condition and arguments for the filter.
func (f PourFilter) where() (string, []any) {
	lo := int64(0)
	if !f.From.IsZero() {
		lo = f.From.Unix()
	}
	cond := "ended_at >= ? AND ended_at <= ?"
	args := []any{lo, f.To.Unix()}
	if f.Beer != nil {
		cond += " AND TRIM(beer_name) = ?"
		args = append(args, strings.TrimSpace(*f.Beer))
	}
	if f.KegID != "" {
		cond += " AND keg_id = ?"
		args = append(args, f.KegID)
	}
	return cond, args
}

// ListPoursPage returns up to limit of the filtered pours, newest first,
// after skipping offset of them. A limit of zero or less returns them all.
func (s *Store) ListPoursPage(f PourFilter, limit, offset int) ([]*Pour, error) {
	cond, args := f.where()
	return s.queryPours(cond, limit, offset, args...)
}

// PourTotal is an amount poured in one display unit. Scales set to different
// unit systems can pour in both oz and ml, which are never summed together.
type PourTotal struct {
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit"`
}

// BeerPours is one beer's share of a PourSummary. Name is the trimmed beer
// name, "" for pours with no beer on tap.
type BeerPours struct {
	Name   string      `json:"name"`
	Count  int         `json:"count"`
	Totals []PourTotal `json:"totals"`
}

// PourSummary counts and totals the filtered pours, overall and per beer, in
// the display units. Beers are ordered by count, most first.
type PourSummary struct {
	Count  int         `json:"count"`
	Totals []PourTotal `json:"totals"`
	ByBeer []BeerPours `json:"by_beer"`
}

// SummarizePours counts and totals the filtered pours in the display units.
func (s *Store) SummarizePours(f PourFilter, u DisplayUnits) (PourSummary, error) {
	cond, args := f.where()
	rows, err := s.db.Query(
		"SELECT TRIM(beer_name), unit, COUNT(*), SUM(amount) FROM pours WHERE "+cond+
			" GROUP BY 1, 2 ORDER BY 1, 2", args...)
	if err != nil {
		return PourSummary{}, err
	}
	defer rows.Close()

	sum := PourSummary{Totals: []PourTotal{}, ByBeer: []BeerPours{}}
	beers := map[string]int{}
	for rows.Next() {
		var name, unit string
		var count int
		var amount float64
		if err := rows.Scan(&name, &unit, &count, &amount); err != nil {
			return PourSummary{}, err
		}
		shown, label := pourDisplay(amount, unit, u)
		i, ok := beers[name]
		if !ok {
			i = len(sum.ByBeer)
			beers[name] = i
			sum.ByBeer = append(sum.ByBeer, BeerPours{Name: name, Totals: []PourTotal{}})
		}
		sum.ByBeer[i].Count += count
		sum.ByBeer[i].Totals = addPourTotal(sum.ByBeer[i].Totals, shown, label)
		sum.Count += count
		sum.Totals = addPourTotal(sum.Totals, shown, label)
	}
	if err := rows.Err(); err != nil {
		return PourSummary{}, err
	}
	slices.SortStableFunc(sum.ByBeer, func(a, b BeerPours) int { return b.Count - a.Count })
	return sum, nil
}

func addPourTotal(totals []PourTotal, amount float64, unit string) []PourTotal {
	for i := range totals {
		if totals[i].Unit == unit {
			totals[i].Amount += amount
			return totals
		}
	}
	return append(totals, PourTotal{Amount: amount, Unit: unit})
}

// PourScale is a scale All Pours can filter by: its keg id and the label on
// its newest pour.
type PourScale struct {
	KegID string `json:"keg_id"`
	Label string `json:"label"`
}

// PourFilterOptions lists the beers and scales with pours between two times,
// whatever beer or scale is chosen, so a choice never vanishes from its own
// list. Beers are trimmed names, "" for no beer on tap.
func (s *Store) PourFilterOptions(from, to time.Time) (beers []string, scales []PourScale, err error) {
	cond, args := PourFilter{From: from, To: to}.where()
	beers, scales = []string{}, []PourScale{}

	rows, err := s.db.Query("SELECT DISTINCT TRIM(beer_name) FROM pours WHERE "+cond+" ORDER BY 1", args...)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, nil, err
		}
		beers = append(beers, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	// SQLite takes the bare column from the row holding MAX(), so each keg
	// carries the label of its newest pour.
	rows, err = s.db.Query(
		"SELECT keg_id, TRIM(scale_label), MAX(ended_at) FROM pours WHERE "+cond+" GROUP BY keg_id ORDER BY 2, 1",
		args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sc PourScale
		var newest int64
		if err := rows.Scan(&sc.KegID, &sc.Label, &newest); err != nil {
			return nil, nil, err
		}
		scales = append(scales, sc)
	}
	return beers, scales, rows.Err()
}

// PourEdit changes one stored pour, identified by its id. Only the values
// marked Set are written. Amount is in the display units' pour-sized
// sub-unit and is converted back into the pour's own unit. A text value
// cleared with a nil Value is stored as empty.
type PourEdit struct {
	ID         int64
	Amount     LogValue[float64]
	BeerName   LogValue[string]
	BeerStyle  LogValue[string]
	ABV        LogValue[float64]
	TapNumber  LogValue[int]
	ScaleLabel LogValue[string]
}

// UpdatePours applies edits to stored pours in one transaction. If any edited
// pour no longer exists nothing is changed and ErrNotFound is returned.
//
// A hand edit is a correction, so the minimum and maximum pour sizes that
// trackPour applies when recording are not checked again.
func (s *Store) UpdatePours(edits []PourEdit, u DisplayUnits) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	text := func(v LogValue[string]) string {
		if v.Value == nil {
			return ""
		}
		return strings.TrimSpace(*v.Value)
	}
	for _, e := range edits {
		var unit string
		if err := tx.QueryRow("SELECT unit FROM pours WHERE id = ?", e.ID).Scan(&unit); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: pour %d", ErrNotFound, e.ID)
			}
			return err
		}

		var sets []string
		var args []any
		if e.Amount.Set {
			if e.Amount.Value == nil {
				return errors.New("a pour's amount cannot be cleared")
			}
			sets = append(sets, "amount = ?")
			args = append(args, pourAmountToStored(*e.Amount.Value, unit, u))
		}
		if e.BeerName.Set {
			sets, args = append(sets, "beer_name = ?"), append(args, text(e.BeerName))
		}
		if e.BeerStyle.Set {
			sets, args = append(sets, "beer_style = ?"), append(args, text(e.BeerStyle))
		}
		if e.ABV.Set {
			sets, args = append(sets, "abv = ?"), append(args, e.ABV.Value)
		}
		if e.TapNumber.Set {
			sets, args = append(sets, "tap_number = ?"), append(args, e.TapNumber.Value)
		}
		if e.ScaleLabel.Set {
			sets, args = append(sets, "scale_label = ?"), append(args, text(e.ScaleLabel))
		}
		if len(sets) == 0 {
			continue
		}
		if _, err := tx.Exec("UPDATE pours SET "+strings.Join(sets, ", ")+" WHERE id = ?",
			append(args, e.ID)...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeletePours removes the pours with the given ids in one transaction and
// returns how many were removed. Ids with no pour are ignored.
func (s *Store) DeletePours(ids []int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var removed int64
	for _, id := range ids {
		res, err := tx.Exec("DELETE FROM pours WHERE id = ?", id)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		removed += n
	}
	return removed, tx.Commit()
}
