package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"time"
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
	// Glass is the glass the tap list draws in place of a picture, one of
	// GlassStyles, or "" for the default pint.
	Glass string `json:"glass"`
	// Image is the URL of the tap's picture, or "" when it has none. It is
	// read from tap_images and never written through SaveTap; the version in
	// the query string changes whenever the picture does, so it can be cached.
	Image string `json:"image"`
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

// GlassStyles are the glasses a tap can show when it has no picture. The UI
// draws each; "" means the default, a pint.
var GlassStyles = []string{"pint", "nonic", "boston", "tulip", "snifter", "pilsner", "weizen", "goblet", "mug", "dimpled", "flute"}

// IsGlassStyle reports whether name is one of GlassStyles. The empty string
// means the default and is not one.
func IsGlassStyle(name string) bool {
	for _, g := range GlassStyles {
		if g == name {
			return true
		}
	}
	return false
}

// DefaultTapColor is the accent used when a tap has no colour set.
const DefaultTapColor = "#c9a849"

const tapColumns = `id, tap_number, name, brewery, style, abv, ibu, srm, color_preset, color,
	description, tasting_notes, kegged_date, keg_id, glass`

// tapSelect reads a tap with its picture's version, which scanTap expects.
const tapSelect = `SELECT t.id, t.tap_number, t.name, t.brewery, t.style, t.abv, t.ibu, t.srm,
	t.color_preset, t.color, t.description, t.tasting_notes, t.kegged_date, t.keg_id, t.glass,
	i.updated_at
	FROM taps t LEFT JOIN tap_images i ON i.tap_id = t.id`

func scanTap(row interface{ Scan(...any) error }) (*Tap, error) {
	t := &Tap{}
	var imageVersion *int64
	err := row.Scan(&t.ID, &t.TapNumber, &t.Name, &t.Brewery, &t.Style, &t.ABV, &t.IBU,
		&t.SRM, &t.ColorPreset, &t.Color, &t.Description, &t.TastingNotes, &t.KeggedDate, &t.KegID,
		&t.Glass, &imageVersion)
	if err != nil {
		return nil, err
	}
	if imageVersion != nil {
		t.Image = TapImageURL(t.ID, *imageVersion)
	}
	return t, nil
}

// TapImageURL is where the API serves a tap's picture, versioned so a
// replaced picture gets a new URL.
func TapImageURL(tapID string, version int64) string {
	return fmt.Sprintf("/api/taps/%s/image?v=%d", url.PathEscape(tapID), version)
}

// ListTaps returns every tap, ordered by tap number with unnumbered taps last.
func (s *Store) ListTaps() ([]*Tap, error) {
	rows, err := s.db.Query(tapSelect + `
		ORDER BY t.tap_number IS NULL, t.tap_number, t.name`)
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
	t, err := scanTap(s.db.QueryRow(tapSelect+` WHERE t.id = ?`, id))
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
		if err := checkKegFree(tx, t.KegID, t.ID); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO taps (`+tapColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.TapNumber, t.Name, t.Brewery, t.Style, t.ABV, t.IBU, t.SRM, t.ColorPreset, t.Color,
		t.Description, t.TastingNotes, t.KeggedDate, t.KegID, t.Glass); err != nil {
		return err
	}
	return tx.Commit()
}

// checkKegFree returns a KegInUseError if a tap other than tapID draws from
// kegID.
func checkKegFree(tx *sql.Tx, kegID, tapID string) error {
	other := &Tap{}
	err := tx.QueryRow(
		"SELECT id, tap_number, name FROM taps WHERE keg_id = ? AND id != ? ORDER BY tap_number LIMIT 1",
		kegID, tapID,
	).Scan(&other.ID, &other.TapNumber, &other.Name)
	switch {
	case err == nil:
		return &KegInUseError{Tap: other}
	case errors.Is(err, sql.ErrNoRows):
		return nil
	default:
		return err
	}
}

// TapLink sets the keg a tap draws from; an empty KegID unlinks it.
type TapLink struct {
	TapID string `json:"tap_id"`
	KegID string `json:"keg_id"`
}

// LinkTaps changes which keg each named tap draws from, all at once, which is
// how the Tap Setup board saves a drop. Every link is applied before any keg
// is checked, so two taps can swap kegs in one call; a keg left on two taps
// fails the whole call with a KegInUseError. Only the kegs named are checked,
// so a database that already has some other keg on two taps can still be
// rearranged. An unknown tap id fails it with ErrNotFound.
func (s *Store) LinkTaps(links []TapLink) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, l := range links {
		res, err := tx.Exec("UPDATE taps SET keg_id = ? WHERE id = ?", l.KegID, l.TapID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrNotFound
		}
	}
	for _, l := range links {
		if l.KegID == "" {
			continue
		}
		if err := checkKegFree(tx, l.KegID, l.TapID); err != nil {
			return err
		}
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

// DeleteTap removes a tap and its picture.
func (s *Store) DeleteTap(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM tap_images WHERE tap_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM taps WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// TapImage is a tap's picture as stored.
type TapImage struct {
	ContentType string
	Data        []byte
	// UpdatedAt is when it was stored, in unix milliseconds.
	UpdatedAt int64
}

// SetTapImage stores a tap's picture, replacing any it had. An unknown tap is
// ErrNotFound. The caller checks that the bytes are an image.
func (s *Store) SetTapImage(tapID, contentType string, data []byte) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists int
	err = tx.QueryRow("SELECT 1 FROM taps WHERE id = ?", tapID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}

	// Kept strictly increasing, so two saves within a millisecond still give
	// the picture a new URL.
	version := time.Now().UnixMilli()
	var previous int64
	err = tx.QueryRow("SELECT updated_at FROM tap_images WHERE tap_id = ?", tapID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	version = max(version, previous+1)

	if _, err := tx.Exec(`INSERT OR REPLACE INTO tap_images (tap_id, content_type, data, updated_at)
		VALUES (?, ?, ?, ?)`, tapID, contentType, data, version); err != nil {
		return err
	}
	return tx.Commit()
}

// GetTapImage returns a tap's picture, or ErrNotFound when it has none.
func (s *Store) GetTapImage(tapID string) (*TapImage, error) {
	img := &TapImage{}
	err := s.db.QueryRow("SELECT content_type, data, updated_at FROM tap_images WHERE tap_id = ?", tapID).
		Scan(&img.ContentType, &img.Data, &img.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return img, err
}

// DeleteTapImage removes a tap's picture. A tap with none is left as it is.
func (s *Store) DeleteTapImage(tapID string) error {
	_, err := s.db.Exec("DELETE FROM tap_images WHERE tap_id = ?", tapID)
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
