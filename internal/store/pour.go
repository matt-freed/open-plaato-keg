package store

import (
	"database/sql"
	"encoding/csv"
	"errors"
	"io"
	"log/slog"
	"strconv"
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

func (s *Store) queryPours(where string, args ...any) ([]*Pour, error) {
	rows, err := s.db.Query("SELECT "+pourColumns+" FROM pours WHERE "+where+
		" ORDER BY ended_at DESC, id DESC", args...)
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
	return s.queryPours("keg_id = ? AND hidden_from_keg = 0 AND ended_at >= ? AND ended_at <= ?",
		id, from.Unix(), to.Unix())
}

// ListPours returns every pour, from every keg, between two times, newest
// first. A zero from means all time.
func (s *Store) ListPours(from, to time.Time) ([]*Pour, error) {
	lo := int64(0)
	if !from.IsZero() {
		lo = from.Unix()
	}
	return s.queryPours("ended_at >= ? AND ended_at <= ?", lo, to.Unix())
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
		to := displayAmountUnit(p.Unit, u)
		mult, label := units.PourSubUnit(to)
		p.Display = &PourDisplay{
			Amount: units.ConvertAmount(p.Amount, p.Unit, to) * mult,
			Unit:   label,
		}
	}
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
