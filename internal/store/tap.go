package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

// Tap is one configured tap on the tap list.
type Tap struct {
	ID string `json:"id"`
	// TapNumber orders the tap list; nil sorts last.
	TapNumber *int     `json:"tap_number"`
	Name      string   `json:"name"`
	Brewery   string   `json:"brewery"`
	Style     string   `json:"style"`
	ABV       *float64 `json:"abv"`
	IBU       *float64 `json:"ibu"`
	// SRM is the beer's colour; the tap list draws the keg in it.
	SRM *float64 `json:"srm"`
	// ColorPreset names a colour for drinks SRM cannot describe. At most one of
	// SRM and ColorPreset is set.
	ColorPreset string `json:"color_preset"`
	// Color is the hand-picked accent from before SRM existed, used only when
	// neither SRM nor ColorPreset is set.
	Color        string `json:"color"`
	Description  string `json:"description"`
	TastingNotes string `json:"tasting_notes"`
	// KeggedDate is when the beer was kegged, as DateLayout, or "".
	KeggedDate string `json:"kegged_date"`
	// KegID links the tap to a keg, so the tap list can show what is left.
	KegID string `json:"keg_id"`
}

// ColorPresets are the named colours offered beside SRM, for drinks the SRM
// scale cannot describe. The UI decides how each is drawn.
var ColorPresets = []string{"clear", "pink", "red", "purple", "green", "blue"}

// IsColorPreset reports whether name is one of ColorPresets. The empty string
// means no preset and is not one.
func IsColorPreset(name string) bool {
	for _, p := range ColorPresets {
		if p == name {
			return true
		}
	}
	return false
}

// DefaultTapColor is the accent used when a tap has no colour set.
const DefaultTapColor = "#c9a849"

const tapColumns = `id, tap_number, name, brewery, style, abv, ibu, srm, color_preset, color,
	description, tasting_notes, kegged_date, keg_id`

func scanTap(row interface{ Scan(...any) error }) (*Tap, error) {
	t := &Tap{}
	err := row.Scan(&t.ID, &t.TapNumber, &t.Name, &t.Brewery, &t.Style, &t.ABV, &t.IBU,
		&t.SRM, &t.ColorPreset, &t.Color, &t.Description, &t.TastingNotes, &t.KeggedDate, &t.KegID)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// ListTaps returns every tap, ordered by tap number with unnumbered taps last.
func (s *Store) ListTaps() ([]*Tap, error) {
	rows, err := s.db.Query(`SELECT ` + tapColumns + ` FROM taps
		ORDER BY tap_number IS NULL, tap_number, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	taps := []*Tap{}
	for rows.Next() {
		t, err := scanTap(rows)
		if err != nil {
			return nil, err
		}
		taps = append(taps, t)
	}
	return taps, rows.Err()
}

// GetTap returns one tap, or ErrNotFound.
func (s *Store) GetTap(id string) (*Tap, error) {
	t, err := scanTap(s.db.QueryRow(`SELECT `+tapColumns+` FROM taps WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// KegInUseError is returned when a tap is given a keg that another tap already
// draws from. A keg feeds one tap; it has to be unlinked from that tap before
// it can be linked to another.
type KegInUseError struct {
	Tap *Tap
}

func (e *KegInUseError) Error() string {
	return fmt.Sprintf("keg is already linked to tap %s", e.Tap.ID)
}

// SaveTap inserts or replaces a tap, refusing a keg another tap already uses.
//
// The check and the write share a transaction, and the store has a single
// connection, so two saves cannot both claim the same keg. It is enforced
// here rather than by a unique index so a database that already has a keg on
// two taps still opens.
func (s *Store) SaveTap(t *Tap) error {
	if t.Color == "" {
		t.Color = DefaultTapColor
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if t.KegID != "" {
		other := &Tap{}
		err := tx.QueryRow(
			"SELECT id, tap_number, name FROM taps WHERE keg_id = ? AND id != ? ORDER BY tap_number LIMIT 1",
			t.KegID, t.ID,
		).Scan(&other.ID, &other.TapNumber, &other.Name)
		switch {
		case err == nil:
			return &KegInUseError{Tap: other}
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO taps (`+tapColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.TapNumber, t.Name, t.Brewery, t.Style, t.ABV, t.IBU, t.SRM, t.ColorPreset, t.Color,
		t.Description, t.TastingNotes, t.KeggedDate, t.KegID); err != nil {
		return err
	}
	return tx.Commit()
}

// OrderTaps renumbers taps 1..n in the order given, which is how the tap list
// persists a drag-and-drop rearrangement. Taps not named keep their number.
// An unknown id fails the whole reorder with ErrNotFound.
func (s *Store) OrderTaps(ids []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for i, id := range ids {
		res, err := tx.Exec("UPDATE taps SET tap_number = ? WHERE id = ?", i+1, id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrNotFound
		}
	}
	return tx.Commit()
}

// DeleteTap removes a tap.
func (s *Store) DeleteTap(id string) error {
	_, err := s.db.Exec("DELETE FROM taps WHERE id = ?", id)
	return err
}

// NewID returns a short random identifier for a new record.
func NewID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail in practice; a readable panic beats
		// silently issuing colliding ids.
		panic("open-plaato-keg: cannot read random bytes: " + err.Error())
	}
	return hex.EncodeToString(b)
}
